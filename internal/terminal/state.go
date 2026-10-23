// Package terminal owns the headless terminal and renders ANSI for display only. Store
// original PTY bytes in the archive. Do not put them in the client render queue.
package terminal

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	xterm "github.com/gitpod-io/xterm-go"
)

// Scrollback is the maximum number of normal-buffer history rows in a view.
const Scrollback = 10000

const (
	// historyBudget limits the rendered history rows that a view keeps. Rows with many
	// styles can be large. The budget bounds memory, saved screens, and frames.
	historyBudget = 8 << 20
	// frameLimit keeps a base64 frame below the 16 MiB protocol message limit.
	frameLimit = 11 << 20
)

func ValidSize(size protocol.Size) bool {
	return size.Cols >= 2 && size.Cols <= 512 && size.Rows >= 1 && size.Rows <= 256
}

// State requires the owning session's lock. It generates responses even without a
// client. Replay callers discard these responses and write no input.
type State struct {
	term      *xterm.Terminal
	lines     *xterm.CircularList[*xterm.BufferLine]
	trim      int64
	epoch     int64
	responses []byte
	history   Cache
}

// Cache keeps rendered history rows for one width. Row numbers count from the start of
// the buffer. They stay stable when the emulator trims old rows. A new epoch or width
// discards the rows.
type Cache struct {
	cols  int
	epoch int64
	first int64
	rows  []string
	bytes int
}

func (c *Cache) end() int64 { return c.first + int64(len(c.rows)) }

func (c *Cache) reset(cols int, epoch, first int64) {
	*c = Cache{cols: cols, epoch: epoch, first: first}
}

func (c *Cache) dropTo(first int64) {
	for len(c.rows) > 0 && c.first < first {
		c.bytes -= rowCost(c.rows[0])
		c.rows[0] = "" // Views hold copies. Release the row now.
		c.rows = c.rows[1:]
		c.first++
	}
	c.first = max(c.first, first)
}

func (c *Cache) add(row string) {
	c.rows = append(c.rows, row)
	c.bytes += rowCost(row)
}

// rowCost includes the line break that a full frame writes after a history row.
func rowCost(row string) int { return len(row) + 2 }

func New(size protocol.Size) *State {
	s := &State{term: xterm.New(xterm.WithCols(int(size.Cols)), xterm.WithRows(int(size.Rows)), xterm.WithScrollback(Scrollback))}
	// The display clients do not encode Kitty extended keyboard events. Do not advertise
	// that optional protocol to applications in this terminal.
	s.term.RegisterCsiHandler(xterm.FunctionIdentifier{Prefix: '?', Final: 'u'}, func(*xterm.Params) bool { return true })
	s.term.OnData(func(data string) { s.responses = append(s.responses, data...) })
	s.trackBuffer()
	return s
}

func (s *State) trackBuffer() {
	lines := s.term.NormalBuffer().Lines
	if lines == s.lines {
		return
	}
	s.lines, s.trim, s.epoch = lines, 0, s.epoch+1
	lines.OnTrimEmitter.Event(func(n int) { s.trim += int64(n) })
}

func (s *State) Apply(event history.Event) ([]byte, error) {
	s.responses = nil
	switch event.Kind {
	case "start", "resize":
		var size protocol.Size
		if err := json.Unmarshal(event.Data, &size); err != nil || !ValidSize(size) {
			return nil, fmt.Errorf("unsupported terminal size at event %d (2–512 columns, 1–256 rows)", event.Seq)
		}
		if int(size.Cols) != s.term.Cols() || int(size.Rows) != s.term.Rows() {
			s.term.Resize(int(size.Cols), int(size.Rows))
			s.epoch++ // A resize can reflow history. Send a fresh view.
		}
	case "output":
		s.term.Write(event.Data)
	}
	s.trackBuffer()
	return s.responses, nil
}

// View contains derived display data. It excludes executable OSC sequences, terminal
// queries, clipboard operations, and native process state.
type View struct {
	Size    protocol.Size `json:"size"`
	History []string      `json:"history"`
	Lines   []string      `json:"lines"`
	Offset  int64         `json:"offset"`
	Epoch   int64         `json:"epoch"`
	CursorX int           `json:"cursorX"`
	CursorY int           `json:"cursorY"`
	Modes   string        `json:"modes"`
}

func (s *State) View() *View {
	v := s.screen()
	v.History = slices.Clone(s.history.rows)
	return v
}

// screen updates the history cache and returns a view without history rows.
func (s *State) screen() *View {
	t := s.term
	s.syncHistory()
	v := &View{Size: protocol.Size{Cols: uint16(t.Cols()), Rows: uint16(t.Rows())}, Offset: s.history.first, Epoch: s.epoch,
		CursorX: min(t.CursorX(), t.Cols()-1), CursorY: t.CursorY(), Modes: inputModes(t)}
	active := t.Buffer()
	for y := 0; y < t.Rows(); y++ {
		v.Lines = append(v.Lines, renderLine(active.Lines.Get(active.YBase+y), t.Cols()))
	}
	return v
}

