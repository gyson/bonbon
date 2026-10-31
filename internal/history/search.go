package history

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	searchBytes  = 32 << 20
	searchEvents = 100000
	contextRunes = 8192
	searchWindow = 4096
)

type SearchFilter struct {
	Query     string        `json:"query"`
	Scope     string        `json:"scope"`
	Session   string        `json:"session,omitempty"`
	Project   string        `json:"project,omitempty"`
	MatchCase bool          `json:"matchCase"`
	Limit     int           `json:"limit,omitempty"`
	Through   int64         `json:"through,omitempty"`
	Cursor    *SearchCursor `json:"cursor,omitempty"`
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

type SearchDisplay struct {
	Before string `json:"before"`
	Match  string `json:"match"`
	After  string `json:"after"`
}

type SearchContext struct {
	Before     string         `json:"before"`
	Match      string         `json:"match"`
	After      string         `json:"after"`
	MoreBefore bool           `json:"moreBefore"`
	MoreAfter  bool           `json:"moreAfter"`
	LastSeq    int64          `json:"lastSeq"`
	Display    *SearchDisplay `json:"display,omitempty"`
	positions  searchContextPositions
}

type SearchHit struct {
	Anchor    SearchAnchor  `json:"anchor"`
	Title     string        `json:"title"`
	Workspace string        `json:"workspace"`
	Terminal  int           `json:"terminal"`
	Tool      string        `json:"tool"`
	Created   string        `json:"created"`
	Context   SearchContext `json:"context"`
	last      int64
}

// SearchCursor points to the next character to scan. Offset counts Unicode characters.
type SearchCursor struct {
	Seq    int64 `json:"seq"`
	Offset int   `json:"offset"`
}

type SearchPage struct {
	Matches    []SearchHit   `json:"matches"`
	More       bool          `json:"more"`
	Through    int64         `json:"through"`
	Cursor     *SearchCursor `json:"cursor,omitempty"`
	Incomplete bool          `json:"incomplete"`
}

// Search uses a separate read-only connection. A cursor resumes at the next character.
// Bounded lookbehind restores matches that cross page and event boundaries.
func (s *Store) Search(parent context.Context, filter SearchFilter) (SearchPage, error) {
	page, err := s.search(parent, filter, 5*time.Second)
	if err == nil {
		s.searchSnippets(parent, &page)
	}
	if parent.Err() != nil {
		return page, parent.Err()
	}
	return page, err
}

func (s *Store) search(parent context.Context, filter SearchFilter, timeout time.Duration) (page SearchPage, err error) {
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
	if filter.Limit < 1 || filter.Limit > 50 || filter.Through < 0 {
		return page, errors.New("invalid history search page")
	}
	next := SearchCursor{}
	if filter.Cursor != nil {
		next = *filter.Cursor
		if filter.Through == 0 || next.Seq <= 0 || next.Seq > filter.Through+1 || next.Offset < 0 {
			return page, errors.New("invalid history search cursor")
		}
	}
	condition := ""
	args := []any{next.Seq, filter.Through}
	switch filter.Scope {
	case "session":
		if filter.Session == "" {
			return page, errors.New("select a session for history search")
		}
		condition = " AND s.id=?"
		args = append(args, filter.Session)
	case "project":
		if filter.Project == "" {
			return page, errors.New("select a project for history search")
		}
		condition = " AND s.project_id=?"
		args = append(args, filter.Project)
	case "all":
	default:
		return page, errors.New("unknown history search scope")
	}
	page.Through = filter.Through
	if next.Seq == 0 {
		next.Seq = 1
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// A scan deadline preserves completed results. Caller cancellation stops the request.
	defer func() {
		if parent.Err() != nil {
			err = parent.Err()
			return
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && page.Through > 0 {
			page.Incomplete, err = true, nil
			initial := SearchCursor{Seq: 1}
			if filter.Cursor != nil {
				initial = *filter.Cursor
			}
			page.More = next != initial
			if page.More {
				page.Cursor = &next
			}
		}
	}()
	conn, err := s.readConnection(ctx)
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
	reader := searchReader{conn: conn, through: page.Through}
	states := map[string]*searchMatcher{}
	bytes, events := 0, 0
	for next.Seq <= page.Through {
		end := min(page.Through, next.Seq+searchWindow-1)
		args[0], args[1] = next.Seq, end
		// Keep events first so project scope cannot sort an entire project before row one.
		stmt, err := searchStatement(conn, searchEventSQL+condition+` ORDER BY e.seq`, args...)
		if err != nil {
			return page, err
		}
		// Each range closes before the next range starts, including early returns.
		rangeErr := func() error {
			defer stmt.Close()
			for stmt.Step() {
				if err := ctx.Err(); err != nil {
					return err
				}
				seq, sid, rid := stmt.ColumnInt64(0), stmt.ColumnText(1), stmt.ColumnText(2)
				text := []rune(stmt.ColumnText(3))
				start := 0
				if seq == next.Seq {
					start = next.Offset
				}
				if start > len(text) {
					return errors.New("invalid history search cursor offset")
				}
				if start == len(text) {
					next = SearchCursor{Seq: seq + 1}
					continue
				}
				size := len(string(text[start:]))
				if bytes+size > searchBytes || events == searchEvents {
					page.More, page.Incomplete, page.Cursor = true, true, &next
					return errSearchPage
				}
				run, err := reader.run(sid, rid)
				if err != nil {
					return err
				}
				state := states[rid]
				if state == nil {
					// Rebuild evicted state from recorded lookbehind when its run returns.
					if (len(states)+1)*(len(needle)+121) > 262144 {
						clear(states)
					}
					state = &searchMatcher{positions: make([]searchPosition, len(needle)+121)}
					previous, err := reader.before(sid, rid, seq, start, len(state.positions), run.first)
					if err != nil {
						return err
					}
					for _, p := range previous {
						state.push(p, needle, prefix, filter.MatchCase)
					}
					states[rid] = state
				}
				bytes += size
				events++
				for offset := start; offset < len(text); offset++ {
					if offset%4096 == 0 && ctx.Err() != nil {
						return ctx.Err()
					}
					if state.push(searchPosition{seq, offset, text[offset]}, needle, prefix, filter.MatchCase) {
						if len(page.Matches) == filter.Limit {
							page.More, page.Cursor = true, &next
							return errSearchPage
						}
						positions := state.tail(len(needle) + 121)
						matchStart := len(positions) - len(needle)
						a := positions[matchStart]
						before := positions[:matchStart]
						context := SearchContext{Match: positionText(positions[matchStart:]), LastSeq: seq, MoreBefore: len(before) > 120 || (len(before) > 0 && before[0].seq > run.first)}
						if len(before) > 120 {
							before = before[len(before)-120:]
						}
						context.Before = positionText(before)
						context.positions.before = append([]searchPosition(nil), before...)
						context.positions.match = append([]searchPosition(nil), positions[matchStart:]...)
						after := text[offset+1:]
						context.MoreAfter = len(after) > 180 || seq < run.last
						if len(after) > 180 {
							after = after[:180]
						}
						context.After = string(after)
						for i, char := range after {
							context.positions.after = append(context.positions.after, searchPosition{seq, offset + 1 + i, char})
						}
						page.Matches = append(page.Matches, SearchHit{
							Anchor: SearchAnchor{Session: sid, Run: rid, Seq: a.seq, Offset: a.offset, Length: len(needle), Through: page.Through},
							Title:  run.title, Workspace: run.workspace, Terminal: run.number, Tool: run.tool,
							Created: stmt.ColumnText(4), Context: context, last: run.last,
						})
					}
					next = SearchCursor{Seq: seq, Offset: offset + 1}
				}
				next = SearchCursor{Seq: seq + 1}
			}
			return stmt.Err()
		}()
		if errors.Is(rangeErr, errSearchPage) {
			return page, nil
		}
		if rangeErr != nil {
			return page, rangeErr
		}
		// A completed empty range is progress too.
		next = SearchCursor{Seq: end + 1}
	}
	return page, nil
}

const searchEventSQL = `SELECT e.seq,e.session_id,e.run_id,e.text,e.created
            FROM events e CROSS JOIN sessions s CROSS JOIN runs r
            WHERE s.id=e.session_id AND r.id=e.run_id AND r.session_id=e.session_id
            AND e.kind='output' AND e.text<>'' AND e.seq>=? AND e.seq<=?`

var errSearchPage = errors.New("history search page complete")

type searchPosition struct {
	seq    int64
	offset int
	char   rune
}
type searchMatcher struct {
	matched, head, count int
	positions            []searchPosition
}

func (s *searchMatcher) push(p searchPosition, needle []rune, prefix []int, matchCase bool) bool {
	s.positions[s.head] = p
	s.head = (s.head + 1) % len(s.positions)
	s.count = min(s.count+1, len(s.positions))
	r := searchFold(p.char, matchCase)
	for s.matched > 0 && r != needle[s.matched] {
		s.matched = prefix[s.matched-1]
	}
	if r == needle[s.matched] {
		s.matched++
	}
	if s.matched != len(needle) {
		return false
	}
	s.matched = prefix[len(needle)-1]
	return true
}

func (s *searchMatcher) tail(count int) []searchPosition {
	count = min(count, s.count)
	result := make([]searchPosition, count)
	for i := range result {
		result[i] = s.positions[(s.head-count+i+len(s.positions))%len(s.positions)]
	}
	return result
}

func positionText(positions []searchPosition) string {
	text := make([]rune, len(positions))
	for i, p := range positions {
		text[i] = p.char
	}
	return string(text)
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
