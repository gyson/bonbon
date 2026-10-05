package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	agent "bonbon/internal/runtime"
	"bonbon/internal/terminal"
)

// A running session belongs to the server. Attachments are replaceable views;
// their cancellation must never cancel the process or recording.
type runningSession struct {
	id         string
	controls   chan agent.Control
	cancel     context.CancelFunc
	done       chan struct{}
	mu         sync.Mutex
	clients    map[*attachment]struct{}
	controller *attachment
	controlMu  sync.Mutex // Orders accepted input, size changes and control transfers.
	terminal   *terminal.State
	revision   int64
	runID      string
	ended      bool
	exit       protocol.Message
}

type attachment struct {
	conn     *protocol.Conn
	messages chan protocol.Message // Reliable receipts, never terminal output.
	changed  chan struct{}         // Coalesced render demand, not a queue of PTY events.
	closed   chan struct{}
	once     sync.Once
	size     protocol.Size // Display viewport; only the controller resizes the PTY.
}

func (a *attachment) close() { a.once.Do(func() { close(a.closed); a.conn.Close() }) }
func (a *attachment) wake() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

func (r *runningSession) subscribe(conn *protocol.Conn, size protocol.Size) *attachment {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := &attachment{conn: conn, messages: make(chan protocol.Message, 16), changed: make(chan struct{}, 1), closed: make(chan struct{}), size: size}
	if r.clients == nil {
		r.clients = make(map[*attachment]struct{})
	}
	r.clients[a] = struct{}{}
	if r.controller == nil && !r.ended {
		r.controller = a
	}
	a.wake()
	return a
}
func (r *runningSession) leave(a *attachment) {
	r.controlMu.Lock()
	defer r.controlMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, a)
	if r.controller == a {
		r.controller = nil
	}
}
func (a *attachment) inputAck(id string, err error) {
	message := protocol.Message{Type: "input-ack", ID: id}
	if err != nil {
		message.Error = "Input delivery is uncertain: " + err.Error()
	}
	select {
	case <-a.closed:
	case a.messages <- message:
	default:
		a.close()
	}
}

// Serialize accepted controls before changing owners. Already accepted input may
// finish during a handoff; its receipt always belongs to the originating view.
func (r *runningSession) control(a *attachment, message protocol.Message) {
	r.controlMu.Lock()
	defer r.controlMu.Unlock()
	r.mu.Lock()
	_, present := r.clients[a]
	if !present || r.ended {
		r.mu.Unlock()
		return
	}
	if message.Type == "take-control" {
		r.controller = a
		for client := range r.clients {
			client.wake()
		}
		message.Type = "resize"
	}
	if message.Type == "resize" {
		a.size = message.Size
		r.revision++
		a.wake()
	}
	allowed := r.controller == a
	r.mu.Unlock()
	if !allowed {
		if message.Type == "input" && message.ID != "" {
			a.inputAck(message.ID, errors.New("this view does not control the session; input was not sent"))
		}
		return
	}
	control := agent.Control{Message: message}
	if message.Type == "input" && message.ID != "" {
		control.Ack = func(err error) { a.inputAck(message.ID, err) }
	}
	select {
	case r.controls <- control:
	case <-r.done:
	case <-a.closed:
	}
}
func (r *runningSession) publish(event history.Event) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal == nil {
		r.terminal = terminal.New(protocol.Size{Cols: 80, Rows: 24})
	}
	responses, err := r.terminal.Apply(event)
	if err != nil {
		return nil, err
	}
	r.runID = event.RunID
	r.revision++
	for client := range r.clients {
		client.wake()
	}
	return responses, nil
}
func (r *runningSession) finish(code int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = true
	r.controller = nil
	r.revision++
	r.exit = protocol.Message{Type: "exit", Code: code}
	if err != nil {
		r.exit.Error = err.Error()
	}
	for client := range r.clients {
		client.wake()
	}
}

