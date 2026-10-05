package server

import (
	"os"
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
