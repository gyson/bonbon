package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQuerySQLControlsScopeAndPreservesValueTypes(t *testing.T) {
	s := testStore(t)
	first, second := create(t, s), create(t, s)
	appendText(t, s, first.ID, "blueberry café")
	appendText(t, s, second.ID, "strawberry café")
	result, err := s.Query(context.Background(), "SELECT session_id,text FROM events ORDER BY seq")
	if err != nil || len(result.Rows) != 2 {
		t.Fatal(result, err)
	}
	result, err = s.Query(context.Background(), fmt.Sprintf("SELECT text FROM events WHERE session_id='%s' AND text LIKE '%%café%%'", first.ID))
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "blueberry café" {
		t.Fatal(result, err)
	}
	result, err = s.Query(context.Background(), "WITH n(x) AS (VALUES(7)) SELECT x AS same, 'a;b' AS same, x'00ff' AS bytes, NULL AS empty, 1.5 AS real FROM n; -- end")
	if err != nil || !reflect.DeepEqual(result.Columns, []string{"same", "same", "bytes", "empty", "real"}) {
		t.Fatal(result, err)
	}
	expected := []any{int64(7), "a;b", []byte{0, 255}, nil, 1.5}
	if !reflect.DeepEqual(result.Rows[0], expected) {
		t.Fatal(result.Rows)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), `"AP8="`) || !strings.Contains(string(encoded), "null") {
		t.Fatal(string(encoded), err)
	}
}

func TestQueryExplicitScopeAndPagination(t *testing.T) {
	s := testStore(t)
	first, second := create(t, s), create(t, s)
	own := appendText(t, s, first.ID, "first")
	outside := appendText(t, s, second.ID, "other")
	later := appendText(t, s, first.ID, "later")
	result, err := s.Query(context.Background(), fmt.Sprintf("SELECT seq FROM events WHERE session_id='%s' AND seq>%d ORDER BY seq LIMIT 1", first.ID, own.Seq))
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != later.Seq {
		t.Fatal(result, err)
	}
	result, err = s.Query(context.Background(), fmt.Sprintf("SELECT seq FROM events WHERE session_id='%s' AND seq=%d", first.ID, outside.Seq))
	if err != nil || len(result.Rows) != 0 {
		t.Fatal("ignored explicit scope", result, err)
	}
}

func TestQueryRejectsWritesSettingsAttachmentsAndMultipleStatements(t *testing.T) {
	s := testStore(t)
	session := create(t, s)
	appendText(t, s, session.ID, "original evidence")
	outside := filepath.Join(t.TempDir(), "must-not-exist.sqlite")
	for _, statement := range []string{
		"UPDATE sessions SET title='changed'",
		"DELETE FROM events RETURNING seq",
		"INSERT INTO sessions SELECT * FROM sessions",
		"CREATE TABLE changed(x)", "CREATE TEMP TABLE changed(x)", "DROP TABLE events",
		"WITH q AS (SELECT 1) UPDATE sessions SET title='changed'",
		"BEGIN", "COMMIT", "SAVEPOINT x", "PRAGMA table_info(events)", "PRAGMA query_only=OFF", "PRAGMA writable_schema=ON", "PRAGMA journal_mode=DELETE",
		"ATTACH DATABASE '" + outside + "' AS other", "VACUUM INTO '" + outside + "'",
		"SELECT 1; UPDATE sessions SET title='changed'", "SELECT 1); DELETE FROM events; --",
		"SELECT 1; PRAGMA query_only=OFF; DELETE FROM events", "SELECT 1; SELECT 2", "SELECT 1;; -- comment\n DELETE FROM events",
		"SELECT load_extension('" + outside + "')", "SELECT writefile('" + outside + "','changed')",
		"SELECT * FROM pragma_query_only(0)", "SELECT * FROM pragma_table_info('events')", "", ";", "-- comment", "SELECT 'unfinished",
		"SELECT 1\x00; DELETE FROM events",
	} {
		t.Run(statement, func(t *testing.T) {
			if result, err := s.Query(context.Background(), statement); err == nil {
				t.Fatalf("accepted unsafe/invalid query: %+v", result)
			}
		})
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created external file: %v", err)
	}
	got, err := s.Session(session.ID)
	if err != nil || got.Title != session.Title {
		t.Fatal("modified session", got, err)
	}
	result, err := s.Query(context.Background(), "SELECT text FROM events")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "original evidence" {
		t.Fatal("modified history", result, err)
	}
	// Reader settings must never affect the writer used for terminal capture.
	appendText(t, s, session.ID, "recording still works")
}

func TestQueryAllowsQuotesCommentsAndSchemaInspection(t *testing.T) {
	s := testStore(t)
	for _, statement := range []string{
		"/* prefix ; */ SELECT '; -- /*' AS [a;b]; /* suffix */",
		"SELECT 'it''s;safe', 1 AS \"a;\"\"b\", 2 AS `x;``y` -- comment ;\n;",
		"SELECT name,sql FROM sqlite_schema WHERE type='table'",
		"VALUES(1),(2)",
	} {
		if _, err := s.Query(context.Background(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func TestQueryLimitsRowsValuesAndResponseBytes(t *testing.T) {
	s := testStore(t)
	result, err := s.Query(context.Background(), "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1100) SELECT x FROM n")
	if err != nil || !result.Truncated || len(result.Rows) != queryRows {
		t.Fatal(len(result.Rows), result.Truncated, err)
	}
	result, err = s.Query(context.Background(), "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000) SELECT printf('%020000d',x) FROM n")
	if err != nil || !result.Truncated || len(result.Rows) >= queryRows {
		t.Fatal(len(result.Rows), result.Truncated, err)
	}
	if _, err = s.Query(context.Background(), "SELECT zeroblob(2000000)"); err == nil {
		t.Fatal("unbounded value")
	}
	if _, err = s.Query(context.Background(), "SELECT 1"+strings.Repeat(" ", querySQLBytes)); err == nil {
		t.Fatal("unbounded SQL")
	}
}

func TestSlowQueryCanBeCancelledWithoutBlockingCapture(t *testing.T) {
	s := testStore(t)
	session := create(t, s)
	for _, statement := range []string{
		"WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n) SELECT sum(x) FROM n",
		"SELECT 1 UNION ALL SELECT (WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n) SELECT sum(x) FROM n)",
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		done := make(chan error, 1)
		go func() { _, err := s.Query(ctx, statement); done <- err }()
		// An expensive query, including one that stalls after its first row,
		// must neither block capture nor outlive the caller's deadline.
		appendText(t, s, session.ID, "captured during query")
		select {
		case err := <-done:
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("expected deadline", err)
			}
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("query did not stop")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Query(ctx, "SELECT 1"); !errors.Is(err, context.Canceled) {
		t.Fatal("already cancelled query", err)
	}
}
