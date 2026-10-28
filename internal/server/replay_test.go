package server

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	agent "bonbon/internal/runtime"
	"bonbon/internal/terminal"
	"github.com/gorilla/websocket"
)

// Each peer consumes its own acknowledged frame stream.
type testView struct {
	t    *testing.T
	peer *websocket.Conn
	done <-chan struct{}
}

func openTestView(t *testing.T, s *Server, r *runningSession, size protocol.Size) *testView {
	t.Helper()
	conn, peer := attachmentPair(t)
	done := make(chan struct{})
	go func() { defer close(done); s.attach(conn, r, size, nil) }()
	v := &testView{t, peer, done}
	t.Cleanup(func() { peer.Close(); <-done })
	v.read("session")
	return v
}
func (v *testView) send(m protocol.Message) {
	v.t.Helper()
	if err := v.peer.WriteJSON(m); err != nil {
		v.t.Fatal(err)
	}
}
func (v *testView) ack(m protocol.Message) {
	v.send(protocol.Message{Type: "frame-ack", Revision: m.Revision})
}
func (v *testView) read(kind string) protocol.Message {
	v.t.Helper()
	v.peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var m protocol.Message
		if err := v.peer.ReadJSON(&m); err != nil {
			v.t.Fatal(err)
		}
		if m.Type == kind {
			return m
		}
		if m.Type == "error" {
			v.t.Fatal(m.Error)
		}
		if m.Type == "frame" {
			v.ack(m)
		}
	}
}

func TestSharedViewsSizeOwnerAndIndependentFlow(t *testing.T) {
	size := protocol.Size{Cols: 80, Rows: 24}
	small := protocol.Size{Cols: 40, Rows: 10}
	r := &runningSession{id: "fixture", controls: make(chan agent.Control, 16), done: make(chan struct{}), terminal: terminal.New(size), revision: 1}
	s := &Server{}
	next := func() agent.Control {
		t.Helper()
		select {
		case control := <-r.controls:
			return control
		case <-time.After(5 * time.Second):
			t.Fatal("control missing")
			return agent.Control{}
		}
	}
	none := func(reason string) {
		t.Helper()
		select {
		case control := <-r.controls:
			t.Fatalf("%s: %+v", reason, control)
		default:
		}
	}
	owner := func() *attachment {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.sizeOwner
	}
	first := openTestView(t, s, r, size)
	firstFrame := first.read("frame") // Intentionally never acknowledge this slow view yet.
	if control := next(); control.Type != "resize" || control.Size != size {
		t.Fatalf("first view did not set the PTY size: %+v", control)
	}
	second := openTestView(t, s, r, small)
	initial := second.read("frame")
	if !initial.Full || initial.Size != small {
		t.Fatal(initial)
	}
	second.ack(initial)
	none("a new view changed the PTY size")
	for i := 0; i < 1000; i++ {
		if _, err := r.publish(history.Event{Kind: "output", Data: []byte(fmt.Sprintf("\x1b[24;1H\x1b[2KAnswer %d", i))}); err != nil {
			t.Fatal(err)
		}
	}
	for {
		m := second.read("frame")
		second.ack(m)
		if strings.Contains(string(m.Data), "Answer 999") {
			break
		}
	}
	// Input from another view moves the PTY size to that view before the input.
	second.send(protocol.Message{Type: "input", ID: "same-id", Data: []byte("typed")})
	if control := next(); control.Type != "resize" || control.Size != small {
		t.Fatalf("input did not move the PTY size first: %+v", control)
	}
	accepted := next()
	if accepted.Type != "input" || string(accepted.Data) != "typed" {
		t.Fatal(accepted)
	}
	// The receipt must reach its sender, even when another view uses the same input ID.
	// The first view must acknowledge its baseline before it sends input.
	first.send(protocol.Message{Type: "frame-ack", Revision: firstFrame.Revision})
	first.send(protocol.Message{Type: "input", ID: "same-id", Data: []byte("first")})
	if control := next(); control.Type != "resize" || control.Size != size {
		t.Fatalf("input did not return the PTY size: %+v", control)
	}
	returned := next()
	if returned.Type != "input" || string(returned.Data) != "first" {
		t.Fatal(returned)
	}
	accepted.Ack(nil)
	if m := second.read("input-ack"); m.ID != "same-id" || m.Error != "" {
		t.Fatal(m)
	}
	returned.Ack(nil)
	if m := first.read("input-ack"); m.ID != "same-id" || m.Error != "" {
		t.Fatal(m)
	}
	// Only the size owner resizes the PTY. Other views keep their own viewport.
	medium := protocol.Size{Cols: 50, Rows: 12}
	second.send(protocol.Message{Type: "resize", Size: medium})
	for {
		m := second.read("frame")
		second.ack(m)
		if m.Size == medium {
			break
		}
	}
	none("a view without the size resized the PTY")
	large := protocol.Size{Cols: 90, Rows: 30}
	first.send(protocol.Message{Type: "resize", Size: large})
	if control := next(); control.Type != "resize" || control.Size != large {
		t.Fatalf("owner resize missing: %+v", control)
	}
	// A signal reaches the process but does not move the PTY size.
	second.send(protocol.Message{Type: "signal", Signal: 2})
	if control := next(); control.Type != "signal" {
		t.Fatalf("signal changed the PTY size: %+v", control)
	}
	first.peer.Close()
	<-first.done
	r.mu.Lock()
	remaining, ended := len(r.clients), r.ended
	r.mu.Unlock()
	if remaining != 1 || owner() != nil || ended {
		t.Fatal("leaving ended work or selected another size owner")
	}
	// Without a size owner, the next view that sends its size takes it.
	second.send(protocol.Message{Type: "resize", Size: small})
	if control := next(); control.Type != "resize" || control.Size != small {
		t.Fatalf("remaining view did not take the PTY size: %+v", control)
	}
	r.finish(7, nil)
	for {
		m := second.read("frame")
		second.ack(m)
		r.mu.Lock()
		final := r.revision
		r.mu.Unlock()
		if m.Revision == final {
			break
		}
	}
	if m := second.read("exit"); m.Code != 7 {
		t.Fatal(m)
	}
}

