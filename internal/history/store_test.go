package history

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func create(t *testing.T, s *Store) Session {
	t.Helper()
	item, err := s.CreateSession("Synthetic", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func appendText(t *testing.T, s *Store, sid, text string) Event {
	t.Helper()
	e, err := s.Append(sid, "fixture", "output", []byte(text), text)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSplitTerminalBytesPreserveDerivedTextAndEvidence(t *testing.T) {
	s := testStore(t)
	session := create(t, s)
	p := &Text{}
	chunks := [][]byte{[]byte("\x1b[3"), []byte("2mHello blue"), []byte("berry \x1b]0;ignore"), []byte(" title\x07"), {0xe4, 0xbd}, {0xa0, 0xe5, 0xa5, 0xbd}, []byte("\x1b[0m\rnext")}
	var all []byte
	var projection string
	for _, data := range chunks {
		all = append(all, data...)
		text := p.Push(data)
		projection += text
		if _, err := s.Append(session.ID, "run", "output", data, text); err != nil {
			t.Fatal(err)
		}
	}
	if projection != "Hello blueberry 你好\nnext" {
		t.Fatalf("wrong projection: %q", projection)
	}
	events, _ := recordedEvents(t, s, session.ID)
	var raw []byte
	for _, e := range events {
		raw = append(raw, e.Data...)
	}
	if !bytes.Equal(all, raw) {
		t.Fatal("raw bytes changed")
	}

}

func TestSQLiteReopenPreservesSessionRunAndEvidence(t *testing.T) {
	store := testStore(t)
	session := create(t, store)
	run, err := store.NewRun(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status, run.Ended, run.Detail = "exited", Now(), "exit status 7"
	if err = store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	output := []byte("\x1b[32msaved café\x1b[0m\r\n")
	parser := &Text{}
	for _, item := range []struct {
		kind string
		raw  []byte
		text string
	}{
		{"start", []byte(`{"command":["/bin/example","--test"],"workspace":"/tmp","cols":80,"rows":24}`), ""},
		{"input", []byte("request\r\x03\x00"), ""},
		{"output", output, parser.Push(output)},
		{"resize", []byte(`{"rows":40,"cols":120}`), ""},
	} {
		if _, err = store.Append(session.ID, run.ID, item.kind, item.raw, item.text); err != nil {
			t.Fatal(err)
		}
	}
	before, err := recordedEvents(t, store, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Session(session.ID)
	session.Run = run
	if err != nil || !reflect.DeepEqual(got, session) {
		t.Fatalf("lost session or run: %+v %v", got, err)
	}
	after, err := recordedEvents(t, reopened, session.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("lost recorded evidence: %+v %v", after, err)
	}
	matches, err := reopened.Query(context.Background(), "SELECT seq FROM events WHERE session_id='"+session.ID+"' AND text LIKE '%saved café%'")
	if err != nil || len(matches.Rows) != 1 || matches.Rows[0][0] != before[2].Seq {
		t.Fatalf("lost search evidence: %+v %v", matches, err)
	}
}

func TestStartupMarksUnfinishedRunsWithoutRepeatingNotices(t *testing.T) {
	s := testStore(t)
	for _, status := range []string{"starting", "running", "exited"} {
		t.Run(status, func(t *testing.T) {
			item := create(t, s)
			run, err := s.NewRun(item.ID)
			if err != nil {
				t.Fatal(err)
			}
			run.Status = status
			if err = s.SaveRun(run); err != nil {
				t.Fatal(err)
			}
			if err = s.MarkInterrupted(); err != nil {
				t.Fatal(err)
			}
			first, err := s.LatestRun(item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status == "exited" {
				if !reflect.DeepEqual(first, run) {
					t.Fatal("changed finished run", first)
				}
			} else if first.Status != "interrupted" || first.Ended == "" || !strings.Contains(first.Detail, "unknown") {
				t.Fatal("lost interrupted state", first)
			}
			if err = s.MarkInterrupted(); err != nil {
				t.Fatal(err)
			}
			second, err := s.LatestRun(item.ID)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatal("startup changed prior state", second, err)
			}
			events, err := recordedEvents(t, s, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status == "exited" {
				if len(events) != 0 {
					t.Fatal("added notice to finished run", events)
				}
			} else if len(events) != 1 || events[0].Kind != "notice" || events[0].RunID != run.ID {
				t.Fatal("wrong interruption notice", events)
			}
		})
	}
}

func TestUnsupportedFormatsAreRejectedWithoutMigration(t *testing.T) {
	for _, version := range []int{schemaVersion - 1, schemaVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			store := testStore(t)
			session := create(t, store)
			if _, err := store.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(store.Path)
			if err == nil {
				reopened.Close()
				t.Fatal("accepted unsupported history format")
			}
			if !strings.Contains(err.Error(), "use a new --dir") {
				t.Fatal(err)
			}
			var actual int
			if err = store.db.QueryRow("PRAGMA user_version").Scan(&actual); err != nil || actual != version {
				t.Fatal("changed unsupported database version", actual, err)
			}
			if _, err = store.Session(session.ID); err != nil {
				t.Fatal("changed unsupported database contents", err)
			}
		})
	}
}

func TestConcurrentFirstOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	start := make(chan struct{})
	results := make(chan error, 4)
	for range cap(results) {
		go func() {
			<-start
			store, err := Open(path)
			if err == nil {
				_, err = store.CreateSession("Concurrent run", "/tmp", "")
				store.Close()
			}
			results <- err
		}()
	}
	close(start)
	for range cap(results) {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sessions, err := store.ListSessions(SessionFilter{Limit: 1000})
	if err != nil || len(sessions) != cap(results) {
		t.Fatal("concurrent initialization lost sessions", sessions, err)
	}
}

// Read evidence through the same SQL path used by the CLI.
func recordedEvents(t *testing.T, store *Store, id string) ([]Event, error) {
	t.Helper()
	result, err := store.Query(context.Background(), "SELECT "+eventColumns+" FROM events WHERE session_id='"+id+"' ORDER BY seq")
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(result.Rows))
	for _, row := range result.Rows {
		events = append(events, Event{Seq: row[0].(int64), SessionID: row[1].(string), RunID: row[2].(string), Kind: row[3].(string), Data: row[4].([]byte), Text: row[5].(string), Created: row[6].(string)})
	}
	return events, nil
}
