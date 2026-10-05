package history

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const schemaVersion = 6

const (
	sessionColumns = "id,title,workspace,created,COALESCE(project_id,''),archived"
	runColumns     = "id,session_id,status,started,ended,pid,detail"
	eventColumns   = "seq,session_id,run_id,kind,data,text,created"
)

var ErrNotFound = errors.New("session not found")

type Session struct {
	Archived    bool         `json:"archived"`
	ProjectID   string       `json:"projectId"`
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Workspace   string       `json:"workspace"`
	Created     string       `json:"created"`
	Run         *Run         `json:"run,omitempty"`
	Preparation *Preparation `json:"preparation,omitempty"`
	Worktree    *Worktree    `json:"worktree,omitempty"`
}

type Run struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Started   string `json:"started"`
	Ended     string `json:"ended"`
	PID       int    `json:"pid"`
	Detail    string `json:"detail"`
}

type Event struct {
	Seq       int64  `json:"seq"`
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
	Kind      string `json:"kind"`
	Data      []byte `json:"data"`
	Text      string `json:"text"`
	Created   string `json:"created"`
}

type Store struct {
	db   *sql.DB
	Path string
}

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Set private permissions before SQLite creates its database and WAL.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = initialize(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, Path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func initialize(db *sql.DB) error {
	// Serialize initialization so simultaneous first runs see one complete schema.
	if _, err := db.Exec("PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin history initialization: %w", err)
	}
	defer db.Exec("ROLLBACK")
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != schemaVersion {
		return fmt.Errorf("unsupported history format %d; this build requires %d; use a new --dir", version, schemaVersion)
	}
	if version == 0 {
		var tables int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return errors.New("unrecognized history database; use a new --dir")
		}
	}
	if version == 0 {
		_, err := db.Exec(fmt.Sprintf(`CREATE TABLE projects (
                id TEXT PRIMARY KEY, name TEXT NOT NULL, workspace TEXT NOT NULL UNIQUE, created TEXT NOT NULL
            );
            CREATE TABLE sessions (
                id TEXT PRIMARY KEY, title TEXT NOT NULL,
                workspace TEXT NOT NULL, created TEXT NOT NULL,
                project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
                archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1))
            );
            CREATE TABLE runs (
                id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id),
                status TEXT NOT NULL, started TEXT NOT NULL, ended TEXT NOT NULL DEFAULT '',
                pid INTEGER NOT NULL DEFAULT 0, detail TEXT NOT NULL DEFAULT ''
            );
            CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, data TEXT NOT NULL);
            INSERT INTO settings VALUES(1,0,'{"defaultTool":"","worktree":false,"base":"HEAD","tools":[]}');
            CREATE TABLE preparations (
                session_id TEXT PRIMARY KEY REFERENCES sessions(id), revision INTEGER NOT NULL,
                state TEXT NOT NULL, data TEXT NOT NULL
            );
            CREATE TABLE worktrees (
                session_id TEXT PRIMARY KEY REFERENCES sessions(id), path TEXT NOT NULL UNIQUE,
                repository TEXT NOT NULL, base TEXT NOT NULL, commit_id TEXT NOT NULL,
                branch TEXT NOT NULL, state TEXT NOT NULL
            );
            CREATE TABLE events (
                seq INTEGER PRIMARY KEY AUTOINCREMENT,
                session_id TEXT NOT NULL REFERENCES sessions(id), run_id TEXT NOT NULL,
                kind TEXT NOT NULL, data BLOB NOT NULL,
                text TEXT NOT NULL DEFAULT '', created TEXT NOT NULL
            );
            CREATE INDEX sessions_project ON sessions(project_id);
            CREATE INDEX events_session ON events(session_id,seq);
            PRAGMA user_version=%d;`, schemaVersion))
		if err != nil {
			return err
		}
	}
	if err := checkSchema(db); err != nil {
		return err
	}
	if _, err := db.Exec("COMMIT"); err != nil {
		return fmt.Errorf("commit history schema: %w", err)
	}
	return enableWAL(db)
}

func enableWAL(db *sql.DB) error {
	// Changing journal mode needs an exclusive lock. SQLite can return BUSY
	// without invoking its busy handler when concurrent openers upgrade locks.
	// Retry only this idempotent setting, after committing the schema.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
		if err == nil {
			return nil
		}
		if !errors.Is(err, sqlite3.BUSY) {
			return fmt.Errorf("enable history WAL: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("enable history WAL: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func checkSchema(db *sql.DB) error {
	for _, query := range []string{
		"SELECT id,name,workspace,created FROM projects LIMIT 0",
		"SELECT " + sessionColumns + " FROM sessions LIMIT 0",
		"SELECT " + runColumns + " FROM runs LIMIT 0",
		"SELECT " + eventColumns + " FROM events LIMIT 0",
		"SELECT revision,data FROM settings LIMIT 0",
		"SELECT session_id,revision,state,data FROM preparations LIMIT 0",
		"SELECT session_id,path,repository,base,commit_id,branch,state FROM worktrees LIMIT 0",
	} {
		rows, err := db.Query(query)
		if err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanSession(row scanner) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.Title, &s.Workspace, &s.Created, &s.ProjectID, &s.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return s, err
}

func scanRun(row scanner) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.SessionID, &r.Status, &r.Started, &r.Ended, &r.PID, &r.Detail)
	return r, err
}
