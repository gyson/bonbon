package main

import (
	"encoding/json"
	"testing"
	"time"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func TestDraftWatchSyncsEveryView(t *testing.T) {
	directory := t.TempDir()
	startTestServer(t, directory)
	target := client.Target{Dir: directory}
	call := func(request protocol.Request, result any) {
		t.Helper()
		data, err := target.Call(request)
		if err != nil {
			t.Fatal(request.Operation, err)
		}
		if err = json.Unmarshal(data, result); err != nil {
			t.Fatal(err)
		}
	}
	var session history.Session
	call(protocol.Request{Operation: "project-draft", Project: history.GeneralProjectID}, &session)
	read := func(conn *protocol.Conn) protocol.Message {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		m, err := conn.Receive()
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	views := make([]*protocol.Conn, 2)
	for i := range views {
		conn, err := target.Connect(protocol.Request{Operation: "composer-watch", Session: session.ID})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		if m := read(conn); m.Type != "draft" || m.Composer == nil || m.Composer.Draft.Revision != 0 {
			t.Fatalf("initial draft: %+v", m)
		}
		views[i] = conn
	}
	var saved protocol.Composer
	call(protocol.Request{Operation: "composer-draft", Session: session.ID, Draft: &protocol.Draft{Text: "typed in one view"}}, &saved)
	for _, view := range views {
		if m := read(view); m.Type != "draft" || m.Composer.Draft.Revision != saved.Draft.Revision || m.Composer.Draft.Text != "typed in one view" {
			t.Fatalf("view did not receive the saved draft: %+v", m)
		}
	}
	// A send from one view reaches the other view. A stale save gets it back instead.
	var sending, stale protocol.Composer
	call(protocol.Request{Operation: "composer-draft", Session: session.ID, Draft: &protocol.Draft{Revision: saved.Draft.Revision, Text: "typed in one view", Pending: true}}, &sending)
	call(protocol.Request{Operation: "composer-draft", Session: session.ID, Draft: &protocol.Draft{Revision: saved.Draft.Revision, Text: "stale edit"}}, &stale)
	if !stale.Rejected || stale.Draft.Revision != sending.Draft.Revision || !stale.Draft.Pending {
		t.Fatalf("stale save replaced a send: %+v", stale)
	}
	for _, view := range views {
		if m := read(view); !m.Composer.Draft.Pending || m.Composer.Draft.Revision != sending.Draft.Revision {
			t.Fatalf("view did not receive the pending draft: %+v", m)
		}
		if err := view.Send(protocol.Message{Type: "ping"}); err != nil {
			t.Fatal(err)
		}
		if m := read(view); m.Type != "pong" {
			t.Fatalf("expected pong after the rejected save: %+v", m)
		}
	}
}
