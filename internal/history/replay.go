package history

// TerminalPage reads original display events from the start of a recording. Never start
// at an arbitrary byte offset. Cursor state and split escape sequences depend on
// earlier output. Release the database between pages to let other history operations
// continue.
func (s *Store) TerminalPage(sessionID string, after, through int64) ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq,kind,data FROM events
		WHERE session_id=? AND seq>? AND seq<=? AND kind IN ('start','resize','output')
		ORDER BY seq LIMIT 64`, sessionID, after, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.Seq, &event.Kind, &event.Data); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