func TestEndedScreenCacheAndArchiveReconstruction(t *testing.T) {
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession("fixture", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}
	r := &runningSession{id: session.ID, terminal: terminal.New(protocol.Size{Cols: 28, Rows: 8})}
	for _, event := range []history.Event{{Kind: "start", Data: []byte(`{"cols":28,"rows":8}`)}, {Kind: "output", Data: []byte("\x1b[2J\x1b[HWorking\r\x1b[2KAnswer: café 你好\r\n\x1b[6n")}, {Kind: "input", Data: []byte("NEVER REPLAY")}} {
		recorded, err := store.Append(session.ID, "run", event.Kind, event.Data, "")
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind != "input" {
			r.publish(recorded)
		}
	}
	for _, cached := range []bool{false, true} {
		if cached {
			if err := s.saveScreen(r); err != nil {
				t.Fatal(err)
			}
		}
		view, err := s.loadScreen(session.ID, "run")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := view.Render(nil)
		if !strings.Contains(string(data), "Answer: café 你好") || strings.Contains(string(data), "Working") || strings.Contains(string(data), "NEVER REPLAY") || strings.Contains(string(data), "[6n") {
			t.Fatalf("cached=%v: %q", cached, data)
		}
	}
}

// Each run has its own terminal. An interrupted later run must not show the saved
// screen of an earlier run, and replay must not mix the output of two runs.
func TestEachRunReplaysOnlyItsOwnTerminal(t *testing.T) {
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession("fixture", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}
	first := &runningSession{id: session.ID, terminal: terminal.New(protocol.Size{Cols: 30, Rows: 6})}
	for _, event := range []history.Event{{Kind: "start", Data: []byte(`{"cols":30,"rows":6}`)}, {Kind: "output", Data: []byte("FIRST TERMINAL\r\n")}} {
		recorded, err := store.Append(session.ID, "first", event.Kind, event.Data, "")
		if err != nil {
			t.Fatal(err)
		}
		first.publish(recorded)
	}
	if err = s.saveScreen(first); err != nil {
		t.Fatal(err)
	}
	// The second run has no saved screen, as after a server crash.
	for _, event := range []history.Event{{Kind: "start", Data: []byte(`{"cols":40,"rows":5}`)}, {Kind: "output", Data: []byte("SECOND TERMINAL\r\n")}} {
		if _, err := store.Append(session.ID, "second", event.Kind, event.Data, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, check := range []struct{ run, want, other string }{{"first", "FIRST TERMINAL", "SECOND"}, {"second", "SECOND TERMINAL", "FIRST"}} {
		view, err := s.loadScreen(session.ID, check.run)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := view.Render(nil)
		if !strings.Contains(string(data), check.want) || strings.Contains(string(data), check.other) {
			t.Fatalf("run %s: %q", check.run, data)
		}
	}
}
