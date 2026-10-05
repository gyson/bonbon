package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func TestProjectDraftIsSharedAndCommandsComeFromSettings(t *testing.T) {
	s, _ := composerServer(t)
	project, err := s.addProject("Draft project", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.store.SaveSettings(history.Settings{DefaultTool: "agent", Tools: []history.Tool{
		{ID: "agent", Name: "Agent", Command: "echo configured"},
		{ID: "other", Name: "Other", Command: "echo other"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			draft, err := s.prepareProject(context.Background(), project.ID)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- draft.ID
		})
	}
	wg.Wait()
	close(ids)
	var id string
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("concurrent tabs created different drafts")
		}
		id = got
	}
	if id == "" {
		t.Fatal("no project draft")
	}
	settings.Tools[0].Command = "echo changed later"
	if _, err = s.store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	// A client cannot replace the command or the owning project/workspace.
	var request protocol.Request
	if err = json.Unmarshal([]byte(`{"name":"Keep my draft","preparation":{"revision":1,"toolId":"agent","command":"echo injected","toolName":"Injected"},"run":{"workspace":"/other","projectId":"other"}}`), &request); err != nil {
		t.Fatal(err)
	}
	request.Session = id
	saved, err := s.savePreparation(&request)
	if err != nil || saved.Preparation.Command != "echo configured" || saved.Preparation.ToolName != "Agent" || saved.ProjectID != project.ID || saved.Workspace != project.Workspace {
		t.Fatalf("client rewrote snapshot: %+v %v", saved, err)
	}
	for _, tool := range []string{"other", ""} {
		request.Preparation.Revision = saved.Preparation.Revision
		request.Preparation.ToolID = tool
		saved, err = s.savePreparation(&request)
		if err != nil {
			t.Fatal(err)
		}
		want := "echo other"
		if tool == "" {
			want = ""
		}
		if saved.Preparation.Command != want {
			t.Fatal(saved.Preparation)
		}
	}
	request.Preparation.Revision = saved.Preparation.Revision
	request.Preparation.ToolID = "missing"
	if _, err = s.savePreparation(&request); err == nil {
		t.Fatal("accepted absent tool")
	}
	reopened, err := s.prepareProject(context.Background(), project.ID)
	if err != nil || reopened.ID != id || reopened.Title != "Keep my draft" || reopened.Preparation.Command != "" {
		t.Fatalf("reopening overwrote draft: %+v %v", reopened, err)
	}
	projects, err := s.store.Projects()
	if err != nil || len(projects) != 1 || projects[0].DraftID != id {
		t.Fatal(projects, err)
	}
	recent, err := s.store.RecentSessions(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range recent {
		if session.ID == id {
			t.Fatal("project draft appeared as a started session")
		}
	}
}

func fixtureRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}} {
		if _, err := git(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "tracked.txt"), []byte("committed"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
		if _, err := git(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPreparedSessionDefaultsAndWorktreeLifecycle(t *testing.T) {
	s, _ := composerServer(t)
	t.Setenv("SHELL", "/bin/sh")
	ctx := context.Background()
	repo := fixtureRepository(t)
	source := filepath.Join(repo, "sub")
	settings, err := s.store.SaveSettings(history.Settings{DefaultTool: "fixture", Worktree: true, Base: "HEAD", Tools: []history.Tool{{ID: "fixture", Name: "Fixture model", Command: "printf 'hello\\n'"}}})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.addProject("Repository", source)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.prepareProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Run != nil || !session.Preparation.Worktree || session.Preparation.Command != "printf 'hello\\n'" {
		t.Fatalf("preparation: %+v", session)
	}
	if _, err = os.Stat(filepath.Join(s.info.DataDir, "worktrees")); !os.IsNotExist(err) {
		t.Fatal("created checkout before start", err)
	}
	_, err = s.composer(session.ID, &protocol.Draft{Text: "unsent message"})
	if err != nil {
		t.Fatal(err)
	}
	settings.Tools[0].Command = "printf 'changed\\n'"
	if _, err = s.store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("local changes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "untracked.txt"), []byte("local file"), 0600); err != nil {
		t.Fatal(err)
	}
	launched, run, err := s.launchPrepared(ctx, session.ID, session.Preparation.Revision, protocol.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	w := launched.Worktree
	if w == nil || !regexp.MustCompile(`/worktrees/\d{8}-\d{6}-[a-f0-9]{8}$`).MatchString(w.Path) || run.Workspace != filepath.Join(w.Path, "sub") || run.Command != "printf 'hello\\n'" {
		t.Fatalf("launch: %+v %+v", launched, run)
	}
	data, err := os.ReadFile(filepath.Join(run.Workspace, "tracked.txt"))
	if err != nil || string(data) != "committed" {
		t.Fatal(string(data), err)
	}
	if _, err = os.Stat(filepath.Join(run.Workspace, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatal("copied uncommitted file")
	}
	if _, _, err = s.launchPrepared(ctx, session.ID, session.Preparation.Revision, run.Size); err == nil {
		t.Fatal("started twice")
	}
	state, err := s.composer(session.ID, nil)
	if err != nil || state.Draft.Text != "unsent message" {
		t.Fatal(state, err)
	}
	dirty := filepath.Join(w.Path, "untracked")
	if err = os.WriteFile(dirty, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.removeWorktree(ctx, session.ID); err == nil {
		t.Fatal("removed dirty checkout")
	}
	if err = os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	if _, err = git(ctx, w.Path, "config", "core.excludesFile", filepath.Join(repo, "ignored-patterns")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(repo, "ignored-patterns"), []byte("ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(w.Path, "ignored"), []byte("keep ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.removeWorktree(ctx, session.ID); err == nil {
		t.Fatal("removed ignored file")
	}
	if err = os.Remove(filepath.Join(w.Path, "ignored")); err != nil {
		t.Fatal(err)
	}
	s.sessions = map[string]*runningSession{session.ID: {id: session.ID}}
	if err = s.removeWorktree(ctx, session.ID); err == nil {
		t.Fatal("removed active workspace")
	}
	delete(s.sessions, session.ID)
	if _, err = git(ctx, w.Path, "checkout", "--detach"); err != nil {
		t.Fatal(err)
	}
	if err = s.removeWorktree(ctx, session.ID); err == nil {
		t.Fatal("removed detached checkout")
	}
	if _, err = git(ctx, w.Path, "checkout", w.Branch); err != nil {
		t.Fatal(err)
	}
	if err = s.store.RemoveProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.removeWorktree(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = git(ctx, repo, "rev-parse", "--verify", w.Branch); err != nil {
		t.Fatal("removed branch", err)
	}
	persisted, err := s.store.Session(session.ID)
	if err != nil || persisted.Worktree.State != "removed" || persisted.ProjectID != "" {
		t.Fatal(persisted, err)
	}
}

func TestPreparationConflictsAndInterruptedCreation(t *testing.T) {
	s, _ := composerServer(t)
	ctx := context.Background()
	project, err := s.addProject("Draft", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.prepareProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := *session.Preparation
	changed := protocol.PreparationConfig{Revision: original.Revision, Base: "HEAD"}
	request := &protocol.Request{Session: session.ID, Preparation: &changed, Name: "Saved name"}
	saved, err := s.savePreparation(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.savePreparation(request); err == nil {
		t.Fatal("overwrote concurrent edit")
	}
	if _, _, err = s.launchPrepared(ctx, session.ID, original.Revision, protocol.Size{Rows: 24, Cols: 80}); err == nil {
		t.Fatal("launched stale config")
	}
	if err = s.store.SetPreparationState(session.ID, saved.Preparation.Revision, "draft", "creating"); err != nil {
		t.Fatal(err)
	}
	if err = s.store.MarkInterrupted(); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.store.Session(session.ID)
	if err != nil || recovered.Preparation.State != "interrupted" {
		t.Fatal(recovered, err)
	}
	if _, _, err = s.launchPrepared(ctx, session.ID, saved.Preparation.Revision, protocol.Size{Rows: 24, Cols: 80}); err == nil {
		t.Fatal("replayed uncertain launch")
	}
}

func TestGitCapabilityLinkedCheckoutAndInvalidBase(t *testing.T) {
	s, _ := composerServer(t)
	t.Setenv("SHELL", "/bin/sh")
	ctx := context.Background()
	repo := fixtureRepository(t)
	project, err := s.addProject("Git", repo)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.prepareProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := *session.Preparation
	p.Worktree = true
	p.Base = "--not-a-ref"
	session, err = s.store.SavePreparation(session.ID, session.Title, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.launchPrepared(ctx, session.ID, session.Preparation.Revision, protocol.Size{Rows: 24, Cols: 80}); err == nil {
		t.Fatal("accepted invalid base")
	}
	current, err := s.store.Session(session.ID)
	if err != nil || current.Preparation.State != "draft" {
		t.Fatal(current, err)
	}
	p = *current.Preparation
	p.Base = "HEAD"
	session, err = s.store.SavePreparation(session.ID, session.Title, p)
	if err != nil {
		t.Fatal(err)
	}
	launched, _, err := s.launchPrepared(ctx, session.ID, session.Preparation.Revision, protocol.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	if !inspectRepository(ctx, launched.Worktree.Path).Available {
		t.Fatal("linked checkout not detected")
	}
	if inspectRepository(ctx, t.TempDir()).Available {
		t.Fatal("non-Git folder accepted")
	}
	if err = s.removeWorktree(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(launched.Worktree.Branch, "bonbon/") {
		t.Fatal(launched.Worktree)
	}
}
