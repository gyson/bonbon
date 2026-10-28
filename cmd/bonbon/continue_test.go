package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

// A worktree session stops, continues in a new terminal, continues again after a server
// restart, and keeps one handoff folder with every earlier terminal.
func TestContinueWorktreeSessionInNewTerminal(t *testing.T) {
	directory, repo := t.TempDir(), t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}, {"commit", "--allow-empty", "-m", "fixture"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, output, err)
		}
	}
	startTestServer(t, directory)
	target := client.Target{Dir: directory}
	rpc := func(request protocol.Request, result any) {
		t.Helper()
		data, err := target.Call(request)
		if err != nil {
			t.Fatal(request.Operation, err)
		}
		if result != nil {
			if err = json.Unmarshal(data, result); err != nil {
				t.Fatal(err)
			}
		}
	}
	size := protocol.Size{Rows: 24, Cols: 80}
	rpc(protocol.Request{Operation: "settings-save", Settings: &history.Settings{DefaultTool: "fixture", Worktree: true, Base: "HEAD", Tools: []history.Tool{
		{ID: "fixture", Name: "Fixture", Command: "printf 'FIRST_%s\\n' run", ContinueCommand: "printf 'PROMPT<%s>\\n' {prompt}"},
	}}}, nil)
	var project history.Project
	rpc(protocol.Request{Operation: "project-add", Workspace: repo}, &project)
	var session history.Session
	rpc(protocol.Request{Operation: "project-draft", Project: project.ID}, &session)
	if !session.Preparation.Worktree {
		t.Fatal("worktree default not applied", session.Preparation)
	}
	first := openSessionView(t, protocol.Request{Operation: "session-start", Session: session.ID, Revision: session.Preparation.Revision, Size: size})
	waitFor(t, func() bool { return strings.Contains(first.text(), "FIRST_run") })
	// Send one message as the editor does: save it as pending, write it, then clear it.
	var draft protocol.Composer
	rpc(protocol.Request{Operation: "composer-draft", Session: session.ID, Draft: &protocol.Draft{Text: "echo first request", Attachments: []int64{}, Pending: true}}, &draft)
	if _, err := first.Write([]byte("echo first request\r")); err != nil {
		t.Fatal(err)
	}
	rpc(protocol.Request{Operation: "composer-draft", Session: session.ID, Draft: &protocol.Draft{Revision: draft.Draft.Revision, Attachments: []int64{}}}, nil)
	waitFor(t, func() bool { return strings.Count(first.text(), "first request") >= 2 })
	stopTestSession(t, session.ID)
	first.wait(t, 129)

	latest := func() *history.Run {
		t.Helper()
		var current history.Session
		rpc(protocol.Request{Operation: "session-config", Session: session.ID}, &current)
		if current.Run == nil {
			t.Fatal("session has no run")
		}
		return current.Run
	}
	firstRun := latest()
	if firstRun.Status != "stopped" {
		t.Fatal(firstRun)
	}
	var handoff protocol.Handoff
	rpc(protocol.Request{Operation: "session-handoff", Session: session.ID}, &handoff)
	if handoff.Available {
		t.Fatal("handoff is available before the first continue", handoff)
	}
	second := openSessionView(t, protocol.Request{Operation: "session-continue", Session: session.ID, LatestRun: firstRun.ID, Tool: "fixture", Size: size})
	waitFor(t, func() bool {
		return strings.Contains(second.text(), "PROMPT<Continue the work of BonBon session "+session.ID)
	})
	// A second view that saw the same latest run cannot start another terminal.
	duplicate := openSessionView(t, protocol.Request{Operation: "session-continue", Session: session.ID, LatestRun: firstRun.ID, Tool: "fixture", Size: size})
	if message := <-duplicate.done; message.Type != "error" || !strings.Contains(message.Error, "live terminal") {
		t.Fatal("started a second terminal", message)
	}
	readme := func() string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(handoff.Folder, "README.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if text := readme(); !strings.Contains(text, "### Terminal 1 · stopped") || strings.Contains(text, "Terminal 2") {
		t.Fatal(text)
	}
	// A view can show the same prompt again while the new terminal runs.
	var later protocol.Handoff
	rpc(protocol.Request{Operation: "session-handoff", Session: session.ID}, &later)
	if !later.Available || later.Prompt != handoff.Prompt || later.Folder != handoff.Folder {
		t.Fatal("handoff prompt is not available later", later)
	}
	transcript, err := os.ReadFile(filepath.Join(handoff.Folder, "terminals", "1-"+firstRun.ID+".txt"))
	if err != nil || !strings.Contains(string(transcript), "FIRST_run") {
		t.Fatal(string(transcript), err)
	}
	messages, err := os.ReadFile(filepath.Join(handoff.Folder, "messages.md"))
	if err != nil || !strings.Contains(string(messages), "## Terminal 1") || !strings.Contains(string(messages), "Sent\n\n```text\necho first request\n```") {
		t.Fatal(string(messages), err)
	}
	// The new terminal runs in the same worktree. Compare in the shell, because a long
	// path wraps in the terminal.
	var stored history.Session
	rpc(protocol.Request{Operation: "session-config", Session: session.ID}, &stored)
	if _, err = second.Write([]byte(fmt.Sprintf("[ \"$(pwd -P)\" = '%s' ] && printf 'IN_%%s\\n' WORKTREE\r", stored.Worktree.Path))); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(second.text(), "IN_WORKTREE") })

	// A server restart stops the second terminal. Continue again with Shell.
	if output, err := testCommand("server", "restart").CombinedOutput(); err != nil {
		t.Fatalf("restart: %s %v", output, err)
	}
	secondRun := latest()
	if secondRun.ID == firstRun.ID || secondRun.Status != "stopped" {
		t.Fatal(secondRun)
	}
	third := openSessionView(t, protocol.Request{Operation: "session-continue", Session: session.ID, LatestRun: secondRun.ID, Size: size})
	waitFor(t, func() bool { return strings.Contains(third.text(), "BONBON_SHELL_PROMPT> ") })
	text := readme()
	two, one := strings.Index(text, "### Terminal 2 · stopped"), strings.Index(text, "### Terminal 1 · stopped")
	if two < 0 || one < two || !strings.Contains(text, "Started from a handoff folder after terminal 1.") || !strings.Contains(text, "Tool: Fixture") {
		t.Fatal(text)
	}
	transcript, err = os.ReadFile(filepath.Join(handoff.Folder, "terminals", "2-"+secondRun.ID+".txt"))
	if err != nil || !strings.Contains(string(transcript), "PROMPT<Continue the work") {
		t.Fatal(string(transcript), err)
	}
	stopTestSession(t, session.ID)
	third.wait(t, 129)
	// The ended view shows only the latest terminal.
	ended := resumeView(t, session.ID)
	if message := <-ended.done; message.Type != "history-end" || strings.Contains(ended.text(), "PROMPT<") || strings.Contains(ended.text(), "FIRST_run") {
		t.Fatalf("%+v %q", message, ended.text())
	}
	// Each startup command was recorded once, in its own run. No input was replayed.
	var query history.QueryResult
	rpc(protocol.Request{Operation: "query", SQL: fmt.Sprintf("SELECT run_id,count(*) FROM events WHERE session_id='%s' AND kind='input' AND (CAST(data AS TEXT) LIKE '%%FIRST_%%' OR CAST(data AS TEXT) LIKE '%%first request%%') GROUP BY run_id", session.ID)}, &query)
	if len(query.Rows) != 1 || query.Rows[0][0] != firstRun.ID || query.Rows[0][1] != float64(2) {
		t.Fatal("input replayed", query.Rows)
	}
	rpc(protocol.Request{Operation: "query", SQL: fmt.Sprintf("SELECT count(*) FROM runs WHERE session_id='%s'", session.ID)}, &query)
	if query.Rows[0][0] != float64(3) {
		t.Fatal("wrong run count", query.Rows)
	}
}
