package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/protocol"
	agent "bonbon/internal/runtime"
	"github.com/creack/pty"
	"golang.org/x/term"
)

func fixtureAgent() {
	switch os.Args[2] {
	case "echo":
		term.MakeRaw(0)
		fmt.Printf("ARGS=%q\nCWD=%s\nREADY\n", os.Args[3:], mustCWD())
		size, _ := strconv.Atoi(os.Args[3])
		data := make([]byte, size)
		if _, err := io.ReadFull(os.Stdin, data); err != nil {
			os.Exit(9)
		}
		window, _ := pty.GetsizeFull(os.Stdin)
		fmt.Printf("SIZE=%dx%d\nBYTES=%x\n\x1b[32mrecorded café\x1b[0m\n", window.Rows, window.Cols, data)
		os.Exit(7)
	case "interrupt":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		fmt.Println("READY")
		<-signals
		fmt.Println("INTERRUPTED")
		os.Exit(130)
	case "stream":
		term.MakeRaw(0)
		// Raw output needs CRLF. LF alone drifts right on every tick and can
		// split marker words across screen rows during slower race-enabled runs.
		fmt.Print("READY\r\n")
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					fmt.Print("TICK\r\n")
				case <-done:
					return
				}
			}
		}()
		var b [1]byte
		for {
			if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
				os.Exit(9)
			}
			if b[0] == 'q' {
				close(done)
				fmt.Print("FINISHED\r\n")
				os.Exit(19)
			}
			fmt.Printf("INPUT=%x\r\n", b[0])
		}
	case "wait":
		signal.Ignore(syscall.SIGHUP, syscall.SIGTERM)
		fmt.Printf("JOB_PID=%d\nREADY\n", os.Getpid())
		for {
			time.Sleep(time.Second)
		}
	case "activity":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGWINCH)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		fmt.Println("READY")
		emitted := false
		for {
			select {
			case <-signals:
				size, _ := pty.GetsizeFull(os.Stdin)
				fmt.Printf("RESIZED=%dx%d\n", size.Rows, size.Cols)
			case <-ticker.C:
				if _, err := os.Stat(os.Args[3]); err == nil && !emitted {
					fmt.Println("BACKGROUND_ACTIVITY")
					emitted = true
				}
			}
		}
	case "scope":
		fmt.Println("READY")
		cmd := exec.Command(os.Args[0], "query", "SELECT session_id FROM events WHERE session_id='"+os.Getenv("BONBON_SESSION")+"'")
		output, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Print(string(output))
			os.Exit(8)
		}
		var events history.QueryResult
		if json.Unmarshal(output, &events) != nil || len(events.Rows) == 0 {
			os.Exit(9)
		}
		for _, row := range events.Rows {
			if row[0] != os.Getenv("BONBON_SESSION") {
				os.Exit(10)
			}
		}
		fmt.Println("SCOPED_HISTORY_OK")
	}
}

func mustCWD() string { cwd, _ := os.Getwd(); return cwd }

// A synthetic browser view exercises the public session protocol without a
// terminal client. Only the server owns a PTY.
type sessionView struct {
	conn        *protocol.Conn
	done        chan protocol.Message
	mu          sync.Mutex
	output      bytes.Buffer
	launchInput []byte
}

func openSessionView(t *testing.T, request protocol.Request) *sessionView {
	t.Helper()
	conn, err := (client.Target{Dir: os.Getenv("BONBON_DIR")}).Connect(request)
	if err != nil {
		t.Fatal(err)
	}
	c := &sessionView{conn: conn, done: make(chan protocol.Message, 1)}
	t.Cleanup(func() { c.Close() })
	go func() {
		for {
			m, err := conn.Receive()
			if err != nil {
				c.done <- protocol.Message{Type: "error", Error: err.Error()}
				return
			}
			if m.Type == "frame" {
				c.mu.Lock()
				c.output.Write(m.Data)
				c.mu.Unlock()
				if err := conn.Send(protocol.Message{Type: "frame-ack", Revision: m.Revision}); err != nil {
					c.done <- protocol.Message{Type: "error", Error: err.Error()}
					return
				}
			}
			switch m.Type {
			case "exit", "history-end", "error":
				c.done <- m
				return
			}
		}
	}()
	return c
}

