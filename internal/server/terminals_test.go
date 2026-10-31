package server

import (
	"strings"
	"testing"
	"time"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	agent "bonbon/internal/runtime"
	"bonbon/internal/terminal"
)

func TestTerminalsListEachTerminalWithItsTool(t *testing.T) {
	s, session := handoffFixture(t)
	if terminals, err := s.terminals(session.ID); err != nil || len(terminals) != 0 {
		t.Fatal(terminals, err)
	}
	// BonBon 0.0.4 and earlier wrote no tool. The first run then uses the preparation.
	first := recordRun(t, s, session.ID, map[string]any{}, "first\r\n", "exited", true)
	second := recordRun(t, s, session.ID, map[string]any{"tool": "Agent", "continues": first.ID}, "second\r\n", "stopped", true)
	want := []protocol.Terminal{
		{Number: 1, Run: first.ID, Tool: "Fixture", Status: "exited"},
		{Number: 2, Run: second.ID, Tool: "Agent", Status: "stopped"},
	}
	check := func(reason string) {
		t.Helper()
		terminals, err := s.terminals(session.ID)
		if err != nil || len(terminals) != len(want) {
			t.Fatalf("%s: %+v %v", reason, terminals, err)
		}
		for i, terminal := range terminals {
			if terminal.Number != want[i].Number || terminal.Run != want[i].Run || terminal.Tool != want[i].Tool || terminal.Status != want[i].Status {
				t.Fatalf("%s: terminal %d is %+v, expected %+v", reason, i, terminal, want[i])
			}
			if terminal.Run != "" && (terminal.Started == "" || terminal.Ended == "") {
				t.Fatalf("%s: terminal %d has no times: %+v", reason, i, terminal)
			}
		}
	}
	check("two ended terminals")
	// A new terminal has no run record until its process starts.
	s.sessions[session.ID] = &runningSession{id: session.ID, tool: "Codex"}
	want = append(want, protocol.Terminal{Number: 3, Tool: "Codex", Status: "starting"})
	check("a starting terminal")
	third, err := s.store.NewRun(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Append(session.ID, third.ID, "start", []byte(`{"tool":"Codex"}`), ""); err != nil {
		t.Fatal(err)
	}
	third.Status, third.Ended = "running", history.Now()
	if err = s.store.SaveRun(third); err != nil {
		t.Fatal(err)
	}
	want[2] = protocol.Terminal{Number: 3, Run: third.ID, Tool: "Codex", Status: "running"}
	check("a running terminal")
	if _, err := s.terminals("missing"); err == nil {
		t.Fatal("an unknown session has terminals")
	}
}

// A view of an earlier terminal shows its saved screen and never joins the live process.
func TestResumeShowsAnEarlierTerminalWhileTheLatestRuns(t *testing.T) {
	s, session := handoffFixture(t)
	first := recordRun(t, s, session.ID, map[string]any{"tool": "Fixture"}, "FIRST TERMINAL\r\n", "exited", true)
	second := recordRun(t, s, session.ID, map[string]any{"tool": "Agent"}, "SECOND TERMINAL\r\n", "running", false)
	size := protocol.Size{Cols: 40, Rows: 6}
	live := &runningSession{id: session.ID, controls: make(chan agent.Control, 16), done: make(chan struct{}), terminal: terminal.New(size), revision: 1, runID: second.ID}
	s.sessions[session.ID] = live
	other, err := s.store.CreateSession("Other", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	foreign := recordRun(t, s, other.ID, map[string]any{"tool": "Other"}, "OTHER SESSION\r\n", "exited", true)

	resume := func(run string) []protocol.Message {
		t.Helper()
		conn, peer := attachmentPair(t)
		done := make(chan struct{})
		go func() { defer close(done); s.resumeSession(conn, session.ID, run, size) }()
		t.Cleanup(func() { peer.Close(); <-done })
		var messages []protocol.Message
		peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			var m protocol.Message
			if err := peer.ReadJSON(&m); err != nil {
				t.Fatalf("run %q: %v after %+v", run, err, messages)
			}
			messages = append(messages, m)
			switch m.Type {
			case "frame":
				if err := peer.WriteJSON(protocol.Message{Type: "frame-ack", Revision: m.Revision}); err != nil {
					t.Fatal(err)
				}
				if live.run() == run || run == "" {
					// A live view stays open. One frame is enough.
					return messages
				}
			case "history-end", "error":
				return messages
			}
		}
	}
	frameText := func(messages []protocol.Message) string {
		for _, m := range messages {
			if m.Type == "frame" {
				return string(m.Data)
			}
		}
		return ""
	}

	earlier := resume(first.ID)
	if earlier[0].Type != "session" || earlier[0].Active || earlier[0].Status != "exited" || earlier[len(earlier)-1].Type != "history-end" {
		t.Fatalf("earlier terminal: %+v", earlier)
	}
	if text := frameText(earlier); !strings.Contains(text, "FIRST TERMINAL") || strings.Contains(text, "SECOND") {
		t.Fatalf("earlier terminal shows %q", text)
	}
	live.mu.Lock()
	views := len(live.clients)
	live.mu.Unlock()
	if views != 0 {
		t.Fatalf("the earlier terminal view joined the live terminal: %d views", views)
	}
	for _, run := range []string{"", second.ID} {
		latest := resume(run)
		if latest[0].Type != "session" || !latest[0].Active {
			t.Fatalf("run %q does not join the live terminal: %+v", run, latest)
		}
	}
	for _, run := range []string{foreign.ID, "missing"} {
		rejected := resume(run)
		if last := rejected[len(rejected)-1]; last.Type != "error" || last.Error != history.ErrRunNotFound.Error() || frameText(rejected) != "" {
			t.Fatalf("run %q: %+v", run, rejected)
		}
	}
	// After the live process ends, a view without a run shows the latest saved screen.
	delete(s.sessions, session.ID)
	if text := frameText(resume("")); !strings.Contains(text, "SECOND TERMINAL") {
		t.Fatalf("latest terminal shows %q", text)
	}
}
