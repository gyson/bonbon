package terminal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	xterm "github.com/gitpod-io/xterm-go"
)

func TestRenderTracksScreenAcrossRedrawResizeAndScroll(t *testing.T) {
	state := New(protocol.Size{Cols: 28, Rows: 8})
	display := xterm.New(xterm.WithCols(28), xterm.WithRows(8), xterm.WithScrollback(Scrollback))
	var previous *View
	apply := func(kind string, data []byte) {
		t.Helper()
		if _, err := state.Apply(history.Event{Kind: kind, Data: data}); err != nil {
			t.Fatal(err)
		}
		next := state.View()
		bytes, full := next.Render(previous)
		if full {
			display.Reset()
			display.Resize(int(next.Size.Cols), int(next.Size.Rows))
		}
		display.Write(bytes)
		if got, want := visibleText(display.String()), visibleText(state.term.String()); got != want {
			t.Fatalf("%s %q\ngot:\n%s\nwant:\n%s\nframe:%q", kind, data, got, want, bytes)
		}
		if display.CursorX() != next.CursorX || display.CursorY() != next.CursorY {
			t.Fatalf("cursor: %d,%d want %d,%d", display.CursorX(), display.CursorY(), next.CursorX, next.CursorY)
		}
		for y, line := range next.History {
			got := renderLine(display.NormalBuffer().Lines.Get(display.NormalBuffer().YBase-len(next.History)+y), display.Cols())
			if got != line {
				t.Fatalf("history row %d: %q != %q", y, got, line)
			}
		}
		previous = next
	}
	chunks := []string{"shell> agent\r\n", "\x1b[?1049h\x1b[2J\x1b[H", "Agent conversation\r\n\x1b[32mhello, world\x1b[0m\r\n", "Working 10%\rWorking 100%\x1b[K", "\x1b[2;1H\x1b[2K", "\x1b[1;3", "4mAnswer: ", "\xe4\xbd", "\xa0\xe5\xa5\xbd", "\x1b[0m\r\nA long response wraps cleanly across the terminal width.\r\n", "\x1b[41m\x1b[2K\x1b[0m", "\x1b[?1049l", "\r\nshell> \x1b[?2004h"}
	for _, chunk := range chunks {
		apply("output", []byte(chunk))
	}
	for _, cols := range []int{40, 12, 80} {
		apply("resize", []byte(fmt.Sprintf(`{"cols":%d,"rows":10}`, cols)))
		apply("output", []byte(strings.Repeat("X", cols)))
	}
	// Scroll far past the retained history, including skipped render opportunities.
	for i := 0; i < 8; i++ {
		apply("output", []byte(strings.Repeat(fmt.Sprintf("line %d: café 你好\r\n", i), 400)))
	}
	apply("output", []byte("\x1b[3J\x1b[2J\x1b[Hclear\r\n"))
	apply("output", []byte("\x1bcreset\r\n"))
}

func TestQueriesStayOnServerAndFramesContainOnlyPresentation(t *testing.T) {
	state := New(protocol.Size{Cols: 80, Rows: 24})
	responses, err := state.Apply(history.Event{Kind: "output", Data: []byte("hi\x1b[?u\x1b[6n\x1b[5n\x1b]52;c;c2VjcmV0\x07\x1b]0;title\x07")})
	if err != nil || !strings.Contains(string(responses), "\x1b[1;3R") {
		t.Fatalf("query reply: %q %v", responses, err)
	}
	if strings.Contains(string(responses), "u") {
		t.Fatal("advertised unsupported keyboard protocol")
	}
	data, _ := state.View().Render(nil)
	if strings.Contains(string(data), "]52") || strings.Contains(string(data), "title") || strings.Contains(string(data), "[6n") {
		t.Fatalf("unsafe frame: %q", data)
	}
}

func TestSizeLimits(t *testing.T) {
	state := New(protocol.Size{Cols: 80, Rows: 24})
	for _, size := range []protocol.Size{{}, {Cols: 65535, Rows: 24}, {Cols: 80, Rows: 65535}, {Cols: 1, Rows: 1}} {
		data, _ := json.Marshal(size)
		if _, err := state.Apply(history.Event{Kind: "resize", Data: data}); err == nil {
			t.Fatal("accepted size", size)
		}
	}
}

