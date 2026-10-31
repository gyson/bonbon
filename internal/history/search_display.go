package history

import (
	"bytes"
	"context"
	"time"

	"github.com/ncruces/go-sqlite3"
)

type searchContextPositions struct{ before, match, after []searchPosition }

type searchDisplayReader struct {
	conn  *sqlite3.Conn
	masks map[int64][]bool
}

// Snippet reads have their own small budget. They cannot discard a search page.
func (s *Store) searchSnippets(parent context.Context, page *SearchPage) {
	if len(page.Matches) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	conn, err := s.readConnection(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	display := searchDisplayReader{conn: conn}
	for i := range page.Matches {
		hit := &page.Matches[i]
		text := &hit.Context
		if len(text.positions.after) < 180 && text.MoreAfter {
			// The completion event already supplied all of its remaining characters.
			stmt, err := searchStatement(conn, `SELECT seq,text FROM events WHERE session_id=? AND run_id=?
                AND kind='output' AND text<>'' AND seq>? AND seq<=? ORDER BY seq`, hit.Anchor.Session, hit.Anchor.Run, text.LastSeq, hit.last)
			if err == nil {
				positions := append([]searchPosition(nil), text.positions.after...)
				for len(positions) < 181 && stmt.Step() {
					for offset, char := range []rune(stmt.ColumnText(1)) {
						positions = append(positions, searchPosition{stmt.ColumnInt64(0), offset, char})
						if len(positions) == 181 {
							break
						}
					}
				}
				if stmt.Err() == nil {
					text.MoreAfter = len(positions) > 180
					if len(positions) > 180 {
						positions = positions[:180]
					}
					text.positions.after = positions
					text.After = positionText(positions)
				}
				stmt.Close()
			}
		}
		display.normalize(hit.Anchor.Session, hit.Anchor.Run, text)
	}
}

// Preserve stored text and anchor offsets. Normalize only verified original CRLF pairs.
func (r *searchDisplayReader) normalize(sid, rid string, text *SearchContext) {
	if r.masks == nil {
		r.masks = map[int64][]bool{}
	}
	render := func(positions []searchPosition) string {
		var out []rune
		for _, p := range positions {
			mask, ok := r.masks[p.seq]
			if !ok {
				mask = r.mask(sid, rid, p.seq)
				r.masks[p.seq] = mask
			}
			if p.offset < len(mask) && mask[p.offset] {
				continue
			}
			out = append(out, p.char)
		}
		return string(out)
	}
	display := &SearchDisplay{Before: render(text.positions.before), Match: render(text.positions.match), After: render(text.positions.after)}
	if display.Before != text.Before || display.Match != text.Match || display.After != text.After {
		text.Display = display
	}
}

func (r *searchDisplayReader) mask(sid, rid string, seq int64) []bool {
	stmt, err := searchStatement(r.conn, "SELECT data,text FROM events WHERE seq=?", seq)
	if err != nil {
		return nil
	}
	if !stmt.Step() {
		stmt.Close()
		return nil
	}
	raw, stored := stmt.ColumnBlob(0, nil), stmt.ColumnText(1)
	stmt.Close()
	previousCR := false
	if len(raw) > 0 && raw[0] == '\n' {
		stmt, err = searchStatement(r.conn, `SELECT data FROM events WHERE session_id=? AND run_id=? AND kind='output' AND length(data)>0 AND seq<? ORDER BY seq DESC LIMIT 1`, sid, rid, seq)
		if err != nil {
			return nil
		}
		if stmt.Step() {
			data := stmt.ColumnBlob(0, nil)
			previousCR = len(data) > 0 && data[len(data)-1] == '\r'
		}
		stmt.Close()
	}
	if !previousCR && !bytes.Contains(raw, []byte("\r\n")) {
		return nil
	}
	parser := Text{previousCR: previousCR}
	derived, mask := parser.push(raw, true)
	// A chunk can start inside an escape or UTF-8 sequence. Keep its text if replay differs.
	if derived != stored {
		return nil
	}
	return mask
}
