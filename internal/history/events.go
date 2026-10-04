package history

func (s *Store) Append(sid, rid, kind string, data []byte, text string) (Event, error) {
	if data == nil {
		data = []byte{}
	}
	e := Event{SessionID: sid, RunID: rid, Kind: kind, Data: data, Text: text, Created: Now()}
	result, err := s.db.Exec("INSERT INTO events(session_id,run_id,kind,data,text,created) VALUES(?,?,?,?,?,?)", sid, rid, kind, data, text, e.Created)
	if err != nil {
		return e, err
	}
	e.Seq, err = result.LastInsertId()
	return e, err
}

func (s *Store) LastEventData(session, kind string) ([]byte, error) {
	rows, err := s.db.Query("SELECT data FROM events WHERE session_id=? AND kind=? ORDER BY seq DESC LIMIT 1", session, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var data []byte
	if rows.Next() {
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
	}
	return data, rows.Err()
}
