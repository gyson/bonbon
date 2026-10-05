package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func TestProjectWorkspaceResolution(t *testing.T) {
	s, _ := composerServer(t)
	t.Setenv("SHELL", "/bin/sh")
	if err := s.ensureGeneral(); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "repo"), 0700); err != nil {
		t.Fatal(err)
	}
	p, err := s.addProject("", "~/repo")
	if err != nil || p.Name != "repo" {
		t.Fatalf("add project: %+v %v", p, err)
	}
	alias := filepath.Join(home, "alias")
	if err = os.Symlink(p.Workspace, alias); err != nil {
		t.Fatal(err)
	}
	if _, err = s.addProject("Alias", alias); err == nil {
		t.Fatal("accepted duplicate canonical folder")
	}
	request := &protocol.Run{ProjectID: p.ID, Size: protocol.Size{Rows: 24, Cols: 80}}
	run, err := s.prepareProjectRun(request)
	if err != nil || run.Workspace != p.Workspace || request.Workspace != "" {
		t.Fatalf("project launch: %+v %v", run, err)
	}
	request.Workspace = p.Workspace
	if _, err = s.prepareProjectRun(request); err == nil {
		t.Fatal("accepted project with workspace override")
	}
	request.Workspace = ""
	request.ProjectID = "missing"
	if _, err = s.prepareProjectRun(request); err == nil {
		t.Fatal("accepted missing project")
	}
	request.ProjectID = history.GeneralProjectID
	run, err = s.prepareProjectRun(request)
	if err != nil || !strings.HasSuffix(run.Workspace, "/workspaces/general") {
		t.Fatalf("general launch: %+v %v", run, err)
	}
	if err = s.ensureGeneral(); err != nil {
		t.Fatal(err)
	}
	projects, err := s.store.Projects()
	if err != nil || len(projects) != 2 {
		t.Fatalf("duplicate General: %+v %v", projects, err)
	}
	if err = os.Remove(p.Workspace); err != nil {
		t.Fatal(err)
	}
	if _, err = s.addProject("missing", p.Workspace); err == nil {
		t.Fatal("accepted absent folder")
	}
}

func TestHistoryInstructionsCommandsAndNoInput(t *testing.T) {
	s, id := composerServer(t)
	s.info.Executable = "/a path/BonBon's $(false) binary"
	s.info.DataDir = "/a path/instance's `false` directory"
	text := s.historyInstructions()
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, shellQuote(s.info.Executable)+" --dir ") || !strings.Contains(line, "query \"") {
			continue
		}
		// Parse the shell quoting without running the executable, including unusual paths.
		script := "set -- " + line + "\nprintf '%s\\n' \"$1\" \"$2\" \"$3\" \"$4\" \"$5\""
		output, err := exec.Command("/bin/sh", "-c", script).Output()
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
		if len(args) != 5 || args[0] != s.info.Executable || args[1] != "--dir" || args[2] != s.info.DataDir || args[3] != "query" {
			t.Fatalf("unsafe command: %q", output)
		}
		if _, err = s.store.Query(context.Background(), args[4]); err != nil {
			t.Fatalf("example SQL %q: %v", args[4], err)
		}
		count++
	}
	if count != 5 {
		t.Fatalf("expected 5 executable query examples, got %d", count)
	}
	result, err := s.store.Query(context.Background(), "SELECT count(*) FROM events WHERE session_id='"+id+"'")
	if err != nil || result.Rows[0][0] != int64(0) {
		t.Fatal("reading instructions changed history", result, err)
	}
}
