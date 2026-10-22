package history

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// ErrDraftSent reports that another view started a send after the base revision of a
// save. The save must not replace that send or bring back the sent text.
var ErrDraftSent = errors.New("a message was sent from another view after this draft loaded")

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

// SaveDraft stores the whole draft as a new revision. The latest save wins, also when
// its base revision is older than the current revision. Earlier revisions stay in the
// archive. A save from an older base is rejected with ErrDraftSent when its base or a
// later revision is pending. A pending revision records a send, and another view has
// already changed the draft after it.
func (s *Store) SaveDraft(sid string, base int64, data []byte) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRow("SELECT COALESCE(MAX(seq),0) FROM events WHERE session_id=? AND kind='draft'", sid).Scan(&current); err != nil {
		return 0, err
	}
	if base < 0 || base > current {
		return 0, errors.New("invalid draft revision")
	}
	if base < current {
		sent, err := sentSince(tx, sid, base)
		if err != nil {
			return 0, err
		}
		if sent {
			return 0, ErrDraftSent
		}
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

func sentSince(tx *sql.Tx, sid string, base int64) (bool, error) {
	rows, err := tx.Query("SELECT data FROM events WHERE session_id=? AND kind='draft' AND seq>=?", sid, base)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return false, err
		}
		var draft struct {
			Pending bool `json:"pending"`
		}
		if json.Unmarshal(data, &draft) == nil && draft.Pending {
			return true, nil
		}
	}
	return false, rows.Err()
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
