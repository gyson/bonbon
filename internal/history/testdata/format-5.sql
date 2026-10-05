-- Frozen format 5 schema from 1f4ae62:internal/history/store.go.
-- Keep independent of the migration scripts. Synthetic data only.
CREATE TABLE projects (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, workspace TEXT NOT NULL UNIQUE, created TEXT NOT NULL
);
CREATE TABLE sessions (
    id TEXT PRIMARY KEY, title TEXT NOT NULL,
    workspace TEXT NOT NULL, created TEXT NOT NULL,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL
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
PRAGMA user_version=5;
INSERT INTO projects VALUES ('project','Saved project','/missing/workspace','2026-10-01T00:00:00Z');
UPDATE settings SET revision=8,data='{"defaultTool":"example","worktree":true,"base":"main","tools":[{"id":"example","name":"Example","command":"example --interactive"}]}' WHERE id=1;
