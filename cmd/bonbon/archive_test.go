package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func TestIndependentDraftsArchiveAndRestoreAcrossRestart(t *testing.T) {
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
	var first, second history.Session
	for _, draft := range []*history.Session{&first, &second} {
		rpc(protocol.Request{Operation: "project-draft", Project: history.GeneralProjectID}, draft)
	}
	if first.ID == second.ID {
		t.Fatal("plus reused another draft")
	}
	config := preparationConfig(*first.Preparation)
	config.Branch = "unfinished branch name"
	rpc(protocol.Request{Operation: "session-config", Session: first.ID, Name: "Find me later", Preparation: &config}, &first)
	var file protocol.Attachment
	rpc(protocol.Request{Operation: "composer-attach", Session: first.ID, Upload: &protocol.Upload{Name: "note.txt", Data: []byte("durable attachment")}}, &file)
	rpc(protocol.Request{Operation: "composer-draft", Session: first.ID, Draft: &protocol.Draft{Text: "first unsent message", Attachments: []int64{file.ID}}}, nil)
	rpc(protocol.Request{Operation: "composer-draft", Session: second.ID, Draft: &protocol.Draft{Text: "second unsent message"}}, nil)
	rpc(protocol.Request{Operation: "session-archive", Session: first.ID, Archived: true}, nil)
	if sessions := sessionInfos(t, 100); len(sessions) != 1 || sessions[0].ID != second.ID || sessions[0].Status != "draft" {
		t.Fatal("archiving affected another draft", sessions)
	}
	blocked := openSessionView(t, protocol.Request{Operation: "session-start", Session: first.ID, Revision: first.Preparation.Revision, Size: protocol.Size{Rows: 24, Cols: 80}})
	if message := <-blocked.done; message.Type != "error" || !strings.Contains(message.Error, "restore") {
		t.Fatal("started archived draft", message)
	}
	if output, err := testCommand("server", "restart").CombinedOutput(); err != nil {
		t.Fatalf("restart: %s %v", output, err)
	}
	var found []protocol.SessionInfo
	rpc(protocol.Request{Operation: "session-list", Archived: true, Query: "FIND ME"}, &found)
	if len(found) != 1 || found[0].ID != first.ID || !found[0].Archived || found[0].Status != "draft" {
		t.Fatal("archived draft missing after restart", found)
	}
	var restored history.Session
	rpc(protocol.Request{Operation: "session-config", Session: first.ID}, &restored)
	if !restored.Archived || restored.Title != first.Title || restored.Run != nil || *restored.Preparation != *first.Preparation {
		t.Fatal("lost archived preparation", restored)
	}
	var composer protocol.Composer
	rpc(protocol.Request{Operation: "composer-draft", Session: first.ID}, &composer)
	if composer.Draft.Text != "first unsent message" || len(composer.Attachments) != 1 || composer.Attachments[0].ID != file.ID {
		t.Fatal("lost archived message", composer)
	}
	if data, err := os.ReadFile(composer.Attachments[0].Path); err != nil || string(data) != "durable attachment" {
		t.Fatal("lost attachment bytes", string(data), err)
	}
	rpc(protocol.Request{Operation: "session-archive", Session: first.ID, Archived: false}, nil)
	rpc(protocol.Request{Operation: "session-config", Session: first.ID}, &restored)
	if restored.Archived || restored.Run != nil {
		t.Fatal("restore launched a process", restored)
	}
	view := openSessionView(t, protocol.Request{Operation: "session-start", Session: first.ID, Revision: first.Preparation.Revision, Size: protocol.Size{Rows: 24, Cols: 80}})
	waitFor(t, func() bool { return strings.Contains(view.text(), "BONBON_SHELL_PROMPT> ") })
	if _, err := target.Call(protocol.Request{Operation: "session-archive", Session: first.ID, Archived: true}); err == nil {
		t.Fatal("archived active session")
	}
	if _, err := view.Write([]byte("printf 'ARCHIVE_%s\\n' evidence\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(view.text(), "ARCHIVE_evidence") })
	rpc(protocol.Request{Operation: "session-stop", Session: first.ID}, nil)
	rpc(protocol.Request{Operation: "session-archive", Session: first.ID, Archived: true}, nil)
	replay := resumeView(t, first.ID)
	waitFor(t, func() bool { return strings.Contains(replay.text(), "ARCHIVE_evidence") })
	rpc(protocol.Request{Operation: "composer-draft", Session: first.ID}, &composer)
	if composer.Draft.Text != "first unsent message" {
		t.Fatal("launch or archive consumed message", composer)
	}
	rpc(protocol.Request{Operation: "composer-draft", Session: second.ID}, &composer)
	if composer.Draft.Text != "second unsent message" {
		t.Fatal("independent message changed", composer)
	}
	rpc(protocol.Request{Operation: "session-archive", Session: first.ID, Archived: false}, nil)
	rpc(protocol.Request{Operation: "session-config", Session: first.ID}, &restored)
	if restored.Run == nil || restored.Run.Status != "stopped" {
		t.Fatal("restore changed finished status", restored)
	}
}
