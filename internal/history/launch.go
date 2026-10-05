package history

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Tool struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

type Settings struct {
	Revision    int64  `json:"revision"`
	DefaultTool string `json:"defaultTool"`
	Worktree    bool   `json:"worktree"`
	Base        string `json:"base"`
	Tools       []Tool `json:"tools"`
}

// Preparation snapshots the selected preset. Editing a preset never rewrites a
// prepared or launched session. State is owned by the server.
type Preparation struct {
	Revision int64  `json:"revision"`
	State    string `json:"state"`
	ToolID   string `json:"toolId"`
	ToolName string `json:"toolName"`
	Command  string `json:"command"`
	Worktree bool   `json:"worktree"`
	Base     string `json:"base"`
	Branch   string `json:"branch"`
}

type Worktree struct {
	Path       string `json:"path"`
	Repository string `json:"repository"`
	Base       string `json:"base"`
	Commit     string `json:"commit"`
	Branch     string `json:"branch"`
	State      string `json:"state"`
}

func ValidateCommand(command string) error {
	// One submitted shell line, below common canonical PTY input limits.
	if len(command) > 1000 || !utf8.ValidString(command) || strings.IndexFunc(command, unicode.IsControl) >= 0 {
		return errors.New("startup command must be one line, at most 1000 bytes, without control characters")
	}
	return nil
}

func (s *Store) Settings() (Settings, error) {
	var settings Settings
	var data string
	var revision int64
	err := s.db.QueryRow("SELECT revision,data FROM settings WHERE id=1").Scan(&revision, &data)
	if err == nil {
		err = json.Unmarshal([]byte(data), &settings)
	}
	settings.Revision = revision
	return settings, err
}

func (s *Store) SaveSettings(settings Settings) (Settings, error) {
	if len(settings.Tools) > 100 {
		return settings, errors.New("at most 100 tools are supported")
	}
	seen := map[string]bool{"": true}
	for i := range settings.Tools {
		tool := &settings.Tools[i]
		var err error
		tool.Name, err = validName(tool.Name)
		if err != nil {
			return settings, err
		}
		if tool.ID == "" || len(tool.ID) > 100 || seen[tool.ID] {
			return settings, errors.New("tools need unique IDs")
		}
		seen[tool.ID] = true
		if strings.TrimSpace(tool.Command) == "" {
			return settings, errors.New("tool command is required; use Shell for an empty command")
		}
		if err = ValidateCommand(tool.Command); err != nil {
			return settings, err
		}
	}
	if !seen[settings.DefaultTool] {
		return settings, errors.New("default tool does not exist")
	}
	if len(settings.Base) > 250 || strings.ContainsAny(settings.Base, "\x00\r\n") {
		return settings, errors.New("invalid default Git base")
	}
	if settings.Base == "" {
		settings.Base = "HEAD"
	}
	if settings.Tools == nil {
		settings.Tools = []Tool{}
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return settings, err
	}
	result, err := s.db.Exec("UPDATE settings SET revision=revision+1,data=? WHERE id=1 AND revision=?", string(data), settings.Revision)
	if err = revisionChanged(result, err); err != nil {
		return settings, err
	}
	settings.Revision++
	return settings, nil
}

func revisionChanged(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return errors.New("settings changed or session already started; keep your edits and reload before saving")
	}
	return err
}

func (s *Store) ProjectDraft(project string) (Session, error) {
	var id string
	err := s.db.QueryRow(`SELECT sessions.id FROM sessions JOIN preparations ON session_id=sessions.id
 WHERE project_id=? AND state='draft' ORDER BY sessions.created DESC LIMIT 1`, project).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return s.Session(id)
}

