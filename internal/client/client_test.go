package client

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"bonbon/internal/instance"
	"bonbon/internal/protocol"
	"github.com/gorilla/websocket"
)

func TestMenuQuitIsBoundToItsServerInstance(t *testing.T) {
	for _, expected := range []string{"current", "previous"} {
		t.Run(expected, func(t *testing.T) {
			target, received := fixtureServer(t, "bonbon/other", nil, protocol.Message{Type: "result", Result: json.RawMessage(`{"stopping":true}`)})
			err := target.StopInstance(expected)
			message := receiveRequest(t, received)
			if expected == "previous" {
				if !errors.Is(err, ErrServerVerification) || message.Type != "" {
					t.Fatalf("stale menu sent shutdown: %v %+v", err, message)
				}
			} else if err != nil || message.Request == nil || message.Request.Instance != expected {
				t.Fatalf("menu quit: %v %+v", err, message)
			}
		})
	}
	if err := (Target{}).StopInstance(""); err == nil {
		t.Fatal("accepted an unbound menu quit")
	}
}

func fixtureServer(t *testing.T, version string, change func(*protocol.Message), reply protocol.Message) (Target, <-chan protocol.Message) {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info := protocol.ServerInfo{Protocol: version, Instance: "current", DataDir: directory}
	received := make(chan protocol.Message, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		greetingInfo := info
		greeting := protocol.Message{Type: "server", Server: &greetingInfo}
		if change != nil {
			change(&greeting)
		}
		conn.WriteJSON(greeting)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var message protocol.Message
		if conn.ReadJSON(&message) == nil {
			conn.WriteJSON(reply)
		}
		received <- message
	}))
	info.Port = server.Listener.Addr().(*net.TCPAddr).Port
	server.Start()
	t.Cleanup(server.Close)
	if err = instance.Publish(info); err != nil {
		t.Fatal(err)
	}
	return Target{Dir: directory}, received
}

func receiveRequest(t *testing.T, received <-chan protocol.Message) protocol.Message {
	t.Helper()
	select {
	case message := <-received:
		return message
	case <-time.After(4 * time.Second):
		t.Fatal("connection was not closed")
		return protocol.Message{}
	}
}

func TestClientsVerifyGreetingBeforeSendingOperation(t *testing.T) {
	for name, operation := range map[string]func(Target) error{
		"query": func(target Target) error {
			conn, err := target.Connect(protocol.Request{Operation: "query", SQL: "SELECT 1"})
			if conn != nil {
				conn.Close()
			}
			return err
		},
		"stop": Target.Stop,
	} {
		t.Run(name, func(t *testing.T) {
			for _, mismatch := range []string{"directory", "instance", "port", "protocol", "empty-protocol", "missing-server", "message-type"} {
				t.Run(mismatch, func(t *testing.T) {
					target, received := fixtureServer(t, protocol.Version, func(greeting *protocol.Message) {
						switch mismatch {
						case "directory":
							greeting.Server.DataDir = filepath.Join(greeting.Server.DataDir, "other")
						case "instance":
							greeting.Server.Instance = "replacement"
						case "port":
							greeting.Server.Port++
						case "protocol":
							greeting.Server.Protocol = "unsupported"
						case "empty-protocol":
							greeting.Server.Protocol = ""
						case "missing-server":
							greeting.Server = nil
						case "message-type":
							greeting.Type = "result"
						}
					}, protocol.Message{})
					if err := operation(target); err == nil {
						t.Fatal("accepted mismatched greeting")
					}
					if message := receiveRequest(t, received); message.Type != "" {
						t.Fatalf("sent an operation before verifying identity: %+v", message)
					}
				})
			}
		})
	}
}

func TestQueryAndHealthRequireCurrentProtocol(t *testing.T) {
	for _, health := range []bool{false, true} {
		target, received := fixtureServer(t, "bonbon/other", nil, protocol.Message{})
		var err error
		if health {
			_, err = target.Health()
		} else {
			var conn *protocol.Conn
			conn, err = target.Connect(protocol.Request{Operation: "query", SQL: "SELECT 1"})
			if conn != nil {
				conn.Close()
			}
		}
		if err == nil {
			t.Fatalf("health=%t accepted another protocol version", health)
		}
		if message := receiveRequest(t, received); message.Type != "" {
			t.Fatalf("sent an operation with an unsupported protocol: %+v", message)
		}
	}
}

func TestStopUsesVerifiedServerProtocol(t *testing.T) {
	for _, version := range []string{protocol.Version, "bonbon/other"} {
		t.Run(version, func(t *testing.T) {
			target, received := fixtureServer(t, version, nil, protocol.Message{Type: "result", Result: json.RawMessage(`{"stopping":true}`)})
			if err := target.Stop(); err != nil {
				t.Fatal(err)
			}
			message := receiveRequest(t, received)
			if message.Type != "request" || message.Request == nil || message.Request.Operation != "stop" || message.Request.Protocol != version || message.Request.Instance != "current" {
				t.Fatalf("shutdown did not use the verified greeting: %+v", message)
			}
		})
	}
}

func TestStopRequiresAcknowledgement(t *testing.T) {
	for _, reply := range []protocol.Message{
		{Type: "error", Error: "shutdown refused"},
		{Type: "session"},
		{Type: "result"},
		{Type: "result", Result: json.RawMessage(`{"stopping":false}`)},
	} {
		target, _ := fixtureServer(t, "bonbon/other", nil, reply)
		if err := target.Stop(); err == nil {
			t.Fatalf("accepted shutdown reply: %+v", reply)
		}
	}
}
