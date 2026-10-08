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

// Each peer consumes its own role messages and acknowledged frame stream.
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

func TestSharedViewsControlAndIndependentFlow(t *testing.T) {
	size := protocol.Size{Cols: 80, Rows: 24}
	r := &runningSession{id: "fixture", controls: make(chan agent.Control, 16), done: make(chan struct{}), terminal: terminal.New(size), revision: 1}
	s := &Server{}
	first := openTestView(t, s, r, size)
	if !first.read("control").Controlling {
		t.Fatal("first view did not get control")
	}
	firstFrame := first.read("frame") // Intentionally never acknowledge this slow view.
	<-r.controls                      // Initial controller resize.
	second := openTestView(t, s, r, protocol.Size{Cols: 40, Rows: 10})
	if second.read("control").Controlling {
		t.Fatal("second view stole control")
	}
	initial := second.read("frame")
	if !initial.Full || initial.Size.Cols != 40 {
		t.Fatal(initial)
	}
	second.ack(initial)
	select {
	case m := <-r.controls:
		t.Fatalf("viewer resized PTY: %+v", m)
	default:
	}
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
	second.send(protocol.Message{Type: "input", ID: "same-id", Data: []byte("forbidden")})
	if second.read("input-ack").Error == "" {
		t.Fatal("viewer input accepted")
	}
	second.send(protocol.Message{Type: "take-control", Size: protocol.Size{Cols: 40, Rows: 10}})
	if !second.read("control").Controlling || first.read("control").Controlling {
		t.Fatal("handoff failed")
	}
	if control := <-r.controls; control.Type != "resize" || control.Size.Cols != 40 {
		t.Fatal(control)
	}
	second.send(protocol.Message{Type: "input", ID: "same-id", Data: []byte("accepted")})
	var accepted agent.Control
	select {
	case accepted = <-r.controls:
	case <-time.After(5 * time.Second):
		t.Fatal("controller input missing")
	}
	// Finish the accepted write after another view takes control. The receipt must reach
	// its sender, even when another view uses the same input ID. The first view must
	// acknowledge its baseline before it requests control.
	first.send(protocol.Message{Type: "frame-ack", Revision: firstFrame.Revision})
	first.send(protocol.Message{Type: "take-control", Size: size})
	if !first.read("control").Controlling || second.read("control").Controlling {
		t.Fatal("return control failed")
	}
	<-r.controls
	accepted.Ack(nil)
	if m := second.read("input-ack"); m.ID != "same-id" || m.Error != "" {
		t.Fatal(m)
	}
	second.send(protocol.Message{Type: "input", ID: "late", Data: []byte("rejected")})
	if second.read("input-ack").Error == "" {
		t.Fatal("old controller input accepted")
	}
	select {
	case m := <-r.controls:
		t.Fatalf("viewer input reached runtime: %+v", m)
	default:
	}
	first.peer.Close()
	<-first.done
	r.mu.Lock()
	remaining, owner, ended := len(r.clients), r.controller, r.ended
	r.mu.Unlock()
	if remaining != 1 || owner != nil || ended {
		t.Fatal("leaving ended work or selected another owner")
	}
	second.send(protocol.Message{Type: "take-control", Size: size})
	if !second.read("control").Controlling {
		t.Fatal("remaining viewer cannot take control")
	}
	<-r.controls
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
		view, err := s.loadScreen(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := view.Render(nil)
		if !strings.Contains(string(data), "Answer: café 你好") || strings.Contains(string(data), "Working") || strings.Contains(string(data), "NEVER REPLAY") || strings.Contains(string(data), "[6n") {
			t.Fatalf("cached=%v: %q", cached, data)
		}
	}
}
