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

func TestContinueCommandNeedsOneUnquotedPromptPlaceholder(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, err := store.Settings()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"codex", "codex {prompt} {prompt}", "codex '{prompt}'", `codex "{prompt}"`, "codex {prompt}\rwhoami"} {
		settings.Tools = []Tool{{ID: "codex", Name: "Codex", Command: "codex", ContinueCommand: command}}
		if _, err = store.SaveSettings(settings); err == nil {
			t.Fatalf("accepted continue command %q", command)
		}
	}
	settings.Tools = []Tool{
		{ID: "codex", Name: "Codex", Command: "codex", ContinueCommand: "codex --model=gpt-6-astra {prompt}"},
		{ID: "plain", Name: "Plain", Command: "plain", ContinueCommand: "   "},
	}
	saved, err := store.SaveSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Settings()
	if err != nil || got.Revision != saved.Revision || got.Tools[0].ContinueCommand != "codex --model=gpt-6-astra {prompt}" || got.Tools[1].ContinueCommand != "" {
		t.Fatal(got, err)
	}
}

func TestRunsListEachTerminalOfASessionInOrder(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession("Runs", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := store.Runs(session.ID); err != nil || len(runs) != 0 {
		t.Fatal(runs, err)
	}
	var ids []string
	var last int64
	for i := range 3 {
		run, err := store.NewRun(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, run.ID)
		if i == 2 {
			break // A run that never started has no events.
		}
		if _, err = store.Append(session.ID, run.ID, "start", []byte(`{"tool":"Fixture"}`), ""); err != nil {
			t.Fatal(err)
		}
		event, err := store.Append(session.ID, run.ID, "output", []byte("out"), "out")
		if err != nil {
			t.Fatal(err)
		}
		last = event.Seq
	}
	runs, err := store.Runs(session.ID)
	if err != nil || len(runs) != 3 {
		t.Fatal(runs, err)
	}
	for i, run := range runs {
		if run.ID != ids[i] {
			t.Fatalf("run %d is %s, expected %s", i, run.ID, ids[i])
		}
	}
	if runs[1].Last != last || runs[1].First != last-1 || string(runs[1].Start) != `{"tool":"Fixture"}` || runs[2].First != 0 || len(runs[2].Start) != 0 {
		t.Fatalf("%+v", runs)
	}
}