// row renders a normal-buffer row. The row number counts from the start of the buffer.
func (s *State) row(y int64, cols int) string {
	return renderLine(s.term.NormalBuffer().Lines.Get(int(y-s.trim)), cols)
}

// syncHistory keeps the newest history rows that fit in historyBudget. It renders only
// new rows. The first kept row moves only forward until a reset or reflow.
func (s *State) syncHistory() {
	c, cols := &s.history, s.term.Cols()
	end := s.trim + int64(s.term.NormalBuffer().YBase)
	if c.cols != cols || c.epoch != s.epoch || c.end() > end {
		c.reset(cols, s.epoch, s.trim)
	}
	c.dropTo(s.trim)
	// Render from the newest row back. Stop when the new rows alone fill the budget.
	var added []string
	bytes := 0
	for y := end - 1; y >= c.end(); y-- {
		row := s.row(y, cols)
		if bytes+rowCost(row) > historyBudget {
			break
		}
		added = append(added, row)
		bytes += rowCost(row)
	}
	if c.end()+int64(len(added)) < end {
		c.reset(cols, s.epoch, end-int64(len(added)))
	}
	slices.Reverse(added)
	for _, row := range added {
		c.add(row)
	}
	for c.bytes > historyBudget {
		c.dropTo(c.first + 1)
	}
}

// ViewFor crops a smaller viewer without resizing or reflowing the shared PTY.
// Keep the bottom of the screen visible and put clipped top rows in scrollback.
// A narrower viewer keeps its history rows in cache, so each frame renders only new
// rows. A nil cache renders all rows again.
func (s *State) ViewFor(size protocol.Size, cache *Cache) *View {
	v := s.screen()
	history := s.history.rows
	if size.Cols < v.Size.Cols {
		if cache == nil {
			cache = &Cache{}
		}
		cols, end := int(size.Cols), s.history.end()
		if cache.cols != cols || cache.epoch != s.epoch || cache.first > v.Offset || cache.end() > end {
			cache.reset(cols, s.epoch, v.Offset)
		}
		cache.dropTo(v.Offset)
		for y := cache.end(); y < end; y++ {
			cache.add(s.row(y, cols))
		}
		history = cache.rows
	}
	v.History = slices.Clone(history)
	if v.Size == size {
		return v
	}
	t := s.term
	active := t.Buffer()
	top := max(0, t.Rows()-int(size.Rows))
	v.Lines = nil
	for y := 0; y < max(t.Rows(), int(size.Rows)); y++ {
		line := "\x1b[0m\x1b[K"
		if y < t.Rows() {
			line = renderLine(active.Lines.Get(active.YBase+y), int(size.Cols))
		}
		if y < top {
			v.History = append(v.History, line)
		} else {
			v.Lines = append(v.Lines, line)
		}
	}
	if excess := len(v.History) - Scrollback; excess > 0 {
		v.Offset += int64(excess)
		v.History = v.History[excess:]
	}
	if v.CursorX >= int(size.Cols) || v.CursorY < top {
		v.Modes += "\x1b[?25l"
	}
	v.CursorX = min(v.CursorX, int(size.Cols)-1)
	v.CursorY = max(0, v.CursorY-top)
	v.Size = size
	return v
}

func renderLine(line *xterm.BufferLine, cols int) string {
	if line == nil {
		return "\x1b[0m\x1b[K"
	}
	var out strings.Builder
	lastStyle := ""
	cell := xterm.NewCellData()
	previous := xterm.NewCellData()
	length := min(cols, line.GetNoBgTrimmedLength())
	for x := 0; x < length; x++ {
		line.LoadCell(x, cell)
		if cell.GetWidth() == 0 {
			continue
		}
		if x+cell.GetWidth() > cols {
			length = x
			break
		}
		if lastStyle == "" || !cell.AttributesEqual(previous) {
			lastStyle = cellStyle(cell)
			out.WriteString(lastStyle)
		}
		chars := cell.GetChars()
		if chars == "" {
			chars = " "
		}
		// Untrusted text must not add a control sequence to a rendered row.
		out.WriteString(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, chars))
		cell, previous = previous, cell
	}
	out.WriteString("\x1b[0m")
	if length < cols {
		out.WriteString("\x1b[K")
	}
	return out.String()
}

