// Package server owns agent processes and the open history archive.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bonbon/internal/history"
	"bonbon/internal/instance"
	"bonbon/internal/protocol"
	"bonbon/internal/webui"
	"github.com/gorilla/websocket"
)

type Server struct {
	store    *history.Store
	info     protocol.ServerInfo
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	sessions map[string]*runningSession
}

// Serve stops accepting requests on cancellation and waits for agent cleanup.
// The caller owns the archive and the exclusive server lock for its directory.
func Serve(ctx context.Context, listener net.Listener, store *history.Store) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	executable, _ := os.Executable()
	s := &Server{store: store, cancel: cancel, sessions: make(map[string]*runningSession), info: protocol.ServerInfo{
		Protocol: protocol.Version, Instance: history.ID(), PID: os.Getpid(),
		DataDir: filepath.Dir(store.Path), Port: listener.Addr().(*net.TCPAddr).Port,
		Executable: executable,
	}}
	if err := instance.Publish(s.info); err != nil {
		return err
	}
	defer instance.Remove(s.info.DataDir)
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second}
	mux := http.NewServeMux()
	webui.Register(mux)
	mux.HandleFunc("GET /client-config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.info)
	})
	mux.HandleFunc("GET "+protocol.Path, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if ctx.Err() != nil {
			s.mu.Unlock()
			http.Error(w, "server is stopping", http.StatusServiceUnavailable)
			return
		}
		// HTTP shutdown does not wait for upgraded connections. Register before
		// upgrading, and prevent registrations once shutdown begins.
		s.wg.Add(1)
		s.mu.Unlock()
		defer s.wg.Done()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		wire := protocol.Wrap(conn)
		defer wire.Close()
		s.handle(ctx, wire)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Apply the loopback Host restriction to both assets and WebSockets.
		// The upgrader also rejects foreign browser origins.
		if r.Host != listener.Addr().String() && r.Host != fmt.Sprintf("localhost:%d", s.info.Port) {
			http.Error(w, "invalid local host", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws://127.0.0.1:%d ws://localhost:%d; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'", s.info.Port, s.info.Port))
		mux.ServeHTTP(w, r)
	})
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() { <-ctx.Done(); httpServer.Close() }()
	err := httpServer.Serve(listener)
	s.mu.Lock()
	cancel()
	s.mu.Unlock()
	httpServer.Close()
	s.wg.Wait()
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) handle(ctx context.Context, conn *protocol.Conn) {
	// Let attached clients receive the final screen and exit status during normal
	// shutdown. A stalled peer must still release its handler within five seconds.
	finished := make(chan struct{})
	defer close(finished)
	stopClose := context.AfterFunc(ctx, func() {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			conn.Close()
		}
	})
	defer stopClose()
	if err := conn.Send(protocol.Message{Type: "server", Server: &s.info}); err != nil {
		return
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	message, err := conn.Receive()
	if err != nil || message.Type != "request" || message.Request == nil {
		return
	}
	conn.SetReadDeadline(time.Time{})
	request := message.Request
	if request.Protocol != protocol.Version {
		s.reply(conn, nil, errors.New("unsupported client protocol; rebuild the client and restart the server"))
		return
	}
	if ctx.Err() != nil {
		s.reply(conn, nil, errors.New("server is stopping"))
		return
	}
	switch request.Operation {
	case "stop":
		if request.Instance != s.info.Instance {
			s.reply(conn, nil, errors.New("server instance changed; retry stop"))
			return
		}
		s.reply(conn, map[string]bool{"stopping": true}, nil)
		s.cancel()
	case "session-new":
		s.newSession(ctx, conn, request.Run)
	case "session-resume":
		s.resumeSession(conn, request.Session, request.Size)
	case "session-list":
		result, err := s.listSessions(request.Limit)
		s.reply(conn, result, err)
	case "session-stop":
		result, err := s.stopSession(request.Session)
		s.reply(conn, result, err)
	case "query":
		result, err := s.store.Query(ctx, request.SQL)
		s.reply(conn, result, err)
	case "composer-draft":
		result, err := s.composer(request.Session, request.Draft)
		s.reply(conn, result, err)
	case "composer-attach":
		result, err := s.upload(request.Session, request.Upload)
		s.reply(conn, result, err)
	default:
		s.reply(conn, nil, errors.New("unknown request"))
	}
}

func (s *Server) reply(conn *protocol.Conn, result any, err error) {
	if err != nil {
		conn.Send(protocol.Message{Type: "error", Error: err.Error()})
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		conn.Send(protocol.Message{Type: "error", Error: err.Error()})
		return
	}
	conn.Send(protocol.Message{Type: "result", Result: data})
}
