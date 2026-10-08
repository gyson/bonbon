package protocol

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func socketPair(t *testing.T) (*Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		accepted <- conn
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := <-accepted
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { conn.Close(); peer.Close() })
	return Wrap(conn), peer
}

func TestWebSocketJSONPreservesBytes(t *testing.T) {
	conn, peer := socketPair(t)
	want := []byte{0, 3, 27, 255, '\n', '\r'}
	// This is ordinary JSON a browser can send, with no BonBon framing header.
	if err := peer.WriteMessage(websocket.TextMessage, []byte(`{"type":"input","data":"AAMb/woN"}`)); err != nil {
		t.Fatal(err)
	}
	message, err := conn.Receive()
	if err != nil || message.Type != "input" || !bytes.Equal(message.Data, want) {
		t.Fatalf("lost bytes: %+v %v", message, err)
	}
	if err = conn.Send(Message{Type: "output", Data: want}); err != nil {
		t.Fatal(err)
	}
	kind, data, err := peer.ReadMessage()
	if err != nil || kind != websocket.TextMessage || !bytes.Contains(data, []byte(`"data":"AAMb/woN"`)) {
		t.Fatalf("not a JSON text message: %d %q %v", kind, data, err)
	}
}

func TestWebSocketRejectsInvalidMessages(t *testing.T) {
	for _, test := range []struct {
		name string
		kind int
		data string
	}{
		{"binary", websocket.BinaryMessage, `{"type":"input"}`},
		{"malformed JSON", websocket.TextMessage, `{`},
		{"multiple JSON values", websocket.TextMessage, `{} {}`},
		{"empty message", websocket.TextMessage, ""},
		{"null", websocket.TextMessage, "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, peer := socketPair(t)
			if err := peer.WriteMessage(test.kind, []byte(test.data)); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Receive(); err == nil {
				t.Fatal("accepted invalid message")
			}
		})
	}
}

func TestWebSocketBoundsMessages(t *testing.T) {
	conn, peer := socketPair(t)
	if err := conn.Send(Message{Type: "error", Error: strings.Repeat("x", maxMessage)}); err == nil {
		t.Fatal("sent oversized JSON message")
	}
	// The message limit must include all WebSocket fragments.
	done := make(chan struct{})
	go func() {
		defer close(done)
		peer.SetWriteDeadline(time.Now().Add(5 * time.Second))
		writer, err := peer.NextWriter(websocket.TextMessage)
		if err != nil {
			return
		}
		defer writer.Close()
		chunk := bytes.Repeat([]byte("x"), 4096)
		for n := 0; n <= maxMessage; n += len(chunk) {
			if _, err = writer.Write(chunk); err != nil {
				return
			}
		}
	}()
	_, err := conn.Receive()
	conn.Close()
	<-done
	if !errors.Is(err, websocket.ErrReadLimit) {
		t.Fatalf("expected message limit: %v", err)
	}
}

func TestConcurrentSendsRemainSeparateMessages(t *testing.T) {
	conn, peer := socketPair(t)
	var senders sync.WaitGroup
	for i := 0; i < 20; i++ {
		senders.Add(1)
		go func() {
			defer senders.Done()
			if err := conn.Send(Message{Type: "output", Code: i}); err != nil {
				t.Error(err)
			}
		}()
	}
	seen := make(map[int]bool)
	for i := 0; i < 20; i++ {
		var message Message
		if err := peer.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		seen[message.Code] = true
	}
	senders.Wait()
	if len(seen) != 20 {
		t.Fatal("lost concurrent messages", seen)
	}
}
