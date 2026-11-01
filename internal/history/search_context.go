package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/ncruces/go-sqlite3"
)

type searchRunInfo struct {
	first, last            int64
	number                 int
	title, workspace, tool string
}

type searchReader struct {
	conn     *sqlite3.Conn
	through  int64
	sessions map[string]map[string]*searchRunInfo
}

// Cache only requested runs. Bounds and start events use the format 7 run indexes.
func (r *searchReader) run(sid, rid string) (*searchRunInfo, error) {
	if r.sessions == nil {
		r.sessions = map[string]map[string]*searchRunInfo{}
	}
	runs := r.sessions[sid]
	if runs == nil {
		runs = map[string]*searchRunInfo{}
		r.sessions[sid] = runs
	}
	if run := runs[rid]; run != nil {
		return run, nil
	}
	stmt, err := searchStatement(r.conn, `SELECT s.title,s.workspace,COALESCE(p.data,'{}'),
        (SELECT count(*) FROM runs prior WHERE prior.session_id=r.session_id AND prior.rowid<=r.rowid)
        FROM runs r JOIN sessions s ON s.id=r.session_id
        LEFT JOIN preparations p ON p.session_id=s.id WHERE r.id=? AND r.session_id=?`, rid, sid)
	if err != nil {
		return nil, err
	}
	if !stmt.Step() {
		err = errors.Join(ErrRunNotFound, stmt.Err())
		stmt.Close()
		return nil, err
	}
	run := &searchRunInfo{title: stmt.ColumnText(0), workspace: stmt.ColumnText(1), number: stmt.ColumnInt(3)}
	var preparation Preparation
	json.Unmarshal([]byte(stmt.ColumnText(2)), &preparation)
	stmt.Close()
	for _, bound := range []struct {
		order  string
		target *int64
	}{{"ASC", &run.first}, {"DESC", &run.last}} {
		stmt, err = searchStatement(r.conn, outputBoundSQL+" ORDER BY seq "+bound.order+" LIMIT 1", rid, sid, r.through)
		if err != nil {
			return nil, err
		}
		if stmt.Step() {
			*bound.target = stmt.ColumnInt64(0)
		}
		err = stmt.Err()
		stmt.Close()
		if err != nil {
			return nil, err
		}
	}
	stmt, err = searchStatement(r.conn, startEventSQL+" AND seq<=? ORDER BY seq LIMIT 1", rid, sid, r.through)
	if err != nil {
		return nil, err
	}
	var start []byte
	if stmt.Step() {
		start = stmt.ColumnBlob(0, nil)
	}
	err = stmt.Err()
	stmt.Close()
	if err != nil {
		return nil, err
	}
	run.tool = RunTool(start, run.number == 1, &preparation)
	runs[rid] = run
	return run, nil
}

const outputBoundSQL = `SELECT seq FROM events WHERE run_id=? AND session_id=?
    AND kind='output' AND text<>'' AND seq<=?`
const startEventSQL = `SELECT data FROM events WHERE run_id=? AND session_id=? AND kind='start'`

// before reads at most count characters, with source positions for matcher lookbehind.
func (r *searchReader) before(sid, rid string, seq int64, offset, count int, first int64) ([]searchPosition, error) {
	stmt, err := searchStatement(r.conn, `SELECT seq,text FROM events
        WHERE session_id=? AND run_id=? AND kind='output' AND text<>'' AND seq>=? AND seq<=? ORDER BY seq DESC`, sid, rid, first, seq)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	result := make([]searchPosition, 0, count)
	for len(result) < count && stmt.Step() {
		eventSeq := stmt.ColumnInt64(0)
		text := []rune(stmt.ColumnText(1))
		if eventSeq == seq {
			if offset > len(text) {
				return nil, errors.New("invalid history character offset")
			}
			text = text[:offset]
		}
		for i := len(text) - 1; i >= 0 && len(result) < count; i-- {
			result = append(result, searchPosition{eventSeq, i, text[i]})
		}
	}
	slices.Reverse(result)
	return result, stmt.Err()
}

func (s *Store) SearchContext(ctx context.Context, request ContextRequest) (result SearchContext, err error) {
	a := request.Anchor
	if a.Session == "" || a.Run == "" || a.Seq <= 0 || a.Through < a.Seq || a.Offset < 0 || a.Length < 1 || a.Length > 256 || request.Before < 0 || request.Before > contextRunes || request.After < 0 || request.After > contextRunes {
		return result, errors.New("invalid history context range")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := s.readConnection(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	reader := searchReader{conn: conn, through: a.Through}
	run, err := reader.run(a.Session, a.Run)
	if err != nil {
		return result, err
	}
	anchor, err := searchStatement(conn, `SELECT text FROM events WHERE seq=? AND session_id=? AND run_id=? AND kind='output'`, a.Seq, a.Session, a.Run)
	if err != nil {
		return result, err
	}
	valid := anchor.Step() && a.Offset < utf8.RuneCountInString(anchor.ColumnText(0))
	err = anchor.Err()
	anchor.Close()
	if err != nil {
		return result, err
	}
	if !valid {
		return result, errors.New("history match no longer exists")
	}
	left, err := reader.before(a.Session, a.Run, a.Seq, a.Offset, request.Before+1, run.first)
	if err != nil {
		return result, err
	}
	if len(left) > request.Before {
		result.MoreBefore = true
		left = left[1:]
	}
	result.Before = positionText(left)
	result.positions.before = left
	after, err := searchStatement(conn, `SELECT seq,text FROM events
        WHERE session_id=? AND run_id=? AND kind='output' AND text<>'' AND seq>=? AND seq<=? ORDER BY seq`, a.Session, a.Run, a.Seq, run.last)
	if err != nil {
		return result, err
	}
	defer after.Close()
	var right []rune
	var rightPositions []searchPosition
	for len(right) < a.Length+request.After+1 && after.Step() {
		text := []rune(after.ColumnText(1))
		if after.ColumnInt64(0) == a.Seq {
			text = text[a.Offset:]
		}
		count := min(len(text), a.Length+request.After+1-len(right))
		if result.LastSeq == 0 && len(right)+count >= a.Length {
			result.LastSeq = after.ColumnInt64(0)
		}
		start := 0
		if after.ColumnInt64(0) == a.Seq {
			start = a.Offset
		}
		for i, char := range text[:count] {
			rightPositions = append(rightPositions, searchPosition{after.ColumnInt64(0), start + i, char})
		}
		right = append(right, text[:count]...)
	}
	if err = after.Err(); err != nil {
		return result, err
	}
	if len(right) < a.Length {
		return result, errors.New("history context does not contain the complete match")
	}
	result.Match = string(right[:a.Length])
	result.positions.match = rightPositions[:a.Length]
	right = right[a.Length:]
	if len(right) > request.After {
		result.MoreAfter = true
		right = right[:request.After]
	}
	result.After = string(right)
	result.positions.after = rightPositions[a.Length : a.Length+len(right)]
	(&searchDisplayReader{conn: conn, through: a.Through}).normalize(a.Session, a.Run, &result)
	return result, nil
}

func searchStatement(conn *sqlite3.Conn, sql string, args ...any) (*sqlite3.Stmt, error) {
	stmt, _, err := conn.Prepare(sql)
	if err != nil {
		return nil, err
	}
	for i, arg := range args {
		switch value := arg.(type) {
		case string:
			err = stmt.BindText(i+1, value)
		case int64:
			err = stmt.BindInt64(i+1, value)
		default:
			err = fmt.Errorf("unsupported search parameter %T", value)
		}
		if err != nil {
			stmt.Close()
			return nil, err
		}
	}
	return stmt, nil
}
