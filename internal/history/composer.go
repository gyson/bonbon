package history

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
)

// ErrDraftSent reports that another view started a send after the base revision of a
// save. The save must not replace that send or bring back the sent text.
var ErrDraftSent = errors.New("a message was sent from another view after this draft loaded")

// LatestDraft and SaveDraft keep immutable draft revisions in the event archive. Drafts
// have no run ID. Do not treat them as sent messages or replay input. Only a pending
// revision records a send attempt. See SentDrafts.
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
		if pending(data) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func pending(data []byte) bool {
	var draft struct {
		Pending bool `json:"pending"`
	}
	return json.Unmarshal(data, &draft) == nil && draft.Pending
}

// SentDraft is a pending draft revision. The message editor saves one before it writes
// a message to the terminal. Next holds the draft revision after it, or nil.
type SentDraft struct {
	Seq     int64
	Created string
	Data    []byte
	Next    []byte
}

// SentDrafts returns up to limit pending draft revisions older than revision before,
// newest first. A before value of zero starts at the latest revision. More reports
// that older pending revisions exist.
func (s *Store) SentDrafts(sid string, before int64, limit int) (sent []SentDraft, more bool, err error) {
	if before <= 0 {
		before = math.MaxInt64
	}
	// The store uses one connection. Read the first next revision before the scan.
	var next []byte
	err = s.db.QueryRow("SELECT data FROM events WHERE session_id=? AND kind='draft' AND seq>=? ORDER BY seq LIMIT 1", sid, before).Scan(&next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	rows, err := s.db.Query("SELECT seq,created,data FROM events WHERE session_id=? AND kind='draft' AND seq<? ORDER BY seq DESC", sid, before)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		draft := SentDraft{Next: next}
		if err = rows.Scan(&draft.Seq, &draft.Created, &draft.Data); err != nil {
			return nil, false, err
		}
		if pending(draft.Data) {
			if len(sent) == limit {
				return sent, true, nil
			}
			sent = append(sent, draft)
		}
		next = draft.Data
	}
	return sent, false, rows.Err()
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
