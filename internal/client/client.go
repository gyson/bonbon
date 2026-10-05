// Package client connects the CLI to the local BonBon server.
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"bonbon/internal/instance"
	"bonbon/internal/protocol"
	"github.com/gorilla/websocket"
)

// Target selects one instance. Its endpoint can change on every server start.
type Target struct{ Dir string }

// ErrServerVerification requires explicit recovery, not a transport retry.
var ErrServerVerification = errors.New("server verification failed")

func (t Target) open() (*protocol.Conn, protocol.ServerInfo, error) {
	var info protocol.ServerInfo
	directory, err := filepath.EvalSymlinks(t.Dir)
	if err != nil {
		return nil, info, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, info, err
	}
	saved, err := instance.Read(directory)
	if err != nil {
		return nil, info, fmt.Errorf("read BonBon instance %s (run bonbon --dir %q server start first): %w", directory, directory, err)
	}
	saved.DataDir = directory
	return openServer(saved)
}

// openServer verifies the peer on the connection that will carry the operation.
// A menu retains its parent's verified descriptor, independently of server.json.
func openServer(saved protocol.ServerInfo) (*protocol.Conn, protocol.ServerInfo, error) {
	var info protocol.ServerInfo
	if saved.Instance == "" || saved.Protocol == "" || !filepath.IsAbs(saved.DataDir) || saved.Port < 1 || saved.Port > 65535 {
		return nil, info, fmt.Errorf("%w: incomplete server identity", ErrServerVerification)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(saved.Port))
	dialer := websocket.Dialer{HandshakeTimeout: time.Second}
	conn, _, err := dialer.Dial("ws://"+address+protocol.Path, nil)
	if err != nil {
		return nil, info, fmt.Errorf("connect to BonBon in %s: %w", saved.DataDir, err)
	}
	wire := protocol.Wrap(conn)
	wire.SetReadDeadline(time.Now().Add(time.Second))
	reply, err := wire.Receive()
	if err == nil {
		if reply.Type != "server" || reply.Server == nil || reply.Server.Protocol == "" {
			err = fmt.Errorf("%w: invalid BonBon server greeting", ErrServerVerification)
		} else if reply.Server.DataDir != saved.DataDir || reply.Server.Instance != saved.Instance || reply.Server.Port != saved.Port || reply.Server.Protocol != saved.Protocol || reply.Server.PID != saved.PID {
			err = fmt.Errorf("%w: server identity does not match the expected instance", ErrServerVerification)
		} else {
			info = *reply.Server
		}
	}
	if err != nil {
		wire.Close()
		return nil, info, err
	}
	wire.SetReadDeadline(time.Time{})
	return wire, info, nil
}

func (t Target) Connect(request protocol.Request) (*protocol.Conn, error) {
	// Verify the greeting on this connection before sending any operation.
	wire, _, err := t.openCurrent()
	if err != nil {
		return nil, err
	}
	request.Protocol = protocol.Version
	if err = wire.Send(protocol.Message{Type: "request", Request: &request}); err != nil {
		wire.Close()
		return nil, err
	}
	return wire, nil
}

func (t Target) Health() (protocol.ServerInfo, error) {
	conn, info, err := t.openCurrent()
	if err == nil {
		conn.Close()
	}
	return info, err
}

func (t Target) openCurrent() (*protocol.Conn, protocol.ServerInfo, error) {
	conn, info, err := t.open()
	if err == nil && info.Protocol != protocol.Version {
		conn.Close()
		return nil, info, fmt.Errorf("%w: server uses %s, client uses %s; run bonbon --dir %q server restart", ErrServerVerification, info.Protocol, protocol.Version, t.Dir)
	}
	return conn, info, err
}

// Stop uses the verified server's advertised version for the lifecycle request.
// Shutdown does not depend on session protocol compatibility. Verify and send on
// the same connection, without retrying or signalling a PID from server.json.
func (t Target) Stop() error {
	conn, info, err := t.open()
	if err != nil {
		return fmt.Errorf("refusing to stop an unverified server: %w", err)
	}
	return stop(conn, info)
}

// StopInstance binds a companion's Quit action to the server that launched it.
// expected must be the identity verified when the companion started. Reconnect
// directly, so missing or replaced connection files cannot strand the menu.
func StopInstance(expected protocol.ServerInfo) error {
	conn, info, err := openServer(expected)
	if err != nil {
		return fmt.Errorf("refusing to stop an unverified server: %w", err)
	}
	return stop(conn, info)
}

func stop(conn *protocol.Conn, info protocol.ServerInfo) error {
	defer conn.Close()
	request := protocol.Request{Protocol: info.Protocol, Operation: "stop", Instance: info.Instance}
	if err := conn.Send(protocol.Message{Type: "request", Request: &request}); err != nil {
		return err
	}
	result, err := receiveResult(conn)
	if err != nil {
		return err
	}
	var reply struct {
		Stopping bool `json:"stopping"`
	}
	if err = json.Unmarshal(result, &reply); err != nil || !reply.Stopping {
		return errors.New("server did not acknowledge shutdown")
	}
	return nil
}

func (t Target) Call(request protocol.Request) (json.RawMessage, error) {
	conn, err := t.Connect(request)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return receiveResult(conn)
}

func receiveResult(conn *protocol.Conn) (json.RawMessage, error) {
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	reply, err := conn.Receive()
	if err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, errors.New(reply.Error)
	}
	if reply.Type != "result" {
		return nil, fmt.Errorf("unexpected server reply %q", reply.Type)
	}
	return reply.Result, nil
}
