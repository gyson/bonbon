package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func preparationConfig(p history.Preparation) protocol.PreparationConfig {
	return protocol.PreparationConfig{Revision: p.Revision, ToolID: p.ToolID, Worktree: p.Worktree, Base: p.Base, Branch: p.Branch}
}

func projectStartRequest(t *testing.T, project, title string, size protocol.Size) protocol.Request {
	t.Helper()
	target := client.Target{Dir: os.Getenv("BONBON_DIR")}
	data, err := target.Call(protocol.Request{Operation: "project-draft", Project: project})
	if err != nil {
		t.Fatal(err)
	}
	var session history.Session
	if err = json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	if title != session.Title {
		config := preparationConfig(*session.Preparation)
		data, err = target.Call(protocol.Request{Operation: "session-config", Session: session.ID, Name: title, Preparation: &config})
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &session); err != nil {
			t.Fatal(err)
		}
	}
	return protocol.Request{Operation: "session-start", Session: session.ID, Revision: session.Preparation.Revision, Size: size}
}

func shellStartRequest(t *testing.T, workspace, title string, size protocol.Size) protocol.Request {
	t.Helper()
	if strings.HasPrefix(workspace, "~/") {
		workspace = filepath.Join(os.Getenv("HOME"), workspace[2:])
	}
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	target := client.Target{Dir: os.Getenv("BONBON_DIR")}
	data, err := target.Call(protocol.Request{Operation: "project-list"})
	if err != nil {
		t.Fatal(err)
	}
	var projects []history.Project
	if err = json.Unmarshal(data, &projects); err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		if project.Workspace == canonical {
			return projectStartRequest(t, project.ID, title, size)
		}
	}
	data, err = target.Call(protocol.Request{Operation: "project-add", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	var project history.Project
	if err = json.Unmarshal(data, &project); err != nil {
		t.Fatal(err)
	}
	return projectStartRequest(t, project.ID, title, size)
}

func TestPreparedStartupCommandRunsOnceAndKeepsDraft(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			if _, err := os.Stat(shell); err != nil {
				t.Skip("shell unavailable")
			}
			t.Setenv("SHELL", shell)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("ZDOTDIR", home)
			directory, workspace := t.TempDir(), t.TempDir()
			startTestServer(t, directory)
			target := client.Target{Dir: directory}
			rpc := func(request protocol.Request, result any) {
				t.Helper()
				data, err := target.Call(request)
				if err != nil {
					t.Fatal(request.Operation, err)
				}
				if result != nil {
					if err = json.Unmarshal(data, result); err != nil {
						t.Fatal(err)
					}
				}
			}
			marker := filepath.Join(workspace, "count")
			command := fmt.Sprintf("printf x >> '%s'; printf 'STARTUP_%%s\\n' done", marker)
			var settings history.Settings
			rpc(protocol.Request{Operation: "settings-save", Settings: &history.Settings{DefaultTool: "fixture", Tools: []history.Tool{{ID: "fixture", Name: "Fixture", Command: command}}}}, &settings)
			var session history.Session
			var project history.Project
			rpc(protocol.Request{Operation: "project-add", Workspace: workspace}, &project)
			rpc(protocol.Request{Operation: "project-draft", Project: project.ID}, &session)
			rpc(protocol.Request{Operation: "composer-draft", Session: session.ID, Draft: &protocol.Draft{Text: "unsent draft"}}, nil)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("preparing executed startup command")
			}
			p := session.Preparation
			stale := preparationConfig(*p)
			rpc(protocol.Request{Operation: "settings-save", Settings: &history.Settings{Revision: settings.Revision, Tools: []history.Tool{}}}, nil)
			view := openSessionView(t, protocol.Request{Operation: "session-start", Session: session.ID, Revision: p.Revision, Size: protocol.Size{Rows: 24, Cols: 80}})
			waitFor(t, func() bool { data, _ := os.ReadFile(marker); return string(data) == "x" })
			waitFor(t, func() bool { return strings.Contains(view.text(), "STARTUP_done") })
			if _, err := target.Call(protocol.Request{Operation: "session-config", Session: session.ID, Preparation: &stale, Name: "changed"}); err == nil {
				t.Fatal("edited launched command")
			}
			duplicate := openSessionView(t, protocol.Request{Operation: "session-start", Session: session.ID, Revision: p.Revision, Size: protocol.Size{Rows: 24, Cols: 80}})
			if message := <-duplicate.done; message.Type != "error" {
				t.Fatal("duplicate start succeeded", message)
			}
			view.Close()
			resumed := resumeView(t, session.ID)
			waitFor(t, func() bool { return strings.Contains(resumed.text(), "STARTUP_done") })
			if _, err := resumed.Write([]byte("printf 'SHELL_%s\\n' alive\r")); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return strings.Contains(resumed.text(), "SHELL_alive") })
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "x" {
				t.Fatal("command replayed", string(data), err)
			}
			var composer protocol.Composer
			rpc(protocol.Request{Operation: "composer-draft", Session: session.ID}, &composer)
			if composer.Draft.Text != "unsent draft" || composer.Draft.Pending {
				t.Fatal("startup submitted editor draft", composer)
			}
			rpc(protocol.Request{Operation: "session-stop", Session: session.ID}, nil)
			var query history.QueryResult
			rpc(protocol.Request{Operation: "query", SQL: fmt.Sprintf("SELECT count(*) FROM events WHERE session_id='%s' AND kind='input'", session.ID)}, &query)
			if query.Rows[0][0] != float64(2) {
				t.Fatal("unexpected terminal input", query)
			}
		})
	}
}

