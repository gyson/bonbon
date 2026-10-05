-- Baseline: format 3 from BonBon v0.0.1. Used only for an empty database.
CREATE TABLE sessions (
    id TEXT PRIMARY KEY, title TEXT NOT NULL,
    workspace TEXT NOT NULL, created TEXT NOT NULL
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
CREATE INDEX events_session ON events(session_id,seq);
