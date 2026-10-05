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