func TestProjectDraftSurvivesRestartAndBecomesSession(t *testing.T) {
	directory := t.TempDir()
	startTestServer(t, directory)
	target := client.Target{Dir: directory}
	rpc := func(request protocol.Request, result any) {
		t.Helper()
		data, err := target.Call(request)
		if err != nil {
			t.Fatal(request.Operation, err)
		}
		if result != nil {
			if err = json.Unmarshal(data, result); err != nil {
				t.Fatal(err)
			}
		}
	}
	var first, second history.Project
	rpc(protocol.Request{Operation: "project-add", Name: "First", Workspace: t.TempDir()}, &first)
	rpc(protocol.Request{Operation: "project-add", Name: "Second", Workspace: t.TempDir()}, &second)
	var original, other history.Session
	rpc(protocol.Request{Operation: "project-draft", Project: first.ID}, &original)
	rpc(protocol.Request{Operation: "project-draft", Project: second.ID}, &other)
	if original.ID == other.ID {
		t.Fatal("projects share a draft")
	}
	var file protocol.Attachment
	rpc(protocol.Request{Operation: "composer-attach", Session: original.ID, Upload: &protocol.Upload{Name: "note.txt", Data: []byte("project attachment")}}, &file)
	rpc(protocol.Request{Operation: "composer-draft", Session: original.ID, Draft: &protocol.Draft{Text: "unsent project message", Attachments: []int64{file.ID}}}, nil)
	config := preparationConfig(*original.Preparation)
	rpc(protocol.Request{Operation: "session-config", Session: original.ID, Name: "Saved project draft", Preparation: &config}, &original)
	if sessions := sessionInfos(t, 100); len(sessions) != 0 {
		t.Fatal("unstarted drafts entered session list", sessions)
	}
	if output, err := testCommand("server", "restart").CombinedOutput(); err != nil {
		t.Fatalf("restart: %s %v", output, err)
	}
	var restored history.Session
	rpc(protocol.Request{Operation: "project-draft", Project: first.ID}, &restored)
	if restored.ID != original.ID || restored.Title != original.Title || *restored.Preparation != *original.Preparation {
		t.Fatal("lost draft settings", restored)
	}
	var composer protocol.Composer
	rpc(protocol.Request{Operation: "composer-draft", Session: restored.ID}, &composer)
	if composer.Draft.Text != "unsent project message" || len(composer.Attachments) != 1 || composer.Attachments[0].ID != file.ID {
		t.Fatal("lost project composition", composer)
	}
	view := openSessionView(t, protocol.Request{Operation: "session-start", Session: restored.ID, Revision: restored.Preparation.Revision, Size: protocol.Size{Rows: 24, Cols: 80}})
	waitFor(t, func() bool { return strings.Contains(view.text(), "BONBON_SHELL_PROMPT> ") })
	var next history.Session
	rpc(protocol.Request{Operation: "project-draft", Project: first.ID}, &next)
	if next.ID == original.ID || next.Title != "" || next.Run != nil {
		t.Fatal("project did not get a fresh draft", next)
	}
	rpc(protocol.Request{Operation: "composer-draft", Session: next.ID}, &composer)
	if composer.Draft.Text != "" || len(composer.Attachments) != 0 {
		t.Fatal("new project draft inherited session message", composer)
	}
	rpc(protocol.Request{Operation: "composer-draft", Session: original.ID}, &composer)
	if composer.Draft.Text != "unsent project message" {
		t.Fatal("start consumed draft", composer)
	}
	rpc(protocol.Request{Operation: "project-draft", Project: second.ID}, &restored)
	if restored.ID != other.ID {
		t.Fatal("another project's draft changed")
	}
	if sessions := sessionInfos(t, 100); len(sessions) != 1 || sessions[0].ID != original.ID {
		t.Fatal("wrong started sessions", sessions)
	}
	rpc(protocol.Request{Operation: "project-remove", Project: first.ID}, nil)
	rpc(protocol.Request{Operation: "session-config", Session: next.ID}, &restored)
	if restored.ProjectID != "" || restored.Workspace != first.Workspace {
		t.Fatal("project removal lost draft", restored)
	}
	rpc(protocol.Request{Operation: "session-stop", Session: original.ID}, nil)
}
