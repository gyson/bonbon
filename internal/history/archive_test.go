package history

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

func TestArchivePersistenceAndSearchAcrossPages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	project, err := s.CreateProject("Saved work", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreatePreparation("Old 100%_draft", project.Workspace, project.ID, Preparation{ToolName: "Shell", Base: "unfinished base"})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"text":"unsent message","attachments":[],"pending":false}`)
	revision, err := s.SaveDraft(first.ID, 0, data)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := s.Append(first.ID, "", "attachment", []byte{0, 1, 2, 255}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 105 {
		session, err := s.CreatePreparation(fmt.Sprintf("Draft %d", i), project.Workspace, project.ID, Preparation{})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetSessionArchived(session.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetSessionArchived(first.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := s.ListSessions(SessionFilter{Limit: 1000})
	if err != nil || len(visible) != 0 {
		t.Fatal("archived drafts leaked into normal list", visible, err)
	}
	page, err := s.ListSessions(SessionFilter{Limit: 100, Archived: true})
	if err != nil || len(page) != 100 {
		t.Fatal(page, err)
	}
	seen := map[string]bool{}
	for _, item := range page {
		seen[item.ID] = true
	}
	page, err = s.ListSessions(SessionFilter{Limit: 100, Offset: 100, Archived: true})
	if err != nil || len(page) != 6 || page[5].ID != first.ID {
		t.Fatal("old drafts are not reachable", page, err)
	}
	for _, item := range page {
		if seen[item.ID] {
			t.Fatal("overlapping pages", item.ID)
		}
	}
	for _, query := range []string{"OLD", "100%_", first.ID} {
		found, err := s.ListSessions(SessionFilter{Limit: 1, Archived: true, Query: query})
		if err != nil || len(found) != 1 || found[0].ID != first.ID {
			t.Fatal("search did not find old draft", query, found, err)
		}
	}
	for _, query := range []string{"saved WORK", "/workspace"} {
		found, err := s.ListSessions(SessionFilter{Limit: 1000, Archived: true, Query: query})
		if err != nil || len(found) != 106 {
			t.Fatal("project search failed", query, len(found), err)
		}
	}
	if err = s.RemoveProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSessionArchived(first.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Session(first.ID)
	if err != nil || got.Archived || got.ProjectID != "" || got.Title != first.Title || got.Workspace != first.Workspace || got.Run != nil || *got.Preparation != *first.Preparation {
		t.Fatal("restore changed session content", got, err)
	}
	draft, err := s.LatestDraft(first.ID)
	if err != nil || draft.Seq != revision || !bytes.Equal(draft.Data, data) {
		t.Fatal("archive lost draft revision", draft, err)
	}
	file, err := s.Attachment(first.ID, attachment.Seq)
	if err != nil || !bytes.Equal(file.Data, attachment.Data) {
		t.Fatal("archive lost original attachment", file, err)
	}
	visible, err = s.ListSessions(SessionFilter{})
	if err != nil || len(visible) != 1 || visible[0].ID != first.ID {
		t.Fatal("restore did not return draft to normal list", visible, err)
	}
}

func TestArchiveRejectsActiveRunsAndLaunchClaims(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	project, err := s.CreateProject("Project", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.CreatePreparation("Draft", project.Workspace, project.ID, Preparation{})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"creating", "launched"} {
		if err = s.SetPreparationState(draft.ID, 1, "draft", state); err != nil {
			t.Fatal(err)
		}
		if err = s.SetSessionArchived(draft.ID, true); err == nil {
			t.Fatal("archived unfinished launch", state)
		}
		if err = s.SetPreparationState(draft.ID, 1, state, "draft"); err != nil {
			t.Fatal(err)
		}
	}
	run, err := s.NewRun(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"starting", "running", "exited", "stopped", "failed", "interrupted"} {
		run.Status = status
		if err = s.SaveRun(run); err != nil {
			t.Fatal(err)
		}
		err = s.SetSessionArchived(draft.ID, true)
		if wantError := status == "starting" || status == "running"; (err != nil) != wantError {
			t.Fatal("incorrect archive eligibility", status, err)
		}
	}
	if err = s.SetSessionArchived("missing", true); err == nil {
		t.Fatal("archived nonexistent session")
	}
	for _, filter := range []SessionFilter{{Limit: -1}, {Limit: 1001}, {Offset: -1}} {
		if _, err = s.ListSessions(filter); err == nil {
			t.Fatal("accepted invalid filter", filter)
		}
	}
}
