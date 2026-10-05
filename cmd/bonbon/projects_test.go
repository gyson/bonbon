package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func TestProjectsLaunchAndRemovalThroughServer(t *testing.T) {
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
	var projects []history.Project
	rpc(protocol.Request{Operation: "project-list"}, &projects)
	if len(projects) != 1 || projects[0].ID != history.GeneralProjectID {
		t.Fatalf("missing General: %+v", projects)
	}
	general := projects[0]
	if !strings.HasSuffix(general.Workspace, "/workspaces/general") {
		t.Fatal(general)
	}
	var custom history.Project
	rpc(protocol.Request{Operation: "project-add", Name: "Custom", Workspace: t.TempDir()}, &custom)
	var views []*sessionView
	var customID string
	for _, project := range []history.Project{general, custom} {
		view := openSessionView(t, protocol.Request{Operation: "session-new", Run: &protocol.Run{ProjectID: project.ID, Title: project.Name, Size: protocol.Size{Rows: 24, Cols: 80}}})
		views = append(views, view)
		waitFor(t, func() bool { return strings.Contains(view.text(), "BONBON_SHELL_PROMPT> ") })
		var info protocol.SessionInfo
		for _, session := range sessionInfos(t, 100) {
			if session.ProjectID == project.ID {
				info = session
			}
		}
		if info.ID == "" || info.Workspace != project.Workspace {
			t.Fatalf("wrong project session: %+v", info)
		}
		if project.ID == custom.ID {
			customID = info.ID
		}
		var count history.QueryResult
		rpc(protocol.Request{Operation: "query", SQL: fmt.Sprintf("SELECT count(*) FROM events WHERE session_id='%s' AND kind='input'", info.ID)}, &count)
		if count.Rows[0][0] != float64(0) {
			t.Fatal("project launch injected terminal input")
		}
		command := "printf 'WORKSPACE=%s\\n' \"$PWD\"\r"
		if _, err := view.Write([]byte(command)); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool {
			var result history.QueryResult
			rpc(protocol.Request{Operation: "query", SQL: fmt.Sprintf("SELECT text FROM events WHERE session_id='%s' AND kind='output' ORDER BY seq", info.ID)}, &result)
			var text strings.Builder
			for _, row := range result.Rows {
				text.WriteString(row[0].(string))
			}
			return strings.Contains(text.String(), "WORKSPACE="+project.Workspace)
		})
	}
	rpc(protocol.Request{Operation: "project-rename", Project: custom.ID, Name: "Renamed"}, nil)
	rpc(protocol.Request{Operation: "session-rename", Session: customID, Name: "Keep working"}, nil)
	rpc(protocol.Request{Operation: "project-remove", Project: custom.ID}, nil)
	found := false
	for _, info := range sessionInfos(t, 100) {
		if info.ID == customID {
			found = true
			if info.ProjectID != "" || info.Status != "running" || info.Workspace != custom.Workspace || info.Title != "Keep working" {
				t.Fatalf("project removal changed runtime: %+v", info)
			}
		}
	}
	if !found {
		t.Fatal("project removal deleted its session")
	}
	if _, err := views[1].Write([]byte("printf 'still-%s\\n' working\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(views[1].text(), "still-working") })
	// Session creation for the removed project must fail without leaving a new record.
	removed := openSessionView(t, protocol.Request{Operation: "session-new", Run: &protocol.Run{ProjectID: custom.ID, Size: protocol.Size{Rows: 24, Cols: 80}}})
	select {
	case result := <-removed.done:
		if result.Type != "error" {
			t.Fatalf("removed project started: %+v", result)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("removed project launch did not fail")
	}
	var replacement history.Project
	rpc(protocol.Request{Operation: "project-add", Workspace: custom.Workspace}, &replacement)
	if replacement.Name != filepath.Base(custom.Workspace) {
		t.Fatal("folder basename was not the default name")
	}
	if output, err := testCommand("server", "restart").CombinedOutput(); err != nil {
		t.Fatalf("restart: %s %v", output, err)
	}
	rpc(protocol.Request{Operation: "project-list"}, &projects)
	if len(projects) != 2 || projects[0].ID != general.ID || projects[1].ID != replacement.ID {
		t.Fatalf("projects lost across restart: %+v", projects)
	}
}
