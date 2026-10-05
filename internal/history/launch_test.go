package history

import (
	"path/filepath"
	"testing"
)

func TestSettingsPersistAndRejectStaleEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.Settings()
	if err != nil || len(settings.Tools) != 0 || settings.Worktree || settings.Base != "HEAD" {
		t.Fatal(settings, err)
	}
	settings.Tools = []Tool{{ID: "astra", Name: "Codex 6 Astra", Command: "codex --model=gpt-6-astra"}, {ID: "default", Name: "Codex", Command: "codex"}}
	settings.DefaultTool = "astra"
	settings.Worktree = true
	saved, err := store.SaveSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveSettings(settings); err == nil {
		t.Fatal("overwrote settings")
	}
	project, err := store.CreateProject("Project", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreatePreparation("Prepared", project.Workspace, project.ID, Preparation{Command: "echo saved", ToolName: "Fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.Settings()
	if err != nil || got.Revision != saved.Revision || got.Tools[0].Command != "codex --model=gpt-6-astra" || !got.Worktree {
		t.Fatal(got, err)
	}
	restored, err := store.Session(session.ID)
	if err != nil || restored.Preparation == nil || restored.Preparation.Command != "echo saved" || restored.Preparation.State != "draft" || restored.Run != nil {
		t.Fatal("preparation changed on reopen", restored, err)
	}
	got.Tools = got.Tools[1:]
	if _, err = store.SaveSettings(got); err == nil {
		t.Fatal("accepted absent default tool")
	}
	for _, command := range []string{"echo hi\rwhoami", "echo\x00bad", "echo hi\nexit"} {
		if ValidateCommand(command) == nil {
			t.Fatal("accepted control character")
		}
	}
}
