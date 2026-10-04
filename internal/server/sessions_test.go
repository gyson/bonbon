package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bonbon/internal/protocol"
	"github.com/gorilla/websocket"
)

func TestPrepareRunEnvironment(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "test-agent")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	t.Setenv("SHELL", "test-agent")
	t.Setenv("BONBON_TEST_LAUNCH", "from-server")
	size := protocol.Size{Rows: 24, Cols: 80}
	request := &protocol.Run{Workspace: directory, Size: size}
	run, err := prepareRun(request)
	if err != nil || run.Shell != executable {
		t.Fatalf("server launch: %+v %v", run, err)
	}
	if !strings.Contains(strings.Join(run.Env, "\n"), "BONBON_TEST_LAUNCH=from-server") || run.Env[len(run.Env)-1] != "TERM=xterm-256color" {
		t.Fatal("server environment not inherited")
	}
	t.Setenv("SHELL", "")
	run, err = prepareRun(request)
	if err != nil || run.Shell != "/bin/sh" {
		t.Fatalf("default shell: %+v %v", run, err)
	}

}

func TestPrepareRunRejectsInvalidLaunch(t *testing.T) {
	valid := protocol.Run{Workspace: t.TempDir(), Size: protocol.Size{Rows: 24, Cols: 80}}
	for _, change := range []func(*protocol.Run){
		func(r *protocol.Run) { r.Workspace = "." },
		func(r *protocol.Run) { r.Workspace = "~someone/project" },
		func(r *protocol.Run) { r.Workspace = "$HOME/project" },
		func(r *protocol.Run) { r.Size.Rows = 0 },
	} {
		run := valid
		change(&run)
		if _, err := prepareRun(&run); err == nil {
			t.Fatalf("accepted invalid launch: %+v", run)
		}
	}
}

func TestPrepareRunWorkspaceHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, path := range []struct{ input, want string }{
		{"~", home},
		{"~/", home},
		{"~/project with spaces", filepath.Join(home, "project with spaces")},
		{"~/project/../other", filepath.Join(home, "other")},
		{filepath.Join(home, "literal~", "$NAME"), filepath.Join(home, "literal~", "$NAME")},
	} {
		request := &protocol.Run{Workspace: path.input, Size: protocol.Size{Rows: 24, Cols: 80}}
		run, err := prepareRun(request)
		if err != nil || run.Workspace != path.want {
			t.Fatalf("workspace %q: got %+v, %v; want %q", path.input, run, err, path.want)
		}
		if request.Workspace != path.input {
			t.Fatal("modified the original workspace")
		}
	}
	for _, home := range []string{"", "relative/home"} {
		t.Setenv("HOME", home)
		request := &protocol.Run{Workspace: "~/project", Size: protocol.Size{Rows: 24, Cols: 80}}
		if _, err := prepareRun(request); err == nil {
			t.Fatalf("accepted unresolved home %q", home)
		}
	}
}

func attachmentPair(t *testing.T) (*protocol.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		accepted <- conn
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := <-accepted
	t.Cleanup(func() { peer.Close(); conn.Close() })
	return protocol.Wrap(conn), peer
}
