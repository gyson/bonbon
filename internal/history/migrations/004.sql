-- Existing sessions keep their workspace and remain standalone.
CREATE TABLE projects (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, workspace TEXT NOT NULL UNIQUE, created TEXT NOT NULL
);
ALTER TABLE sessions ADD COLUMN project_id TEXT REFERENCES projects(id) ON DELETE SET NULL;
CREATE INDEX sessions_project ON sessions(project_id);