func cellStyle(c *xterm.CellData) string {
	codes := []string{"0"}
	for _, flag := range []struct {
		value uint32
		code  string
	}{
		{c.IsBold(), "1"}, {c.IsDim(), "2"}, {c.IsItalic(), "3"}, {c.IsBlink(), "5"},
		{c.IsInverse(), "7"}, {c.IsInvisible(), "8"}, {c.IsStrikethrough(), "9"}, {c.IsOverline(), "53"},
	} {
		if flag.value != 0 {
			codes = append(codes, flag.code)
		}
	}
	if c.IsUnderline() != 0 {
		codes = append(codes, fmt.Sprintf("4:%d", c.GetUnderlineStyle()))
	}
	color := func(prefix string, value int, rgb, palette bool) {
		if rgb {
			codes = append(codes, fmt.Sprintf("%s;2;%d;%d;%d", prefix, (value>>16)&255, (value>>8)&255, value&255))
		} else if palette {
			codes = append(codes, fmt.Sprintf("%s;5;%d", prefix, value))
		}
	}
	color("38", c.GetFgColor(), c.IsFgRGB(), c.IsFgPalette())
	color("48", c.GetBgColor(), c.IsBgRGB(), c.IsBgPalette())
	if c.IsUnderline() != 0 {
		color("58", c.GetUnderlineColor(), c.IsUnderlineColorRGB(), c.IsUnderlineColorPalette())
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func inputModes(t *xterm.Terminal) string {
	m := t.DecPrivateModes()
	var out strings.Builder
	for _, mode := range []struct {
		code int
		on   bool
	}{
		{1, m.ApplicationCursorKeys}, {66, m.ApplicationKeypad}, {2004, m.BracketedPasteMode}, {1004, m.SendFocus},
		{9, m.MouseTrackingMode == "X10"}, {1000, m.MouseTrackingMode == "VT200"},
		{1002, m.MouseTrackingMode == "DRAG"}, {1003, m.MouseTrackingMode == "ANY"},
		{1006, m.MouseEncoding == "SGR"}, {1016, m.MouseEncoding == "SGR_PIXELS"}, {25, !t.IsCursorHidden()},
	} {
		end := "l"
		if mode.on {
			end = "h"
		}
		fmt.Fprintf(&out, "\x1b[?%d%s", mode.code, end)
	}
	return out.String()
}

// Render compares the current view with the last acknowledged view. It generates
// history scrolling from rendered rows. It does not copy PTY bytes. A reset or reflow
// replaces the baseline.
func (v *View) Render(previous *View) (data []byte, full bool) {
	full = previous == nil || previous.Size != v.Size || previous.Epoch != v.Epoch
	var added []string
	if !full {
		oldEnd := previous.Offset + int64(len(previous.History))
		end := v.Offset + int64(len(v.History))
		if v.Offset < previous.Offset || v.Offset > oldEnd || end < oldEnd || (end == oldEnd && v.Offset != previous.Offset) {
			full = true
		} else {
			overlap := int(oldEnd - v.Offset)
			if !slices.Equal(previous.History[int(v.Offset-previous.Offset):], v.History[:overlap]) {
				full = true
			} else {
				added = v.History[overlap:]
			}
		}
	}
	var out strings.Builder
	// The client only displays frames. It uses no source scroll regions, origin mode, or
	// autowrap. Explicit cell positions work for normal and alternate screens.
	const prefix = "\x1b[?7l\x1b[?6l\x1b[4l\x1b[r\x1b[0m"
	out.WriteString(prefix)
	if !full {
		for _, line := range added {
			out.WriteString("\x1b[H")
			out.WriteString(line)
			fmt.Fprintf(&out, "\x1b[%d;1H\r\n", v.Size.Rows)
		}
		for y, line := range v.Lines {
			if len(added) > 0 || line != previous.Lines[y] {
				fmt.Fprintf(&out, "\x1b[%d;1H", y+1)
				out.WriteString(line)
			}
		}
		// A frame must fit in one protocol message. Send a full frame that fits instead.
		if out.Len() > frameLimit {
			full = true
			out.Reset()
			out.WriteString(prefix)
		}
	}
	if full {
		out.WriteString("\x1b[2J\x1b[3J\x1b[H")
		for _, line := range v.History[v.fullFrameStart():] {
			out.WriteString(line)
			out.WriteString("\r\n")
		}
		// Populate all screen rows to place every history row above the viewport.
		for i, line := range v.Lines {
			if i > 0 {
				out.WriteString("\r\n")
			}
			out.WriteString(line)
		}
	}
	out.WriteString(v.Modes)
	fmt.Fprintf(&out, "\x1b[%d;%dH", v.CursorY+1, v.CursorX+1)
	return []byte(out.String()), full
}

// fullFrameStart returns the first history row that a full frame sends. A full frame
// always contains the screen. It omits the oldest history rows that exceed frameLimit.
func (v *View) fullFrameStart() int {
	room := frameLimit - len(v.Modes) - 64
	for _, line := range v.Lines {
		room -= rowCost(line)
	}
	first := len(v.History)
	for first > 0 && room >= rowCost(v.History[first-1]) {
		first--
		room -= rowCost(v.History[first])
	}
	return first
}
