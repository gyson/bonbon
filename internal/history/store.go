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

func enableWAL(db *sql.DB) error {
	// A journal mode change needs an exclusive lock. SQLite can return BUSY without its
	// busy handler when concurrent openers upgrade locks. After the schema commits, retry
	// only this idempotent setting.
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

func checkSchema(ctx context.Context, conn *sql.Conn) error {
	for _, query := range []string{
		"SELECT id,name,workspace,created FROM projects LIMIT 0",
		"SELECT " + sessionColumns + " FROM sessions LIMIT 0",
		"SELECT " + runColumns + " FROM runs LIMIT 0",
		"SELECT " + eventColumns + " FROM events LIMIT 0",
		"SELECT revision,data FROM settings LIMIT 0",
		"SELECT session_id,revision,state,data FROM preparations LIMIT 0",
		"SELECT session_id,path,repository,base,commit_id,branch,state FROM worktrees LIMIT 0",
	} {
		rows, err := conn.QueryContext(ctx, query)
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
