package history

import (
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const GeneralProjectID = "general"

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
	Created   string `json:"created"`
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 200 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("use a name of 1–200 characters without control characters")
	}
	return name, nil
}

// General follows the instance location. Existing sessions keep their launch path.
func (s *Store) EnsureGeneral(workspace string) error {
	_, err := s.db.Exec(`INSERT INTO projects(id,name,workspace,created) VALUES(?,'General',?,?)
 ON CONFLICT(id) DO UPDATE SET workspace=excluded.workspace`, GeneralProjectID, workspace, Now())
	return err
}

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id,name,workspace,created FROM projects
 ORDER BY id='general' DESC,name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Workspace, &p.Created); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) Project(id string) (Project, error) {
	var p Project
	err := s.db.QueryRow("SELECT id,name,workspace,created FROM projects WHERE id=?", id).Scan(&p.ID, &p.Name, &p.Workspace, &p.Created)
	if errors.Is(err, sql.ErrNoRows) {
		err = errors.New("project not found")
	}
	return p, err
}

func (s *Store) CreateProject(name, workspace string) (Project, error) {
	name, err := validName(name)
	if err != nil {
		return Project{}, err
	}
	p := Project{ID: ID(), Name: name, Workspace: workspace, Created: Now()}
	_, err = s.db.Exec("INSERT INTO projects(id,name,workspace,created) VALUES(?,?,?,?)", p.ID, p.Name, p.Workspace, p.Created)
	return p, err
}

func changedRow(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return errors.New("item not found")
	}
	return err
}

func (s *Store) RenameProject(id, name string) error {
	if id == GeneralProjectID {
		return errors.New("General is a built-in project and cannot be renamed")
	}
	name, err := validName(name)
	if err != nil {
		return err
	}
	return changedRow(s.db.Exec("UPDATE projects SET name=? WHERE id=?", name, id))
}

// Foreign keys detach sessions without deleting their history or workspace paths.
func (s *Store) RemoveProject(id string) error {
	if id == GeneralProjectID {
		return errors.New("General is a built-in project and cannot be removed")
	}
	return changedRow(s.db.Exec("DELETE FROM projects WHERE id=?", id))
}

func (s *Store) RenameSession(id, title string) error {
	title, err := validName(title)
	if err != nil {
		return err
	}
	return changedRow(s.db.Exec("UPDATE sessions SET title=? WHERE id=?", title, id))
}
