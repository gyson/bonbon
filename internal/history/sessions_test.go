package history

import (
	"path/filepath"
	"testing"
)

func TestRecentSessionsUseTerminalActivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			store.Close()
		}
	})
	older, err := store.CreateSession("older", "/tmp", "")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := store.CreateSession("newer", "/tmp", "")
	if err != nil {
		t.Fatal(err)
	}
	const early = "2026-01-01T00:00:00Z"
	const later = "2026-01-02T00:00:00Z"
	const activity = "2026-01-03T00:00:00Z"
	const ignored = "2026-01-04T00:00:00Z"
	for id, created := range map[string]string{older.ID: early, newer.ID: later} {
		if _, err = store.db.Exec("UPDATE sessions SET created=? WHERE id=?", created, id); err != nil {
			t.Fatal(err)
		}
	}
	appendAt := func(id, kind, created string) {
		t.Helper()
		event, err := store.Append(id, "", kind, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec("UPDATE events SET created=? WHERE seq=?", created, event.Seq); err != nil {
			t.Fatal(err)
		}
	}
	assertRecent := func(id, updated string) {
		t.Helper()
		items, err := store.ListSessions(SessionFilter{Limit: 1})
		if err != nil || len(items) != 1 || items[0].ID != id || items[0].Updated != updated {
			t.Fatalf("recent = %+v, %v; want %s at %s", items, err, id, updated)
		}
	}
	// Sessions with no terminal events use creation time, even with newer drafts.
	appendAt(older.ID, "draft", ignored)
	assertRecent(newer.ID, later)
	appendAt(older.ID, "output", activity)
	appendAt(newer.ID, "output", activity)
	assertRecent(newer.ID, activity)
	for _, kind := range []string{"resize", "draft", "attachment", "terminal-response", "terminal-screen"} {
		appendAt(older.ID, kind, ignored)
		assertRecent(newer.ID, activity)
	}
	if err = store.RenameSession(older.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	assertRecent(newer.ID, activity)
	// Only qualifying event sequences determine the order when timestamps are equal. This
	// includes input and lifecycle events without associated output.
	for _, kind := range []string{"start", "run", "input", "output", "notice"} {
		appendAt(older.ID, kind, activity)
		assertRecent(older.ID, activity)
		appendAt(newer.ID, "output", activity)
		assertRecent(newer.ID, activity)
	}
	appendAt(older.ID, "draft", ignored)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	assertRecent(newer.ID, activity)
}