func newShell(t *testing.T, workspace, title string) *sessionView {
	t.Helper()
	c := openSessionView(t, shellStartRequest(t, workspace, title, protocol.Size{Rows: 24, Cols: 80}))
	waitFor(t, func() bool { return strings.Contains(c.text(), "BONBON_SHELL_PROMPT> ") })
	return c
}

func resumeView(t *testing.T, id string) *sessionView {
	t.Helper()
	return openSessionView(t, protocol.Request{Operation: "session-resume", Session: id, Size: protocol.Size{Rows: 24, Cols: 80}})
}

// Type a fixture command into an ordinary interactive shell, just as in the UI.
func launchAgent(t *testing.T, workspace string, args ...string) *sessionView {
	t.Helper()
	c := newShell(t, workspace, "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c.launchInput = fixtureInput(executable, args...)
	if _, err = c.Write(c.launchInput); err != nil {
		t.Fatal(err)
	}
	return c
}

func (c *sessionView) Write(data []byte) (int, error) {
	err := c.conn.Send(protocol.Message{Type: "input", Data: data})
	if err != nil {
		return 0, err
	}
	return len(data), nil
}
func (c *sessionView) Close() error { return c.conn.Close() }
func (c *sessionView) text() string { c.mu.Lock(); defer c.mu.Unlock(); return c.output.String() }

func fixtureInput(executable string, args ...string) []byte {
	command := []string{executable, "--fixture-agent"}
	command = append(command, args...)
	for i, arg := range command {
		command[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return []byte("exec " + strings.Join(command, " ") + "\r")
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func (c *sessionView) wait(t *testing.T, expected int) {
	t.Helper()
	select {
	case m := <-c.done:
		if m.Error != "" || m.Code != expected {
			t.Fatalf("session end = %+v, expected %d: %s", m, expected, c.text())
		}
	case <-time.After(8 * time.Second):
		t.Fatalf("session did not end: %s", c.text())
	}
}

func archive(t *testing.T, dataDir string) *history.Store {
	t.Helper()
	store, err := history.Open(filepath.Join(dataDir, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestShellRunsCommandWithArgumentsBytesAndResize(t *testing.T) {
	workspace, dataDir := t.TempDir(), t.TempDir()
	startTestServer(t, dataDir)
	input := []byte("hello\r\x03\x1b[A\x1b[200~two\nlines café\x1b[201~")
	args := []string{"echo", strconv.Itoa(len(input)), "--model", "argument with spaces"}
	c := launchAgent(t, workspace, args...)
	waitFor(t, func() bool { return strings.Contains(c.text(), "READY") })
	store := archive(t, dataDir)
	sessions, err := store.ListSessions(history.SessionFilter{Limit: 1000})
	if err != nil || len(sessions) != 1 {
		t.Fatal(sessions, err)
	}
	session := sessions[0]
	if err = c.conn.Send(protocol.Message{Type: "resize", Size: protocol.Size{Rows: 40, Cols: 120}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		events, _ := recordedEvents(t, store, session.ID)
		for _, e := range events {
			if e.Kind == "resize" {
				return true
			}
		}
		return false
	})
	if _, err = c.Write(input); err != nil {
		t.Fatal(err)
	}
	c.wait(t, 7)
	events, err := recordedEvents(t, store, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var inputs, outputs []byte
	for _, e := range events {
		switch e.Kind {
		case "input":
			inputs = append(inputs, e.Data...)
		case "output":
			outputs = append(outputs, e.Data...)
		case "start":
			var metadata struct{ Command []string }
			if json.Unmarshal(e.Data, &metadata) != nil || !reflect.DeepEqual(metadata.Command, []string{"/bin/sh", "-i"}) {
				t.Fatalf("did not launch interactive shell: %s", e.Data)
			}
		}
	}
	if !bytes.Equal(inputs, append(c.launchInput, input...)) {
		t.Fatalf("recorded input = %x, expected %x", inputs, input)
	}
	canonical, _ := agent.Canonical(workspace)
	for _, expected := range []string{fmt.Sprintf("ARGS=%q", args[1:]), fmt.Sprintf("BYTES=%x", input), "SIZE=40x120", "CWD=" + canonical, "\x1b[32mrecorded café\x1b[0m"} {
		if !bytes.Contains(outputs, []byte(expected)) {
			t.Fatalf("missing %q: %s", expected, outputs)
		}
	}
	matches, err := store.Query(context.Background(), "SELECT seq FROM events WHERE session_id='"+session.ID+"' AND text LIKE '%recorded café%'")
	if err != nil || len(matches.Rows) == 0 {
		t.Fatal("recorded output is not searchable", err)
	}
	latest, _ := store.LatestRun(session.ID)
	if latest.Status != "exited" || latest.Detail != "exit status 7" {
		t.Fatalf("wrong final state: %+v", latest)
	}
}

func TestSessionCtrlCReachesChild(t *testing.T) {
	dataDir := t.TempDir()
	startTestServer(t, dataDir)
	c := launchAgent(t, t.TempDir(), "interrupt")
	waitFor(t, func() bool { return strings.Contains(c.text(), "READY") })
	c.Write([]byte{3})
	c.wait(t, 130)
	waitFor(t, func() bool { return strings.Contains(c.text(), "INTERRUPTED") })
}

func TestViewClosureKeepsProcessAndHistory(t *testing.T) {
	dataDir := t.TempDir()
	startTestServer(t, dataDir)
	c := launchAgent(t, t.TempDir(), "wait")
	waitFor(t, func() bool { return strings.Contains(c.text(), "READY") })
	store := archive(t, dataDir)
	sessions, _ := store.ListSessions(history.SessionFilter{Limit: 1000})
	pid := sessions[0].Run.PID
	c.Close()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("child died with view: %v", err)
	}
	latest, _ := store.LatestRun(sessions[0].ID)
	if latest.Status != "running" {
		t.Fatalf("wrong detached state: %+v", latest)
	}
	stopTestSession(t, sessions[0].ID)
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("explicit stop left child: %v", err)
	}
	matches, err := store.Query(context.Background(), "SELECT seq FROM events WHERE session_id='"+sessions[0].ID+"' AND text LIKE '%READY%'")
	if err != nil || len(matches.Rows) == 0 {
		t.Fatal("history lost after view closure", err)
	}
}

func TestShellAgentCanReadCurrentHistory(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("BONBON_SESSION", "wrong-inherited-session")
	startTestServer(t, dataDir)
	c := launchAgent(t, t.TempDir(), "scope")
	c.wait(t, 0)
	if !strings.Contains(c.text(), "SCOPED_HISTORY_OK") {
		t.Fatal(c.text())
	}
}

func TestMissingShellKeepsProjectDraft(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SHELL", "bonbon-nonexistent-fixture")
	startTestServer(t, directory)
	c := openSessionView(t, shellStartRequest(t, t.TempDir(), "", protocol.Size{Rows: 24, Cols: 80}))
	select {
	case m := <-c.done:
		if m.Type != "error" || !strings.Contains(m.Error, "bonbon-nonexistent-fixture") {
			t.Fatal(m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing shell was not rejected")
	}
	if sessions := sessionInfos(t, 10); len(sessions) != 1 || sessions[0].Status != "draft" {
		t.Fatal("failed lookup did not retain draft", sessions)
	}
}

func TestConcurrentShellsShareSameAndOverlappingWorkspaces(t *testing.T) {
	dataDir, root := t.TempDir(), t.TempDir()
	workspace := filepath.Join(root, "workspace")
	child := filepath.Join(workspace, "child")
	alias := filepath.Join(root, "alias")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	startTestServer(t, dataDir)
	paths := []string{workspace, workspace, alias, root, child}
	clients := make([]*sessionView, 0, len(paths))
	for i, path := range paths {
		c := newShell(t, path, fmt.Sprintf("shell-%d", i))
		fmt.Fprintf(c, "printf 'SHELL_READY_%%s\\n' '%d'\r", i)
		waitFor(t, func() bool { return strings.Contains(c.text(), fmt.Sprintf("SHELL_READY_%d", i)) })
		clients = append(clients, c)
		if i == 0 {
			c.Close()
		}
	}
	store := archive(t, dataDir)
	sessions, err := store.ListSessions(history.SessionFilter{Limit: 1000})
	if err != nil || len(sessions) != len(paths) {
		t.Fatal(sessions, err)
	}
	pids := map[int]bool{}
	ids := map[string]string{}
	for _, s := range sessions {
		if s.Run == nil || s.Run.Status != "running" || pids[s.Run.PID] {
			t.Fatal("sessions did not run independently", s)
		}
		pids[s.Run.PID] = true
		ids[s.Title] = s.ID
	}
	stopTestSession(t, ids["shell-1"])
	clients[1].wait(t, 129)
	clients[2].Write([]byte("exit 3\r"))
	clients[2].wait(t, 3)
	for _, title := range []string{"shell-0", "shell-3", "shell-4"} {
		run, err := store.LatestRun(ids[title])
		if err != nil || run.Status != "running" {
			t.Fatal("stopping another session affected shared workspace", run, err)
		}
		if err := syscall.Kill(run.PID, 0); err != nil {
			t.Fatal("another shell died", err)
		}
	}
	for i := range paths {
		output := recordedOutput(t, store, ids[fmt.Sprintf("shell-%d", i)])
		if !strings.Contains(output, fmt.Sprintf("SHELL_READY_%d", i)) {
			t.Fatal("lost session history", output)
		}
	}
}

func TestInterruptedHistoryDoesNotBlockNewSessionsOrReplayInput(t *testing.T) {
	dataDir, workspace := t.TempDir(), t.TempDir()
	canonical, _ := agent.Canonical(workspace)
	store := archive(t, dataDir)
	session, err := store.CreateSession("Synthetic interrupted run", canonical, "")
	if err != nil {
		t.Fatal(err)
	}
	previous, err := store.NewRun(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Append(session.ID, previous.ID, "input", []byte("never replay me"), ""); err != nil {
		t.Fatal(err)
	}
	startTestServer(t, dataDir)
	before, err := store.LatestRun(session.ID)
	if err != nil || before.Status != "interrupted" {
		t.Fatal(before, err)
	}
	// Stopping a historical run changes neither its recorded state nor any process.
	stopTestSession(t, session.ID)
	after, err := store.LatestRun(session.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal(after, err)
	}
	c := launchAgent(t, workspace, "scope")
	c.wait(t, 0)
	sessions, err := store.ListSessions(history.SessionFilter{Limit: 1000})
	if err != nil || len(sessions) != 2 {
		t.Fatal("wrong session count", sessions, err)
	}
	for _, s := range sessions {
		events, err := recordedEvents(t, store, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if s.ID == session.ID {
			if len(events) != 2 || events[0].Kind != "input" || string(events[0].Data) != "never replay me" || events[1].Kind != "notice" {
				t.Fatal("interrupted evidence changed", events)
			}
			continue
		}
		for _, e := range events {
			if e.Kind == "input" && bytes.Contains(e.Data, []byte("never replay me")) {
				t.Fatal("old input was replayed")
			}
		}
	}
}

func sessionInfos(t *testing.T, limit int) []protocol.SessionInfo {
	t.Helper()
	output, err := (client.Target{Dir: os.Getenv("BONBON_DIR")}).Call(protocol.Request{Operation: "session-list", Limit: limit})
	if err != nil {
		t.Fatalf("list: %s: %v", output, err)
	}
	var sessions []protocol.SessionInfo
	if err := json.Unmarshal(output, &sessions); err != nil {
		t.Fatal(err)
	}
	return sessions
}

func stopTestSession(t *testing.T, id string) {
	t.Helper()
	if output, err := (client.Target{Dir: os.Getenv("BONBON_DIR")}).Call(protocol.Request{Operation: "session-stop", Session: id}); err != nil {
		t.Fatalf("stop: %s: %v", output, err)
	}
}

func TestViewReconnectKeepsProcessAndBackgroundRecording(t *testing.T) {
	dataDir := t.TempDir()
	startTestServer(t, dataDir)
	first := launchAgent(t, t.TempDir(), "stream")
	waitFor(t, func() bool { return strings.Contains(first.text(), "READY") })
	store := archive(t, dataDir)
	sessions, _ := store.ListSessions(history.SessionFilter{Limit: 1000})
	id, pid := sessions[0].ID, sessions[0].Run.PID
	first.Close()
	waitFor(t, func() bool { return sessionInfos(t, 10)[0].Viewers == 0 })
	count := strings.Count(recordedOutput(t, store, id), "TICK")
	waitFor(t, func() bool { return strings.Count(recordedOutput(t, store, id), "TICK") > count+1 })
	second := resumeView(t, id)
	waitFor(t, func() bool {
		return strings.Contains(second.text(), "READY") && strings.Contains(second.text(), "TICK")
	})
	latest, _ := store.LatestRun(id)
	if latest.PID != pid || latest.Status != "running" {
		t.Fatalf("reconnect replaced process: %+v", latest)
	}
	second.Write([]byte("q"))
	second.wait(t, 19)
	if !strings.Contains(second.text(), "FINISHED") {
		t.Fatal(second.text())
	}
	events, _ := recordedEvents(t, store, id)
	var input []byte
	starts := 0
	for _, e := range events {
		if e.Kind == "input" {
			input = append(input, e.Data...)
		}
		if e.Kind == "start" {
			starts++
		}
	}
	if !bytes.Equal(input, append(first.launchInput, 'q')) || starts != 1 {
		t.Fatalf("replayed input or run: %x, starts=%d", input, starts)
	}
	ended := resumeView(t, id)
	ended.wait(t, 0)
	if !strings.Contains(ended.text(), "FINISHED") {
		t.Fatal(ended.text())
	}
	after, _ := recordedEvents(t, store, id)
	if !reflect.DeepEqual(events, after) {
		t.Fatal("ended view changed history")
	}
	sessions, _ = store.ListSessions(history.SessionFilter{Limit: 1000})
	if len(sessions) != 1 || sessions[0].Run.PID != pid {
		t.Fatal("ended view created a run", sessions)
	}
	stopTestSession(t, id) // Idempotent after normal exit.
}

func TestDefaultShellAndStopForegroundJob(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SHELL", "")
	startTestServer(t, dataDir)
	c := newShell(t, t.TempDir(), "")
	exe, _ := os.Executable()
	// The temporary test executable path contains no single quotes.
	fmt.Fprintf(c, "'%s' --fixture-agent wait\r", exe)
	waitFor(t, func() bool { return strings.Contains(c.text(), "READY") })
	var jobPID int
	for _, line := range strings.Split(c.text(), "\n") {
		if index := strings.Index(line, "JOB_PID="); index >= 0 {
			fmt.Sscanf(line[index:], "JOB_PID=%d", &jobPID)
		}
	}
	if jobPID == 0 {
		t.Fatal("missing foreground job PID", c.text())
	}
	sessions := sessionInfos(t, 10)
	stopTestSession(t, sessions[0].ID)
	c.wait(t, 129)
	waitFor(t, func() bool { return errors.Is(syscall.Kill(jobPID, 0), syscall.ESRCH) })
	store := archive(t, dataDir)
	run, _ := store.LatestRun(sessions[0].ID)
	if run.Status != "stopped" {
		t.Fatalf("shell job was not stopped: %+v", run)
	}
}

func TestRecentSessionList(t *testing.T) {
	dataDir := t.TempDir()
	startTestServer(t, dataDir)
	store := archive(t, dataDir)
	var first history.Session
	for i := 0; i < 12; i++ {
		session, err := store.CreateSession(fmt.Sprintf("session %d", i), t.TempDir(), "")
		if err != nil {
			t.Fatal(err)
		}
		run, _ := store.NewRun(session.ID)
		run.Status = "exited"
		store.SaveRun(run)
		store.Append(session.ID, run.ID, "output", []byte("OLD"), "OLD")
		if i == 0 {
			first = session
		}
	}
	firstRun, _ := store.LatestRun(first.ID)
	if _, err := store.Append(first.ID, firstRun.ID, "output", []byte("LATEST"), "LATEST"); err != nil {
		t.Fatal(err)
	}
	output, err := (client.Target{Dir: dataDir}).Call(protocol.Request{Operation: "session-list"})
	var recent []protocol.SessionInfo
	if err != nil || json.Unmarshal(output, &recent) != nil || len(recent) != 10 || recent[0].ID != first.ID {
		t.Fatalf("recent list: %s %v", output, err)
	}
	if len(sessionInfos(t, 50)) != 12 {
		t.Fatal("limit override ignored")
	}

}

func recordedEvents(t *testing.T, store *history.Store, id string) ([]history.Event, error) {
	t.Helper()
	result, err := store.Query(context.Background(), "SELECT kind,data FROM events WHERE session_id='"+id+"' ORDER BY seq")
	if err != nil {
		return nil, err
	}
	events := make([]history.Event, 0, len(result.Rows))
	for _, row := range result.Rows {
		events = append(events, history.Event{Kind: row[0].(string), Data: row[1].([]byte)})
	}
	return events, nil
}

// Inspect original output for fixture assertions without a product text-replay API.
func recordedOutput(t *testing.T, store *history.Store, id string) string {
	t.Helper()
	events, err := recordedEvents(t, store, id)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for _, event := range events {
		if event.Kind == "output" {
			output.Write(event.Data)
		}
	}
	return output.String()
}