func (s *Server) sessionAttachment(ctx context.Context, conn *protocol.Conn, session history.Session, run *agent.Launch) func() {
	s.mu.Lock()
	runContext, cancel := context.WithCancel(ctx)
	r := &runningSession{id: session.ID, controls: make(chan agent.Control, 16), cancel: cancel, done: make(chan struct{}), terminal: terminal.New(run.Size), revision: 1}
	s.sessions[session.ID] = r
	s.mu.Unlock()
	launch := func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer cancel()
			code, err := agent.Run(runContext, s.store, session, *run, r.controls, r.publish)
			err = errors.Join(err, s.saveScreen(r))
			r.finish(code, err)
			s.mu.Lock()
			delete(s.sessions, session.ID)
			s.mu.Unlock()
			close(r.done)
		}()
	}
	return func() { s.attach(conn, r, run.Size, launch) }
}

// Session launches always use the server environment and interactive shell.
func prepareRun(request *protocol.Run) (*agent.Launch, error) {
	if request == nil || !terminal.ValidSize(request.Size) {
		return nil, errors.New("invalid new session request")
	}
	run := agent.Launch{Run: *request}
	if err := history.ValidateCommand(run.Command); err != nil {
		return nil, err
	}
	workspace, err := expandWorkspace(run.Workspace)
	if err != nil {
		return nil, err
	}
	run.Workspace = workspace
	run.Shell = os.Getenv("SHELL")
	if run.Shell == "" {
		run.Shell = "/bin/sh"
	}
	if !filepath.IsAbs(run.Shell) && filepath.Base(run.Shell) != run.Shell {
		return nil, errors.New("SHELL must be an absolute path or a name on the server PATH")
	}
	path, err := exec.LookPath(run.Shell)
	if err != nil {
		return nil, err
	}
	run.Shell, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	run.Env = append(os.Environ(), "TERM=xterm-256color")
	return &run, nil
}

func (s *Server) running(id string) *runningSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *Server) resumeSession(conn *protocol.Conn, id string, size protocol.Size) {
	if !terminal.ValidSize(size) {
		s.reply(conn, nil, errors.New("terminal size must be 2–512 columns and 1–256 rows"))
		return
	}
	if r := s.running(id); r != nil {
		s.attach(conn, r, size, nil)
		return
	}
	session, err := s.store.Session(id)
	if err != nil {
		s.reply(conn, nil, err)
		return
	}
	status := "ended"
	if session.Run != nil {
		status = session.Run.Status
	}
	if err = conn.Send(protocol.Message{Type: "session", Session: id, Status: status}); err != nil {
		return
	}
	view, err := s.loadScreen(id)
	if err != nil {
		s.reply(conn, nil, err)
		return
	}
	data, _ := view.Render(nil)
	if err = conn.Send(protocol.Message{Type: "frame", Data: data, Size: view.Size, Revision: 1, Full: true}); err != nil {
		return
	}
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for {
		ack, err := conn.Receive()
		if err != nil {
			return
		}
		if ack.Type == "ping" {
			conn.Send(protocol.Message{Type: "pong"})
			continue
		}
		if ack.Type != "frame-ack" || ack.Revision != 1 {
			s.reply(conn, nil, errors.New("expected frame acknowledgement"))
			return
		}
		conn.Send(protocol.Message{Type: "history-end"})
		return
	}
}

