package server

import (
	"encoding/json"
	"errors"
	"math"

	"bonbon/internal/protocol"
	"bonbon/internal/terminal"
)

// The final view is a derived cache in SQLite. It cannot resume an emulator or process.
// Original output and sizes remain the authoritative record.
type savedScreen struct {
	Format int            `json:"format"`
	View   *terminal.View `json:"view"`
}

func (s *Server) saveScreen(r *runningSession) error {
	r.mu.Lock()
	view, runID := r.terminal.View(), r.runID
	r.mu.Unlock()
	data, err := json.Marshal(savedScreen{Format: 1, View: view})
	if err != nil {
		return err
	}
	_, err = s.store.Append(r.id, runID, "terminal-screen", data, "")
	return err
}
func (s *Server) loadScreen(id string) (*terminal.View, error) {
	data, err := s.store.LastEventData(id, "terminal-screen")
	if err != nil {
		return nil, err
	}
	if len(data) > 0 {
		var saved savedScreen
		if err = json.Unmarshal(data, &saved); err != nil {
			return nil, err
		}
		if saved.Format != 1 || saved.View == nil || !terminal.ValidSize(saved.View.Size) || len(saved.View.Lines) != int(saved.View.Size.Rows) || len(saved.View.History) > terminal.Scrollback {
			return nil, errors.New("unsupported saved terminal screen")
		}
		return saved.View, nil
	}
	// The server emulates interrupted recordings and recordings without a final cache. It
	// sends no historical query replies or input to a process or client.
	state := terminal.New(protocol.Size{Cols: 80, Rows: 24})
	var after int64
	for {
		events, err := s.store.TerminalPage(id, after, math.MaxInt64)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			return state.View(), nil
		}
		for _, event := range events {
			if _, err = state.Apply(event); err != nil {
				return nil, err
			}
			after = event.Seq
		}
	}
}