func visibleText(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// These synthetic frames are also checked by the real browser xterm.js engine.
// Keep both sides honest: Go regenerates them; JS compares rendering with the
// original PTY events, including split sequences and the alternate buffer.
func TestBrowserFrames(t *testing.T) {
	type step struct {
		Input string           `json:"input,omitempty"`
		Size  *protocol.Size   `json:"size,omitempty"`
		Frame protocol.Message `json:"frame"`
	}
	steps := []step{
		{Input: "shell> agent\r\n"},
		{Input: "\x1b[?1049h\x1b[H\x1b[2JAgent conversation\r\n"},
		{Input: "\x1b[32mHello world\x1b[0m\r\nWorking 10%\rWorking 100%\x1b[K"},
		{Input: "\x1b[2;1H\x1b[2K\x1b[1;3"},
		{Input: "4mAnswer: café 你好\x1b[0m"},
		{Input: "\x1b[4;1H\x1b[48;2;30;40;50m\x1b[2K\x1b[0m"},
		{Size: &protocol.Size{Cols: 40, Rows: 10}},
		{Input: "\x1b[6;1HFollow-up: typo corrected\x1b[K\r\n\x1b[?2004h"},
		{Input: "\x1b[?1049l"},
		{Input: strings.Repeat("readable scrollback\r\n", 30)},
		{Input: "shell> \x1b[?2004h\x1b[?1h\x1b[?1006h\x1b[?1000h\x1b[?25l"},
		{Size: &protocol.Size{Cols: 20, Rows: 6}},
		{Input: "\x1b[3J\x1b[2J\x1b[Hfinal screen\r\n"},
	}
	state := New(protocol.Size{Cols: 28, Rows: 8})
	var previous *View
	for i := range steps {
		event := history.Event{Kind: "output", Data: []byte(steps[i].Input)}
		if steps[i].Size != nil {
			event.Kind = "resize"
			event.Data, _ = json.Marshal(steps[i].Size)
		}
		if _, err := state.Apply(event); err != nil {
			t.Fatal(err)
		}
		next := state.View()
		data, full := next.Render(previous)
		steps[i].Frame = protocol.Message{Type: "frame", Data: data, Size: next.Size, Revision: int64(i + 1), Full: full}
		previous = next
	}
	data, err := json.MarshalIndent(steps, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "../../web/testdata/server-frames.json"
	if os.Getenv("BONBON_UPDATE_FIXTURES") == "1" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, saved) {
		t.Fatal("browser frame fixture is stale; run BONBON_UPDATE_FIXTURES=1 go test ./internal/terminal -run TestBrowserFrames")
	}
}

func TestViewerProjectionDoesNotResizeSharedTerminal(t *testing.T) {
	state := New(protocol.Size{Cols: 8, Rows: 5})
	state.Apply(history.Event{Kind: "output", Data: []byte("one\r\ntwo\r\nabcdef\r\nx你好\r\nlast")})
	original := state.term.String()
	display := xterm.New(xterm.WithCols(4), xterm.WithRows(3), xterm.WithScrollback(Scrollback))
	view := state.ViewFor(protocol.Size{Cols: 4, Rows: 3})
	data, _ := view.Render(nil)
	display.Write(data)
	if got := visibleText(display.String()); got != "abcd\nx你\nlast" {
		t.Fatalf("cropped display: %q", got)
	}
	if len(view.History) != 2 || !strings.Contains(view.History[0], "one") || !strings.Contains(view.History[1], "two") {
		t.Fatalf("clipped top rows: %q", view.History)
	}
	if state.term.String() != original || state.term.Cols() != 8 || state.term.Rows() != 5 {
		t.Fatal("viewer changed the shared terminal")
	}
	state.Apply(history.Event{Kind: "output", Data: []byte("\rnew\x1b[K")})
	next := state.ViewFor(view.Size)
	data, _ = next.Render(view)
	display.Write(data)
	if got := visibleText(display.String()); got != "abcd\nx你\nnew" {
		t.Fatalf("viewer delta: %q", got)
	}
	large := state.ViewFor(protocol.Size{Cols: 12, Rows: 8})
	if len(large.Lines) != 8 || !strings.Contains(large.Lines[2], "abcdef") || !strings.Contains(large.Lines[4], "new") {
		t.Fatalf("larger viewer: %+v", large)
	}
}