// One frame may be in flight. Capture keeps updating the emulator while the
// client renders. After its acknowledgement, diff against the accepted baseline.
func (s *Server) attach(conn *protocol.Conn, r *runningSession, size protocol.Size, launch func()) {
	a := r.subscribe(conn, size)
	if launch != nil {
		launch()
	}
	defer a.close()
	defer r.leave(a)
	if err := conn.Send(protocol.Message{Type: "session", Session: r.id, Active: true}); err != nil {
		return
	}
	if launch == nil {
		r.control(a, protocol.Message{Type: "resize", Size: size})
	}
	incoming := make(chan protocol.Message, 16)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer a.close()
		for {
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			message, err := conn.Receive()
			if err != nil {
				return
			}
			select {
			case incoming <- message:
			case <-a.closed:
				return
			}
		}
	}()
	defer func() { a.close(); <-readerDone }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var previous *terminal.View
	var sent, acked int64
	var sentAt time.Time
	dirty := true
	roleSent, controlling := false, false
	for {
		r.mu.Lock()
		nowControlling := r.controller == a
		r.mu.Unlock()
		if !roleSent || nowControlling != controlling {
			if err := conn.Send(protocol.Message{Type: "control", Controlling: nowControlling}); err != nil {
				return
			}
			roleSent, controlling = true, nowControlling
		}

		if dirty && sent == acked && (previous == nil || time.Since(sentAt) >= 50*time.Millisecond) {
			r.mu.Lock()
			revision, ended, exit := r.revision, r.ended, r.exit
			var next *terminal.View
			if revision != sent || previous == nil {
				next = r.terminal.ViewFor(a.size)
			}
			r.mu.Unlock()
			if next != nil {
				data, full := next.Render(previous)
				if err := conn.Send(protocol.Message{Type: "frame", Data: data, Size: next.Size, Revision: revision, Full: full}); err != nil {
					return
				}
				previous, sent, sentAt = next, revision, time.Now()
			} else if ended {
				conn.Send(exit)
				return
			}
			dirty = false
		}
		select {
		case <-a.closed:
			return
		case <-a.changed:
			dirty = true
		case <-ticker.C:
			if sent != acked && time.Since(sentAt) > 30*time.Second {
				return
			}
			// Coalesce output while a frame is outstanding; render at most 20 times/sec.
			if dirty && sent == acked {
				continue
			}
		case message := <-a.messages:
			if err := conn.Send(message); err != nil {
				return
			}
		case control := <-incoming:
			switch control.Type {
			case "frame-ack":
				if control.Revision != sent || sent == acked {
					s.reply(conn, nil, errors.New("unexpected frame acknowledgement"))
					return
				}
				acked = sent
				dirty = true
			case "ping":
				if conn.Send(protocol.Message{Type: "pong"}) != nil {
					return
				}
			case "input", "resize", "signal", "take-control":
				if acked == 0 {
					s.reply(conn, nil, errors.New("terminal is not ready for input"))
					return
				}
				if control.Type == "input" && len(control.ID) > 80 {
					return
				}
				if (control.Type == "resize" || control.Type == "take-control") && !terminal.ValidSize(control.Size) {
					s.reply(conn, nil, errors.New("terminal size must be 2–512 columns and 1–256 rows"))
					return
				}
				if control.Type == "signal" && syscall.Signal(control.Signal) != syscall.SIGINT {
					continue
				}
				r.control(a, control)
			default:
				s.reply(conn, nil, errors.New("unsupported terminal control"))
				return
			}
		}
	}
}

func (s *Server) listSessions(filter history.SessionFilter) ([]protocol.SessionInfo, error) {
	sessions, err := s.store.ListSessions(filter)
	if err != nil {
		return nil, err
	}
	result := make([]protocol.SessionInfo, 0, len(sessions))
	for _, session := range sessions {
		info := protocol.SessionInfo{ID: session.ID, ProjectID: session.ProjectID, Archived: session.Archived, Title: session.Title, Workspace: session.Workspace, Updated: session.Updated, Status: "ended", Preparation: session.Preparation, Worktree: session.Worktree}
		if session.Preparation != nil {
			info.Status = session.Preparation.State
		}
		if session.Run != nil {
			info.Status = session.Run.Status
		}
		if r := s.running(session.ID); r != nil {
			r.mu.Lock()
			info.Viewers = len(r.clients)
			if !r.ended && session.Run == nil {
				info.Status = "starting"
			}
			r.mu.Unlock()
		}
		result = append(result, info)
	}
	return result, nil
}

func (s *Server) archiveSession(id string, archived bool) error {
	// Serialize with launch so archiving cannot race a new PTY or worktree.
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	if archived && s.running(id) != nil {
		return errors.New("stop the session before archiving it")
	}
	return s.store.SetSessionArchived(id, archived)
}

func (s *Server) stopSession(id string) (any, error) {
	if r := s.running(id); r != nil {
		r.cancel()
		select {
		case <-r.done:
			return map[string]bool{"stopped": true}, nil
		case <-time.After(10 * time.Second):
			return nil, errors.New("session has not finished stopping")
		}
	}
	if _, err := s.store.Session(id); err != nil {
		return nil, err
	}
	return map[string]bool{"stopped": false}, nil
}
