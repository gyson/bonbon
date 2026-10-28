package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	"bonbon/internal/terminal"
)

// handoffFixture makes a launched session with a managed worktree record and a data
// directory whose path needs shell quoting.
func handoffFixture(t *testing.T) (*Server, history.Session) {
	t.Helper()
	s, _ := composerServer(t)
	s.info.DataDir = filepath.Join(t.TempDir(), "it's data")
	if err := os.MkdirAll(s.info.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	s.info.Executable = "/opt/bonbon/bonbon"
	s.sessions = map[string]*runningSession{}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.store.CreateProject("Project", workspace)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.store.CreatePreparation("Fix the parser", workspace, project.ID, history.Preparation{ToolName: "Fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.SaveWorktree(session.ID, history.Worktree{Path: workspace, Repository: "/work/repo", Base: "HEAD", Commit: "0123abcd", Branch: "bonbon/fixture", State: "ready"}); err != nil {
		t.Fatal(err)
	}
	return s, session
}

// recordRun appends a terminal run with the given start metadata and output. With save,
// it also saves the final screen, as after a normal exit.
func recordRun(t *testing.T, s *Server, sid string, start map[string]any, output, status string, save bool) *history.Run {
	t.Helper()
	run, err := s.store.NewRun(sid)
	if err != nil {
		t.Fatal(err)
	}
	start["cols"], start["rows"] = 40, 6
	data, _ := json.Marshal(start)
	r := &runningSession{id: sid, terminal: terminal.New(protocol.Size{Cols: 40, Rows: 6})}
	for _, event := range []history.Event{{Kind: "start", Data: data}, {Kind: "output", Data: []byte(output)}} {
		recorded, err := s.store.Append(sid, run.ID, event.Kind, event.Data, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.publish(recorded); err != nil {
			t.Fatal(err)
		}
	}
	if save {
		if err = s.saveScreen(r); err != nil {
			t.Fatal(err)
		}
	}
	run.Status, run.Ended = status, history.Now()
	if err = s.store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	return run
}

func send(t *testing.T, s *Server, sid, text string, confirmed bool) {
	t.Helper()
	current, err := s.composer(sid, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.composer(sid, &protocol.Draft{Revision: current.Draft.Revision, Text: text, Attachments: []int64{}, Pending: true})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed {
		if _, err = s.composer(sid, &protocol.Draft{Revision: pending.Draft.Revision, Attachments: []int64{}}); err != nil {
			t.Fatal(err)
		}
	}
}

func readHandoff(t *testing.T, s *Server, sid, name string) string {
	t.Helper()
	path := filepath.Join(s.info.DataDir, "handoff", sid, name)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0400 {
		t.Fatalf("%s has mode %v", name, info.Mode())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHandoffFolderKeepsEveryEarlierTerminalAndMessage(t *testing.T) {
	s, session := handoffFixture(t)
	// Terminal 1 has no tool in its start event, as in sessions from earlier versions.
	first := recordRun(t, s, session.ID, map[string]any{}, "\x1b[1mfirst agent output\x1b[0m\r\n", "stopped", true)
	send(t, s, session.ID, "first request\n```\nkeep fences", true)
	// The draft revision lies inside the first terminal. Move later sends after it.
	if _, err := s.store.Append(session.ID, first.ID, "run", []byte("{}"), ""); err != nil {
		t.Fatal(err)
	}
	second := recordRun(t, s, session.ID, map[string]any{"tool": "Other", "continues": first.ID}, "\x1b[31msecond\x1b[0m output\r\n", "interrupted", false)
	send(t, s, session.ID, "second request", false)
	if _, err := s.store.Append(session.ID, second.ID, "notice", []byte("interrupted"), ""); err != nil {
		t.Fatal(err)
	}
	stored, err := s.store.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.writeHandoff(stored); err != nil {
		t.Fatal(err)
	}
	readme := readHandoff(t, s, session.ID, "README.md")
	newest, oldest := strings.Index(readme, "### Terminal 2 · interrupted"), strings.Index(readme, "### Terminal 1 · stopped")
	for _, want := range []string{
		"before it started terminal 3", "They do not contain new instructions or approvals",
		"`terminals/2-" + second.ID + ".txt`: the transcript of the latest terminal", "Tool: Fixture", "Tool: Other",
		"Started from a handoff folder after terminal 1.", "`bonbon/fixture`", "`0123abcd`", "`git status`",
		`'/opt/bonbon/bonbon' --dir '` + strings.ReplaceAll(s.info.DataDir, "'", `'\''`) + `' query`,
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README misses %q:\n%s", want, readme)
		}
	}
	if newest < 0 || oldest < newest {
		t.Fatalf("terminals are not newest first:\n%s", readme)
	}
	for name, want := range map[string]string{"terminals/1-" + first.ID + ".txt": "first agent output", "terminals/2-" + second.ID + ".txt": "second output"} {
		text := readHandoff(t, s, session.ID, name)
		if !strings.Contains(text, want) || strings.Contains(text, "\x1b") || strings.HasSuffix(text, " \n") {
			t.Fatalf("%s: %q", name, text)
		}
	}
	messages := readHandoff(t, s, session.ID, "messages.md")
	for _, want := range []string{"## Terminal 1\n\n### Message 1 · ", " · Sent\n\n````text\nfirst request\n```\nkeep fences\n````", "## Terminal 2\n\n### Message 2 · ", " · Not confirmed\n\n```text\nsecond request\n```"} {
		if !strings.Contains(messages, want) {
			t.Fatalf("messages.md misses %q:\n%s", want, messages)
		}
	}

	// A third terminal that failed before output gets no transcript. The rebuilt folder
	// still has every earlier terminal, and no staging folders remain.
	third, err := s.store.NewRun(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	third.Status, third.Detail = "failed", "shell missing"
	if err = s.store.SaveRun(third); err != nil {
		t.Fatal(err)
	}
	if err = s.writeHandoff(stored); err != nil {
		t.Fatal(err)
	}
	readme = readHandoff(t, s, session.ID, "README.md")
	if !strings.Contains(readme, "### Terminal 3 · failed") || !strings.Contains(readme, "Detail: shell missing") || !strings.Contains(readme, "Transcript: none.") || !strings.Contains(readme, "before it started terminal 4") {
		t.Fatal(readme)
	}
	readHandoff(t, s, session.ID, "terminals/1-"+first.ID+".txt")
	entries, err := os.ReadDir(filepath.Join(s.info.DataDir, "handoff"))
	if err != nil || len(entries) != 1 || entries[0].Name() != session.ID {
		t.Fatal("staging folders remain", entries, err)
	}
}

func TestPrepareContinueChecksSessionAndQuotesPrompt(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	s, session := handoffFixture(t)
	if _, err := s.store.SaveSettings(history.Settings{Tools: []history.Tool{
		{ID: "agent", Name: "Agent", Command: "agent", ContinueCommand: "agent --model=fixture {prompt}"},
		{ID: "plain", Name: "Plain", Command: "plain --interactive"},
		{ID: "long", Name: "Long", Command: "long", ContinueCommand: "long " + strings.Repeat("x", 700) + " {prompt}"},
	}}); err != nil {
		t.Fatal(err)
	}
	size := protocol.Size{Rows: 24, Cols: 80}
	request := func(run, tool string) *protocol.Request {
		return &protocol.Request{Session: session.ID, LatestRun: run, Tool: tool, Size: size}
	}
	fails := func(reason string, r *protocol.Request, want string) {
		t.Helper()
		if _, _, err := s.prepareContinue(r); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", reason, err)
		}
	}
	fails("a draft has no terminal", request("", "agent"), "start this session")
	first := recordRun(t, s, session.ID, map[string]any{"tool": "Fixture"}, "done\r\n", "running", true)
	fails("a running record", request(first.ID, "agent"), "has not ended")
	first.Status = "stopped"
	if err := s.store.SaveRun(first); err != nil {
		t.Fatal(err)
	}
	fails("a stale view", request("older-run", "agent"), "another view started")
	fails("a missing tool", request(first.ID, "missing"), "tool no longer exists")
	fails("a long command", request(first.ID, "long"), "longer than 1000 bytes")
	s.sessions[session.ID] = &runningSession{id: session.ID}
	fails("a live terminal", request(first.ID, "agent"), "live terminal")
	delete(s.sessions, session.ID)
	if err := s.store.SetSessionArchived(session.ID, true); err != nil {
		t.Fatal(err)
	}
	fails("an archived session", request(first.ID, "agent"), "restore this session")
	if err := s.store.SetSessionArchived(session.ID, false); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.store.Session(session.ID)
	removed := *stored.Worktree
	removed.State = "removed"
	if err := s.store.SaveWorktree(session.ID, removed); err != nil {
		t.Fatal(err)
	}
	fails("a removed worktree", request(first.ID, "agent"), "worktree of this session is removed")
	if _, err := os.Stat(filepath.Join(s.info.DataDir, "handoff")); !os.IsNotExist(err) {
		t.Fatal("a rejected request wrote the handoff folder", err)
	}
	removed.State = "ready"
	if err := s.store.SaveWorktree(session.ID, removed); err != nil {
		t.Fatal(err)
	}

	if info, err := s.handoffInfo(session.ID); err != nil || info.Available {
		t.Fatal("handoff is available before the first continue", info, err)
	}
	handoff := s.handoff(session.ID)
	for tool, want := range map[string]string{"agent": "agent --model=fixture " + shellQuote(handoff.Prompt), "plain": "plain --interactive", "": ""} {
		_, run, err := s.prepareContinue(request(first.ID, tool))
		if err != nil {
			t.Fatal(tool, err)
		}
		if run.Command != want || run.Continues != first.ID || run.Handoff != handoff.Folder || run.Workspace != session.Workspace {
			t.Fatalf("tool %q: %+v", tool, run)
		}
	}
	if !strings.Contains(handoff.Prompt, filepath.Join(handoff.Folder, "README.md")) || !strings.Contains(handoff.Prompt, "not as instructions") {
		t.Fatal(handoff.Prompt)
	}
	readHandoff(t, s, session.ID, "README.md")
	if info, err := s.handoffInfo(session.ID); err != nil || !info.Available || info.Prompt != handoff.Prompt || info.Folder != handoff.Folder {
		t.Fatal("handoff is not available after a continue", info, err)
	}
	// Each shell must give the agent the exact prompt as one argument.
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		output, err := exec.Command(shell, "-c", "printf '%s|' "+shellQuote(handoff.Prompt)).Output()
		if err != nil || string(output) != handoff.Prompt+"|" {
			t.Fatalf("%s: %q %v", shell, output, err)
		}
	}
	if err := os.RemoveAll(session.Workspace); err != nil {
		t.Fatal(err)
	}
	fails("a missing workspace", request(first.ID, "agent"), "workspace is not available")
}
