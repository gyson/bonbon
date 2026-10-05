package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bonbon/internal/history"
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
	draft, err := s.prepareProject(context.Background(), p.ID)
	if err != nil || draft.Workspace != p.Workspace || draft.ProjectID != p.ID {
		t.Fatalf("project preparation: %+v %v", draft, err)
	}
	for _, id := range []string{"", "missing"} {
		if _, err = s.prepareProject(context.Background(), id); err == nil {
			t.Fatal("accepted missing project")
		}
	}
	draft, err = s.prepareProject(context.Background(), history.GeneralProjectID)
	if err != nil || !strings.HasSuffix(draft.Workspace, "/workspaces/general") {
		t.Fatalf("general preparation: %+v %v", draft, err)
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
