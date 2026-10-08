package history

import (
	"database/sql"
	"errors"
)

var ErrDraftConflict = errors.New("draft changed in another view; copy your edits before reloading")

// LatestDraft and SaveDraft keep immutable draft revisions in the event archive. Drafts
// have no run ID. Do not treat them as sent messages or replay input.
func (s *Store) LatestDraft(sid string) (Event, error) {
	var event Event
	err := s.db.QueryRow("SELECT seq,data FROM events WHERE session_id=? AND kind='draft' ORDER BY seq DESC LIMIT 1", sid).Scan(&event.Seq, &event.Data)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return event, err
}

func (s *Store) SaveDraft(sid string, revision int64, data []byte) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRow("SELECT COALESCE(MAX(seq),0) FROM events WHERE session_id=? AND kind='draft'", sid).Scan(&current); err != nil {
		return 0, err
	}
	if current != revision {
		return 0, ErrDraftConflict
	}
	result, err := tx.Exec("INSERT INTO events(session_id,run_id,kind,data,text,created) VALUES(?,'','draft',?,'',?)", sid, data, Now())
	if err != nil {
		return 0, err
	}
	seq, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}

// Verify attachment scope on every read, including draft references.
func (s *Store) Attachment(sid string, seq int64) (Event, error) {
	var event Event
	err := s.db.QueryRow("SELECT seq,data,text FROM events WHERE session_id=? AND seq=? AND kind='attachment'", sid, seq).Scan(&event.Seq, &event.Data, &event.Text)
	return event, err
}

// Draft autosaves validate references without repeated reads of file blobs.
func (s *Store) AttachmentInfo(sid string, seq int64) (Event, error) {
	var event Event
	err := s.db.QueryRow("SELECT seq,text FROM events WHERE session_id=? AND seq=? AND kind='attachment'", sid, seq).Scan(&event.Seq, &event.Text)
	return event, err
}
