-- Frozen format 4 schema from 4bbe15d:internal/history/store.go.
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
CREATE TABLE events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id), run_id TEXT NOT NULL,
    kind TEXT NOT NULL, data BLOB NOT NULL,
    text TEXT NOT NULL DEFAULT '', created TEXT NOT NULL
);
CREATE INDEX sessions_project ON sessions(project_id);
CREATE INDEX events_session ON events(session_id,seq);
PRAGMA user_version=4;
INSERT INTO projects VALUES ('project','Saved project','/missing/workspace','2026-10-01T00:00:00Z');
