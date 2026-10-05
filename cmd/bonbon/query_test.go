package main

import (
	"encoding/json"
	"strings"
	"testing"

	"bonbon/internal/history"
)

func TestQueryCLISelectsExplicitScopeAndRejectsWrites(t *testing.T) {
	directory := t.TempDir()
	startTestServer(t, directory)
	store := archive(t, directory)
	first, _ := store.CreateSession("first", t.TempDir(), "")
	store.CreateSession("second", t.TempDir(), "")
	t.Setenv("BONBON_SESSION", first.ID)
	output, err := testCommand("query", "SELECT title FROM sessions ORDER BY title").CombinedOutput()
	var result history.QueryResult
	if err != nil || json.Unmarshal(output, &result) != nil || len(result.Rows) != 2 {
		t.Fatalf("query: %s %v", output, err)
	}
	if output, err = testCommand("query", "DELETE FROM sessions").CombinedOutput(); err == nil || !strings.Contains(string(output), "read-only query") {
		t.Fatalf("accepted write: %s %v", output, err)
	}
	for _, args := range [][]string{{"history", "list"}, {"history", "search", "text"}, {"history", "read", "1"}} {
		if output, err = testCommand(args...).CombinedOutput(); err == nil || !strings.Contains(string(output), "unknown command") {
			t.Fatalf("legacy command still exists: %s %v", output, err)
		}
	}
}
