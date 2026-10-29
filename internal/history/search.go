package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ncruces/go-sqlite3"
)

const (
	searchBytes  = 32 << 20
	searchEvents = 100000
	contextRunes = 8192
)

type SearchFilter struct {
	Query     string `json:"query"`
	Scope     string `json:"scope"`
	Session   string `json:"session,omitempty"`
	Project   string `json:"project,omitempty"`
	MatchCase bool   `json:"matchCase"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Through   int64  `json:"through,omitempty"`
}

// SearchAnchor identifies characters in original event text. Offset counts Unicode
// characters within one event. Through fixes the recording prefix for later reads.
type SearchAnchor struct {
	Session string `json:"session"`
	Run     string `json:"run"`
	Seq     int64  `json:"seq"`
	Offset  int    `json:"offset"`
	Length  int    `json:"length"`
	Through int64  `json:"through"`
}

type ContextRequest struct {
	Anchor SearchAnchor `json:"anchor"`
	Before int          `json:"before"`
	After  int          `json:"after"`
}

type SearchContext struct {
	Before     string `json:"before"`
	Match      string `json:"match"`
	After      string `json:"after"`
	MoreBefore bool   `json:"moreBefore"`
	MoreAfter  bool   `json:"moreAfter"`
	LastSeq    int64  `json:"lastSeq"`
}

type SearchHit struct {
	Anchor    SearchAnchor  `json:"anchor"`
	Title     string        `json:"title"`
	Workspace string        `json:"workspace"`
	Terminal  int           `json:"terminal"`
	Tool      string        `json:"tool"`
	Created   string        `json:"created"`
	Context   SearchContext `json:"context"`
}

type SearchPage struct {
	Matches []SearchHit `json:"matches"`
	More    bool        `json:"more"`
	Through int64       `json:"through"`
}

// Search reads SQLite on a separate connection. Bounded streaming matches can cross
// any number of output events in one run. They never cross runs or sessions.
func (s *Store) Search(ctx context.Context, filter SearchFilter) (page SearchPage, err error) {
	page.Matches = []SearchHit{}
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Scope == "" {
		filter.Scope = "session"
	}
	if !utf8.ValidString(filter.Query) || strings.TrimSpace(filter.Query) == "" || utf8.RuneCountInString(filter.Query) > 256 || strings.ContainsRune(filter.Query, 0) {
		return page, errors.New("search text must contain 1–256 characters without NUL")
	}
	if filter.Offset < 0 || filter.Offset > 100000 || filter.Limit < 1 || filter.Limit > 50 || filter.Through < 0 || (filter.Offset > 0 && filter.Through == 0) {
		return page, errors.New("invalid history search page")
	}
	condition, scope := "", ""
	switch filter.Scope {
	case "session":
		if filter.Session == "" {
			return page, errors.New("select a session for history search")
		}
		condition, scope = "s.id=?", filter.Session
	case "project":
		if filter.Project == "" {
			return page, errors.New("select a project for history search")
		}
		condition, scope = "s.project_id=?", filter.Project
	case "all":
		condition = "?=''"
	default:
		return page, errors.New("unknown history search scope")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := s.searchConnection(ctx)
	if err != nil {
		return page, err
	}
	defer conn.Close()
	if filter.Through == 0 {
		stmt, err := searchStatement(conn, "SELECT COALESCE(MAX(seq),0) FROM events")
		if err != nil {
			return page, err
		}
		if stmt.Step() {
			filter.Through = stmt.ColumnInt64(0)
		}
		err = stmt.Err()
		stmt.Close()
		if err != nil {
			return page, err
		}
	}
	page.Through = filter.Through
	stmt, err := searchStatement(conn, `SELECT e.seq,e.session_id,e.run_id,e.text,e.created
		FROM events e JOIN sessions s ON s.id=e.session_id
		JOIN runs r ON r.id=e.run_id AND r.session_id=e.session_id
		WHERE e.kind='output' AND e.seq<=? AND (`+condition+`) ORDER BY e.seq`, filter.Through, scope)
	if err != nil {
		return page, err
	}
	defer stmt.Close()
	needle := []rune(filter.Query)
	for i, r := range needle {
		needle[i] = searchFold(r, filter.MatchCase)
	}
	prefix := make([]int, len(needle))
	for i, j := 1, 0; i < len(needle); i++ {
		for j > 0 && needle[i] != needle[j] {
			j = prefix[j-1]
		}
		if needle[i] == needle[j] {
			j++
		}
		prefix[i] = j
	}
	type position struct {
		seq    int64
		offset int
	}
	type partial struct {
		matched, head int
		positions     []position
	}
	states := map[string]*partial{}
	bytes, events, found := 0, 0, 0
	for stmt.Step() {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		seq, sid, rid, text, created := stmt.ColumnInt64(0), stmt.ColumnText(1), stmt.ColumnText(2), stmt.ColumnText(3), stmt.ColumnText(4)
		bytes += len(text)
		events++
		if bytes > searchBytes || events > searchEvents {
			return page, errors.New("history search exceeded its scan limit; select a smaller scope")
		}
		key := sid + "/" + rid
		state := states[key]
		if state == nil {
			state = &partial{positions: make([]position, len(needle))}
		}
		for offset, r := range []rune(text) {
			r = searchFold(r, filter.MatchCase)
			state.positions[state.head] = position{seq, offset}
			state.head = (state.head + 1) % len(needle)
			for state.matched > 0 && r != needle[state.matched] {
				state.matched = prefix[state.matched-1]
			}
			if r == needle[state.matched] {
				state.matched++
			}
			if state.matched != len(needle) {
				continue
			}
			state.matched = prefix[len(needle)-1]
			found++
			if found <= filter.Offset {
				continue
			}
			if len(page.Matches) == filter.Limit {
				page.More = true
				return page, nil
			}
			start := state.positions[state.head]
			hit := SearchHit{Anchor: SearchAnchor{Session: sid, Run: rid, Seq: start.seq, Offset: start.offset, Length: len(needle), Through: page.Through}, Created: created}
			if err = searchMetadata(conn, &hit); err != nil {
				return page, err
			}
			hit.Context, err = readSearchContext(ctx, conn, ContextRequest{Anchor: hit.Anchor, Before: 120, After: 180})
			if err != nil {
				return page, err
			}
			page.Matches = append(page.Matches, hit)
		}
		if state.matched == 0 {
			delete(states, key)
		} else {
			states[key] = state
		}
		if len(states)*len(needle) > 262144 {
			return page, errors.New("too many partial history matches; select a smaller scope")
		}
	}
	return page, stmt.Err()
}

// Simple Unicode folding preserves character offsets. It does not expand characters.
func searchFold(r rune, matchCase bool) rune {
	if matchCase {
		return r
	}
	first := r
	for next := unicode.SimpleFold(r); next != first; next = unicode.SimpleFold(next) {
		if next < r {
			r = next
		}
	}
	return r
}

func (s *Store) SearchContext(ctx context.Context, request ContextRequest) (SearchContext, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := s.searchConnection(ctx)
	if err != nil {
		return SearchContext{}, err
	}
	defer conn.Close()
	return readSearchContext(ctx, conn, request)
}

func (s *Store) searchConnection(ctx context.Context) (*sqlite3.Conn, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(s.Path)}
	uri.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(0)", "busy_timeout(1000)"}}.Encode()
	conn, err := sqlite3.OpenContext(ctx, uri.String())
	if err != nil {
		return nil, err
	}
	conn.SetInterrupt(ctx)
	conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
	return conn, nil
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

func searchMetadata(conn *sqlite3.Conn, hit *SearchHit) error {
	stmt, err := searchStatement(conn, `SELECT s.title,s.workspace,
		(SELECT count(*) FROM runs prior WHERE prior.session_id=r.session_id AND prior.rowid<=r.rowid),
		(SELECT data FROM events WHERE session_id=r.session_id AND run_id=r.id AND kind='start' ORDER BY seq LIMIT 1)
		FROM runs r JOIN sessions s ON s.id=r.session_id WHERE r.id=? AND r.session_id=?`, hit.Anchor.Run, hit.Anchor.Session)
	if err != nil {
		return err
	}
	defer stmt.Close()
	if !stmt.Step() {
		return errors.Join(ErrNotFound, stmt.Err())
	}
	hit.Title, hit.Workspace, hit.Terminal = stmt.ColumnText(0), stmt.ColumnText(1), stmt.ColumnInt(2)
	var start struct {
		Tool string `json:"tool"`
	}
	json.Unmarshal(stmt.ColumnBlob(3, nil), &start)
	hit.Tool = start.Tool
	return nil
}

func readSearchContext(ctx context.Context, conn *sqlite3.Conn, request ContextRequest) (result SearchContext, err error) {
	a := request.Anchor
	if a.Session == "" || a.Run == "" || a.Seq <= 0 || a.Through < a.Seq || a.Offset < 0 || a.Length < 1 || a.Length > 256 || request.Before < 0 || request.Before > contextRunes || request.After < 0 || request.After > contextRunes {
		return result, errors.New("invalid history context range")
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
	before, err := searchStatement(conn, `SELECT seq,text FROM events WHERE session_id=? AND run_id=? AND kind='output' AND seq<=? ORDER BY seq DESC`, a.Session, a.Run, a.Seq)
	if err != nil {
		return result, err
	}
	var chunks []string
	remaining, events := request.Before+1, 0
	for before.Step() {
		if err = ctx.Err(); err != nil {
			break
		}
		events++
		if events > searchEvents {
			err = errors.New("history context exceeded its event limit")
			break
		}
		text := []rune(before.ColumnText(1))
		if before.ColumnInt64(0) == a.Seq {
			text = text[:a.Offset]
		}
		if len(text) > remaining {
			text = text[len(text)-remaining:]
		}
		chunks = append(chunks, string(text))
		remaining -= len(text)
		if remaining == 0 {
			break
		}
	}
	if err == nil {
		err = before.Err()
	}
	before.Close()
	if err != nil {
		return result, err
	}
	slices.Reverse(chunks)
	left := []rune(strings.Join(chunks, ""))
	if len(left) > request.Before {
		result.MoreBefore = true
		left = left[1:]
	}
	result.Before = string(left)
	after, err := searchStatement(conn, `SELECT seq,text FROM events WHERE session_id=? AND run_id=? AND kind='output' AND seq>=? AND seq<=? ORDER BY seq`, a.Session, a.Run, a.Seq, a.Through)
	if err != nil {
		return result, err
	}
	defer after.Close()
	var right []rune
	for after.Step() {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		events++
		if events > searchEvents {
			return result, errors.New("history context exceeded its event limit")
		}
		text := []rune(after.ColumnText(1))
		if after.ColumnInt64(0) == a.Seq {
			text = text[a.Offset:]
		}
		for _, r := range text {
			right = append(right, r)
			if len(right) == a.Length {
				result.LastSeq = after.ColumnInt64(0)
			}
			if len(right) == a.Length+request.After+1 {
				break
			}
		}
		if len(right) == a.Length+request.After+1 {
			break
		}
	}
	if err = after.Err(); err != nil {
		return result, err
	}
	if len(right) < a.Length {
		return result, errors.New("history context does not contain the complete match")
	}
	result.Match = string(right[:a.Length])
	right = right[a.Length:]
	if len(right) > request.After {
		result.MoreAfter = true
		right = right[:request.After]
	}
	result.After = string(right)
	return result, nil
}
