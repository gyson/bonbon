package main

import (
	"encoding/json"
	"strings"
	"testing"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func TestHistorySearchEarlierTerminalsWhileLatestKeepsRunning(t *testing.T) {
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
	start := shellStartRequest(t, t.TempDir(), "History search fixture", protocol.Size{Cols: 80, Rows: 24})
	first := openSessionView(t, start)
	waitFor(t, func() bool { return strings.Contains(first.text(), "BONBON_SHELL_PROMPT> ") })
	if _, err := first.Write([]byte("printf '\\146\\151\\156\\144\\040%s\\n' oldest\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(first.text(), "find oldest") })
	sid := start.Session
	stopTestSession(t, sid)
	var session history.Session
	rpc(protocol.Request{Operation: "session-config", Session: sid}, &session)
	second := openSessionView(t, protocol.Request{Operation: "session-continue", Session: sid, LatestRun: session.Run.ID, Size: protocol.Size{Cols: 80, Rows: 24}})
	waitFor(t, func() bool { return strings.Contains(second.text(), "BONBON_SHELL_PROMPT> ") })
	if _, err := second.Write([]byte("printf '\\146\\151\\156\\144\\040%s\\n' newest\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(second.text(), "find newest") })
	filter := history.SearchFilter{Query: "find ", Session: sid, Limit: 1}
	var firstPage, secondPage history.SearchPage
	rpc(protocol.Request{Operation: "history-search", Search: &filter}, &firstPage)
	if len(firstPage.Matches) != 1 || !firstPage.More || firstPage.Matches[0].Terminal != 1 || !strings.Contains(firstPage.Matches[0].Context.After, "oldest") {
		t.Fatal(firstPage)
	}
	filter.Offset, filter.Through = 1, firstPage.Through
	rpc(protocol.Request{Operation: "history-search", Search: &filter}, &secondPage)
	if len(secondPage.Matches) != 1 || secondPage.More || secondPage.Matches[0].Terminal != 2 || secondPage.Matches[0].Anchor.Seq <= firstPage.Matches[0].Anchor.Seq {
		t.Fatal(secondPage)
	}
	var context history.SearchContext
	rpc(protocol.Request{Operation: "history-context", Context: &history.ContextRequest{Anchor: firstPage.Matches[0].Anchor, Before: 512, After: 512}}, &context)
	if !strings.Contains(context.Match+context.After, "find oldest") || strings.Contains(context.After, "find newest") {
		t.Fatal(context)
	}
	if _, err := second.Write([]byte("printf 'STILL_%s\\n' running\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(second.text(), "STILL_running") })
	stopTestSession(t, sid)
	rpc(protocol.Request{Operation: "session-archive", Session: sid, Archived: true}, nil)
	if output, err := testCommand("server", "restart").CombinedOutput(); err != nil {
		t.Fatalf("restart: %s %v", output, err)
	}
	filter = history.SearchFilter{Query: "find ", Scope: "all"}
	var reopened history.SearchPage
	rpc(protocol.Request{Operation: "history-search", Search: &filter}, &reopened)
	if len(reopened.Matches) != 2 || reopened.Matches[0].Terminal != 1 || reopened.Matches[1].Terminal != 2 {
		t.Fatal(reopened)
	}
}