func (s *Store) CreatePreparation(title, workspace, project string, p Preparation) (Session, error) {
	if project == "" {
		return Session{}, errors.New("choose a project before preparing a session")
	}
	var err error
	if title != "" {
		title, err = validName(title)
		if err != nil {
			return Session{}, err
		}
	}
	if err = ValidateCommand(p.Command); err != nil {
		return Session{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	// Reopening a project, including from another tab, resumes its one draft.
	// The single archive connection serializes this lookup and creation.
	var existing string
	err = tx.QueryRow(`SELECT sessions.id FROM sessions JOIN preparations ON session_id=sessions.id
 WHERE project_id=? AND state='draft' ORDER BY sessions.created DESC LIMIT 1`, project).Scan(&existing)
	if err == nil {
		tx.Rollback()
		return s.Session(existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Session{}, err
	}
	session := Session{ID: ID(), Title: title, Workspace: workspace, ProjectID: project, Created: Now()}
	p.Revision, p.State = 1, "draft"
	data, err := json.Marshal(p)
	if err != nil {
		return session, err
	}
	_, err = tx.Exec("INSERT INTO sessions(id,title,workspace,created,project_id) VALUES(?,?,?,?,NULLIF(?,''))", session.ID, title, workspace, session.Created, project)
	if err == nil {
		_, err = tx.Exec("INSERT INTO preparations VALUES(?,1,'draft',?)", session.ID, string(data))
	}
	if err != nil {
		return session, err
	}
	session.Preparation = &p
	return session, tx.Commit()
}

func (s *Store) SavePreparation(id, title string, p Preparation) (Session, error) {
	if len(title) > 800 || !utf8.ValidString(title) || strings.IndexFunc(title, unicode.IsControl) >= 0 {
		return Session{}, errors.New("invalid session name")
	}
	if strings.TrimSpace(title) != "" {
		var err error
		title, err = validName(title)
		if err != nil {
			return Session{}, err
		}
	}
	if err := ValidateCommand(p.Command); err != nil {
		return Session{}, err
	}
	if len(p.ToolName) > 200 || len(p.Base) > 250 || len(p.Branch) > 250 || strings.ContainsAny(p.Base+p.Branch, "\x00\r\n") {
		return Session{}, errors.New("invalid launch settings")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	p.State = "draft"
	data, err := json.Marshal(p)
	if err != nil {
		return Session{}, err
	}
	result, err := tx.Exec("UPDATE preparations SET revision=revision+1,data=? WHERE session_id=? AND revision=? AND state='draft'", string(data), id, p.Revision)
	if err = revisionChanged(result, err); err != nil {
		return Session{}, err
	}
	_, err = tx.Exec("UPDATE sessions SET title=? WHERE id=?", title, id)
	if err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return s.Session(id)
}

func (s *Store) launchMetadata(session *Session) error {
	var data, state string
	var revision int64
	err := s.db.QueryRow("SELECT revision,state,data FROM preparations WHERE session_id=?", session.ID).Scan(&revision, &state, &data)
	if err == nil {
		var p Preparation
		if err = json.Unmarshal([]byte(data), &p); err != nil {
			return err
		}
		p.Revision, p.State = revision, state
		session.Preparation = &p
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var w Worktree
	err = s.db.QueryRow("SELECT path,repository,base,commit_id,branch,state FROM worktrees WHERE session_id=?", session.ID).Scan(&w.Path, &w.Repository, &w.Base, &w.Commit, &w.Branch, &w.State)
	if err == nil {
		session.Worktree = &w
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func (s *Store) SetPreparationState(id string, revision int64, from, to string) error {
	result, err := s.db.Exec("UPDATE preparations SET state=? WHERE session_id=? AND revision=? AND state=?", to, id, revision, from)
	return revisionChanged(result, err)
}

func (s *Store) SetLaunchWorkspace(id, title, workspace string) error {
	_, err := s.db.Exec("UPDATE sessions SET title=?,workspace=? WHERE id=?", title, workspace, id)
	return err
}

func (s *Store) SaveWorktree(id string, w Worktree) error {
	_, err := s.db.Exec(`INSERT INTO worktrees VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(session_id) DO UPDATE SET path=excluded.path,repository=excluded.repository,base=excluded.base,commit_id=excluded.commit_id,branch=excluded.branch,state=excluded.state`, id, w.Path, w.Repository, w.Base, w.Commit, w.Branch, w.State)
	return err
}
