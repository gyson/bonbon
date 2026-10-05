package history

import (
	"database/sql"
	"errors"
)

type RecentSession struct {
	Session
	Updated string
}

// RecentSessions orders by terminal input, output, and run lifecycle activity,
// including output while detached. View changes and unsent drafts do not count.
func (s *Store) RecentSessions(limit int) ([]RecentSession, error) {
	rows, err := s.db.Query(`SELECT sessions.id,title,workspace,sessions.created,COALESCE(project_id,''),
        COALESCE(activity.created,sessions.created) AS updated
        FROM sessions LEFT JOIN events AS activity ON activity.seq=(
            SELECT seq FROM events WHERE session_id=sessions.id
            AND kind IN ('start','run','input','output','notice') ORDER BY seq DESC LIMIT 1
        )
        WHERE project_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM preparations WHERE session_id=sessions.id AND state='draft'
        )
        ORDER BY julianday(updated) DESC,COALESCE(activity.seq,0) DESC,sessions.id
        LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	result := []RecentSession{}
	for rows.Next() {
		var item RecentSession
		if err = rows.Scan(&item.ID, &item.Title, &item.Workspace, &item.Created, &item.ProjectID, &item.Updated); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range result {
		if err = s.launchMetadata(&result[i].Session); err != nil {
			return nil, err
		}
		result[i].Run, err = s.LatestRun(result[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) Session(id string) (Session, error) {
	session, err := scanSession(s.db.QueryRow("SELECT "+sessionColumns+" FROM sessions WHERE id=?", id))
	if err == nil {
		err = s.launchMetadata(&session)
	}
	if err == nil {
		session.Run, err = s.LatestRun(id)
	}
	return session, err
}

func (s *Store) LatestRun(id string) (*Run, error) {
	r, err := scanRun(s.db.QueryRow("SELECT "+runColumns+" FROM runs WHERE session_id=? ORDER BY rowid DESC LIMIT 1", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &r, err
}

func (s *Store) CreateSession(title, workspace, projectID string) (Session, error) {
	session := Session{ID: ID(), Title: title, Workspace: workspace, Created: Now(), ProjectID: projectID}
	_, err := s.db.Exec("INSERT INTO sessions(id,title,workspace,created,project_id) VALUES(?,?,?,?,NULLIF(?,''))", session.ID, session.Title, session.Workspace, session.Created, projectID)
	return session, err
}

func (s *Store) NewRun(sid string) (*Run, error) {
	r := &Run{ID: ID(), SessionID: sid, Status: "starting", Started: Now()}
	_, err := s.db.Exec("INSERT INTO runs(id,session_id,status,started) VALUES(?,?,?,?)", r.ID, sid, r.Status, r.Started)
	return r, err
}

func (s *Store) SaveRun(r *Run) error {
	_, err := s.db.Exec("UPDATE runs SET status=?,ended=?,pid=?,detail=? WHERE id=?", r.Status, r.Ended, r.PID, r.Detail, r.ID)
	return err
}

// MarkInterrupted runs once at server startup, before accepting session requests.
// Missing exits remain unknown; this changes history metadata, not process state.
func (s *Store) MarkInterrupted() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	if _, err = tx.Exec("UPDATE preparations SET state='interrupted' WHERE state IN ('creating','launched') AND session_id NOT IN (SELECT session_id FROM runs)"); err != nil {
		return err
	}
	detail := "Run has no recorded exit. Process state is unknown."
	if _, err = tx.Exec(`INSERT INTO events(session_id,run_id,kind,data,text,created)
        SELECT session_id,id,'notice',?,'',? FROM runs WHERE status IN ('starting','running')`, []byte(detail), now); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE runs SET status='interrupted',ended=?,detail=?
        WHERE status IN ('starting','running')`, now, detail); err != nil {
		return err
	}
	return tx.Commit()
}
