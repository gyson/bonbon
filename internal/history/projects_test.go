package history

import (
	"context"
	"path/filepath"
	"testing"
)

func TestProjectsPersistAndRemovalPreservesSessionHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if err = s.EnsureGeneral("/instance/workspaces/general"); err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(" BonBon ", "/work/bonbon")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession("Review", project.Workspace, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.NewRun(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := s.Projects()
	if err != nil || len(projects) != 2 || projects[0].ID != GeneralProjectID || projects[1].Name != "BonBon" {
		t.Fatalf("projects after reopen: %+v %v", projects, err)
	}
	if err = s.RenameProject(project.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err = s.RenameSession(session.ID, "Investigate"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Session(session.ID)
	if err != nil || before.ProjectID != project.ID || before.Workspace != project.Workspace || before.Title != "Investigate" {
		t.Fatalf("renamed session: %+v %v", before, err)
	}
	if err = s.RemoveProject(project.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.Session(session.ID)
	if err != nil || after.ProjectID != "" || after.Workspace != before.Workspace || after.Run.ID != run.ID {
		t.Fatalf("lost session on removal: %+v %v", after, err)
	}
	result, err := s.Query(context.Background(), "SELECT project_id FROM sessions")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != nil {
		t.Fatalf("not detached in SQL: %+v %v", result, err)
	}
	if _, err = s.CreateSession("Stale project", project.Workspace, project.ID); err == nil {
		t.Fatal("accepted a removed project")
	}
}

func TestGeneralIdentityAndProjectValidation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnsureGeneral("/first/general"); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession("General work", "/first/general", GeneralProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureGeneral("/relocated/general"); err != nil {
		t.Fatal(err)
	}
	p, err := s.Project(GeneralProjectID)
	if err != nil || p.Workspace != "/relocated/general" {
		t.Fatalf("general: %+v %v", p, err)
	}
	old, err := s.Session(session.ID)
	if err != nil || old.Workspace != "/first/general" {
		t.Fatal("rewrote historical workspace", old, err)
	}
	if err = s.RemoveProject(GeneralProjectID); err == nil {
		t.Fatal("removed General")
	}
	if err = s.RenameProject(GeneralProjectID, "Other"); err == nil {
		t.Fatal("renamed General")
	}
	for _, name := range []string{"", " \t ", "two\nlines"} {
		if _, err = s.CreateProject(name, "/work"); err == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
	if _, err = s.CreateProject("Duplicate", "/relocated/general"); err == nil {
		t.Fatal("accepted duplicate workspace")
	}
	if err = s.RemoveProject("missing"); err == nil {
		t.Fatal("removed nonexistent project")
	}
}
