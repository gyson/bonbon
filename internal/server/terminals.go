package server

import (
	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

// terminals lists the terminals of a session in start order. A view uses it to show an
// earlier terminal. A new terminal has no run record until its process starts. Until
// then, the list ends with a starting terminal that has no run ID.
func (s *Server) terminals(sid string) ([]protocol.Terminal, error) {
	session, err := s.store.Session(sid)
	if err != nil {
		return nil, err
	}
	runs, err := s.store.RunStarts(sid)
	if err != nil {
		return nil, err
	}
	result := make([]protocol.Terminal, 0, len(runs)+1)
	for i, run := range runs {
		result = append(result, protocol.Terminal{Number: i + 1, Run: run.ID, Tool: history.RunTool(run.Start, i == 0, session.Preparation),
			Status: run.Status, Started: run.Started, Ended: run.Ended})
	}
	if r := s.running(sid); r != nil {
		r.mu.Lock()
		starting := !r.ended && (len(runs) == 0 || endedRun[runs[len(runs)-1].Status])
		r.mu.Unlock()
		if starting {
			result = append(result, protocol.Terminal{Number: len(result) + 1, Tool: r.tool, Status: "starting"})
		}
	}
	return result, nil
}
