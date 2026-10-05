package history

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rawDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func execSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func oldDatabase(t *testing.T, version int) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db := rawDatabase(t, path)
	for _, file := range []string{fmt.Sprintf("format-%d.sql", version), "records.sql"} {
		data, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, string(data))
	}
	if version >= 4 {
		execSQL(t, db, "UPDATE sessions SET project_id='project' WHERE id='saved'")
	}
	if version >= 5 {
		execSQL(t, db, `INSERT INTO preparations VALUES('saved',7,'launched','{"toolId":"removed-tool","toolName":"Old tool","command":"old-tool --interactive","base":"main","worktree":true}');
INSERT INTO worktrees VALUES('saved','/missing/worktree','/missing/repo','main','abcdef','saved-branch','ready');`)
	}
	if version >= 6 {
		execSQL(t, db, "UPDATE sessions SET archived=1 WHERE id='saved'")
	}
	return path, db
}

func sqlRows(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// Keep the original column list so added columns do not hide changed old values.
func snapshotDatabase(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	for _, query := range []string{"PRAGMA user_version", "SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY name"} {
		result[query] = sqlRows(t, db, query)
	}
	for _, table := range sqlRows(t, db, "SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name") {
		name := table[0].(string)
		var columns []string
		for _, column := range sqlRows(t, db, "PRAGMA table_info("+name+")") {
			columns = append(columns, column[1].(string))
		}
		query := "SELECT " + strings.Join(columns, ",") + " FROM " + name + " ORDER BY rowid"
		result[query] = sqlRows(t, db, query)
	}
	return result
}

func assertSnapshot(t *testing.T, db *sql.DB, snapshot map[string][][]any, includeSchema bool) {
	t.Helper()
	for query, want := range snapshot {
		if !includeSchema && (query == "PRAGMA user_version" || strings.Contains(query, "FROM sqlite_schema")) {
			continue
		}
		if got := sqlRows(t, db, query); !reflect.DeepEqual(got, want) {
			t.Fatalf("changed %s:\ngot  %#v\nwant %#v", query, got, want)
		}
	}
}

func TestMigrationsPreserveReleasedArchives(t *testing.T) {
	for _, version := range []int{3, 4, 5, 6} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path, db := oldDatabase(t, version)
			before := snapshotDatabase(t, db)
			db.Close()
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			assertSnapshot(t, store.db, before, version == schemaVersion)
			if got := sqlRows(t, store.db, "PRAGMA user_version"); got[0][0] != int64(schemaVersion) {
				t.Fatal("wrong version", got)
			}
			settings, err := store.Settings()
			if err != nil || (version < 5 && !reflect.DeepEqual(settings, Settings{Base: "HEAD", Tools: []Tool{}})) {
				t.Fatal("invalid migrated settings", settings, err)
			}
			session, err := store.Session("saved")
			if err != nil || session.Workspace != "/missing/workspace" || session.Run.Status != "exited" {
				t.Fatal("cannot read old session", session, err)
			}
			if version == 3 && session.ProjectID != "" {
				t.Fatal("assigned an invented project", session)
			}
			if version >= 4 && session.ProjectID != "project" {
				t.Fatal("lost project membership", session)
			}
			if session.Archived != (version >= 6) {
				t.Fatal("changed archive state", session)
			}
			if version == 5 && (session.Preparation.Command != "old-tool --interactive" || session.Worktree.Commit != "abcdef") {
				t.Fatal("lost launch metadata", session)
			}
			result, err := store.Query(context.Background(), "SELECT seq FROM events WHERE text LIKE '%café%'")
			if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(43) {
				t.Fatal("old evidence is not searchable", result, err)
			}
			// The deleted highest sequence must never be reused after migration.
			event, err := store.Append("saved", "finished", "output", []byte("new"), "new")
			if err != nil || event.Seq != 901 {
				t.Fatal("lost event sequence high-water mark", event, err)
			}
			after := snapshotDatabase(t, store.db)
			store.Close()
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			assertSnapshot(t, reopened.db, after, true)
		})
	}
}

func TestMigrationFailureRollsBackEntireUpgrade(t *testing.T) {
	for _, test := range []struct {
		name, damage, message string
	}{
		{"later-script", "CREATE TABLE worktrees (original TEXT); INSERT INTO worktrees VALUES('keep')", "apply history migration 5"},
		{"latest-script", "ALTER TABLE sessions ADD COLUMN archived INTEGER", "apply history migration 6"},
		{"schema-validation", "ALTER TABLE events RENAME COLUMN data TO evidence", "validate history schema"},
		{"foreign-keys", "PRAGMA foreign_keys=OFF; UPDATE runs SET session_id='missing' WHERE id='finished'", "invalid foreign keys"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, db := oldDatabase(t, 3)
			execSQL(t, db, test.damage)
			before := snapshotDatabase(t, db)
			db.Close()
			for range 2 {
				store, err := Open(path)
				if err == nil {
					store.Close()
					t.Fatal("accepted invalid archive")
				}
				if !strings.Contains(err.Error(), test.message) {
					t.Fatal(err)
				}
				db = rawDatabase(t, path)
				assertSnapshot(t, db, before, true)
				db.Close()
			}
		})
	}
}

func TestUnsupportedDatabasesRemainUnchanged(t *testing.T) {
	for _, test := range []struct {
		name, schema, message string
	}{
		{"prototype", "CREATE TABLE evidence (data BLOB); INSERT INTO evidence VALUES(X'0001FF'); PRAGMA user_version=2", "unsupported history format 2"},
		{"future", fmt.Sprintf("CREATE TABLE evidence (data BLOB); INSERT INTO evidence VALUES(X'0001FF'); PRAGMA user_version=%d", schemaVersion+1), "use a newer BonBon executable"},
		{"unversioned", "CREATE TABLE evidence (data BLOB); INSERT INTO evidence VALUES(X'0001FF')", "unrecognized unversioned"},
		{"view-only", "CREATE VIEW evidence AS SELECT 1", "unrecognized unversioned"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.sqlite")
			db := rawDatabase(t, path)
			execSQL(t, db, test.schema)
			before := snapshotDatabase(t, db)
			db.Close()
			store, err := Open(path)
			if err == nil {
				store.Close()
				t.Fatal("accepted unsupported database")
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatal(err)
			}
			assertSnapshot(t, rawDatabase(t, path), before, true)
		})
	}
}

func TestConcurrentUpgrade(t *testing.T) {
	path, db := oldDatabase(t, 3)
	before := snapshotDatabase(t, db)
	db.Close()
	start := make(chan struct{})
	results := make(chan error, 4)
	for range cap(results) {
		go func() {
			<-start
			store, err := Open(path)
			if err == nil {
				err = store.Close()
			}
			results <- err
		}()
	}
	close(start)
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	db = rawDatabase(t, path)
	assertSnapshot(t, db, before, false)
	if got := sqlRows(t, db, "PRAGMA user_version"); got[0][0] != int64(schemaVersion) {
		t.Fatal("upgrade was not committed", got)
	}
}
