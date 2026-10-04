package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bonbon/internal/protocol"
	"github.com/gorilla/websocket"
)

func TestBrowserLaunchAndReconnect(t *testing.T) {
	directory, home := t.TempDir(), t.TempDir()
	workspace := filepath.Join(home, "project with spaces")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	executable, _ := os.Executable()
	// The browser always uses the server's shell, resolved from its PATH.
	bin := t.TempDir()
	if err := os.Symlink("/bin/sh", filepath.Join(bin, "browser-shell")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "browser-shell")
	t.Setenv("ENV", "")
	t.Setenv("PS1", "BONBON_SHELL_PROMPT> ")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	port := startTestServer(t, directory)
	address := "127.0.0.1:" + port
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+address+"/ws", http.Header{"Origin": {"http://" + address}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readGreeting(t, conn)
	if err = conn.WriteJSON(map[string]any{"type": "request", "request": map[string]any{
		"protocol": protocol.Version, "operation": "session-new", "run": map[string]any{
			"workspace": "~/project with spaces", "title": "Browser fixture",
			"size": map[string]int{"rows": 25, "cols": 90},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	var session protocol.Message
	if err = conn.ReadJSON(&session); err != nil || session.Type != "session" || !session.Active {
		t.Fatalf("browser launch: %+v %v", session, err)
	}
	var output string
	for !strings.Contains(output, "BONBON_SHELL_PROMPT> ") {
		var message protocol.Message
		if err = conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "frame" {
			conn.WriteJSON(protocol.Message{Type: "frame-ack", Revision: message.Revision})
		}
		output += string(message.Data)
	}
	launch := fixtureInput(executable, "stream")
	if err = conn.WriteJSON(protocol.Message{Type: "input", Data: launch}); err != nil {
		t.Fatal(err)
	}
	output = ""
	for !strings.Contains(output, "READY") {
		var message protocol.Message
		if err = conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "frame" {
			conn.WriteJSON(protocol.Message{Type: "frame-ack", Revision: message.Revision})
		}
		output += string(message.Data)
	}
	store := archive(t, directory)
	before, err := store.Session(session.Session)
	if err != nil || before.Run == nil || before.Run.PID == 0 {
		t.Fatalf("missing recorded process: %+v %v", before, err)
	}
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil || before.Workspace != canonical {
		t.Fatalf("workspace was not expanded and canonicalized: %q, %v", before.Workspace, err)
	}
	// Composer submissions use the ordinary input stream with an optional receipt.
	input := []byte("\x1b[200~hello café\rsecond line\x1b[201~\r")
	if err = conn.WriteJSON(protocol.Message{Type: "input", ID: "composer-fixture", Data: input}); err != nil {
		t.Fatal(err)
	}
	for {
		var message protocol.Message
		if err = conn.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "frame" {
			conn.WriteJSON(protocol.Message{Type: "frame-ack", Revision: message.Revision})
		}
		if message.Type == "input-ack" {
			if message.ID != "composer-fixture" || message.Error != "" {
				t.Fatalf("input receipt: %+v", message)
			}
			break
		}
	}
	evidence, err := store.Query(t.Context(), "SELECT data FROM events WHERE session_id='"+session.Session+"' AND kind='input' ORDER BY seq")
	if err != nil || len(evidence.Rows) != 2 || !bytes.Equal(evidence.Rows[0][0].([]byte), launch) || !bytes.Equal(evidence.Rows[1][0].([]byte), input) {
		t.Fatalf("composer input was not recorded once, byte for byte: %+v %v", evidence, err)
	}
	conn.Close()
	waitFor(t, func() bool { return sessionInfos(t, 10)[0].Viewers == 0 })
	resumed := resumeView(t, session.Session)
	waitFor(t, func() bool { return strings.Contains(resumed.text(), "READY") })
	after, err := store.Session(session.Session)
	if err != nil || after.Run.PID != before.Run.PID {
		t.Fatalf("resume changed process: %+v %v", after, err)
	}
	resumed.Write([]byte("q"))
	resumed.wait(t, 19)
	waitFor(t, func() bool { return strings.Contains(resumed.text(), "FINISHED") })
	recorded := recordedOutput(t, store, session.Session)
	if !strings.Contains(recorded, "FINISHED") {
		t.Fatalf("missing durable output: %q", recorded)
	}
}

func TestEmbeddedWebUI(t *testing.T) {
	port := startTestServer(t, t.TempDir())
	address := "127.0.0.1:" + port
	client := &http.Client{Timeout: 3 * time.Second}
	for _, path := range []string{"/", "/assets/app.js", "/assets/app.css", "/assets/bonbon.svg", "/assets/favicon.svg", "/assets/menubar.png", "/assets/licenses.txt", "/client-config"} {
		response, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || len(data) == 0 {
			t.Fatalf("asset %s: %d %v", path, response.StatusCode, err)
		}
		if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("missing browser protections: %v", response.Header)
		}
		if path == "/assets/app.js" && (!strings.Contains(string(data), protocol.Version) || !strings.Contains(response.Header.Get("Content-Type"), "javascript")) {
			t.Fatal("browser protocol or MIME type differs from server")
		}
		if path == "/client-config" {
			var info protocol.ServerInfo
			if json.Unmarshal(data, &info) != nil || info.Protocol != protocol.Version || info.Instance == "" {
				t.Fatal("invalid browser connection information")
			}
		}
	}
	for _, path := range []string{"/", "/client-config", "/assets/app.js"} {
		request, _ := http.NewRequest("GET", "http://"+address+path, nil)
		request.Host = "foreign.example:" + port
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("foreign host reached %s", path)
		}
	}
	for _, path := range []string{"/assets/", "/assets/vendor/", "/history.sqlite", "/server.json"} {
		response, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("unexpected file exposure at %s: %d", path, response.StatusCode)
		}
	}
}

func TestWebSocketEndpointAndBrowserOrigins(t *testing.T) {
	directory := t.TempDir()
	port := startTestServer(t, directory)
	address := "127.0.0.1:" + port
	for _, test := range []struct {
		name, host, origin, path string
		status                   int
	}{
		{"CLI", address, "", "/ws", 101},
		{"browser", address, "http://" + address, "/ws", 101},
		{"localhost browser", "localhost:" + port, "http://localhost:" + port, "/ws", 101},
		{"foreign origin", address, "https://example.com", "/ws", 403},
		{"different port", address, "http://127.0.0.1:1", "/ws", 403},
		{"opaque origin", address, "null", "/ws", 403},
		{"foreign host", "example.com:" + port, "http://example.com:" + port, "/ws", 403},
		{"unknown path", address, "", "/other", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := http.Header{"Host": {test.host}}
			if test.origin != "" {
				headers.Set("Origin", test.origin)
			}
			dialer := websocket.Dialer{HandshakeTimeout: time.Second}
			conn, response, err := dialer.Dial("ws://"+address+test.path, headers)
			if response != nil {
				defer response.Body.Close()
			}
			if conn != nil {
				defer conn.Close()
			}
			if response == nil || response.StatusCode != test.status {
				t.Fatalf("unexpected handshake: %v %v", response, err)
			}
			if test.status != http.StatusSwitchingProtocols {
				if err == nil {
					t.Fatal("upgraded a rejected connection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Use plain JSON over the public endpoint, without BonBon's client or
			// transport helpers, as the browser does.
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			readGreeting(t, conn)
			if err = conn.WriteJSON(map[string]any{
				"type": "request", "request": map[string]any{
					"protocol": protocol.Version, "operation": "query",
					"sql": "SELECT 'browser-ready' AS value",
				},
			}); err != nil {
				t.Fatal(err)
			}
			kind, data, err := conn.ReadMessage()
			var message struct {
				Type   string `json:"type"`
				Result struct {
					Rows [][]string `json:"rows"`
				} `json:"result"`
			}
			if err != nil || kind != websocket.TextMessage || json.Unmarshal(data, &message) != nil || message.Type != "result" || len(message.Result.Rows) != 1 || len(message.Result.Rows[0]) != 1 || message.Result.Rows[0][0] != "browser-ready" {
				t.Fatalf("bad JSON reply: %q %v", data, err)
			}
			if _, _, err = conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				t.Fatalf("RPC did not close normally: %v", err)
			}
		})
	}
	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + address + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("ordinary HTTP request returned %d", response.StatusCode)
	}
}

func TestWebSocketRejectsWrongProtocol(t *testing.T) {
	directory := t.TempDir()
	port := startTestServer(t, directory)
	conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+port+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	readGreeting(t, conn)
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"request","request":{"protocol":"unsupported","operation":"session-list"}}`)); err != nil {
		t.Fatal(err)
	}
	var reply protocol.Message
	if err = conn.ReadJSON(&reply); err != nil || reply.Type != "error" || !strings.Contains(reply.Error, "unsupported client protocol") {
		t.Fatalf("accepted wrong protocol: %+v %v", reply, err)
	}
}

func TestServerStopClosesIdleAndStalledClients(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	port := startTestServer(t, directory)
	address := "127.0.0.1:" + port
	idleHTTP, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer idleHTTP.Close()
	idleWS, _, err := websocket.DefaultDialer.Dial("ws://"+address+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idleWS.Close()
	readGreeting(t, idleWS)
	stalled, _, err := websocket.DefaultDialer.Dial("ws://"+address+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	stalled.SetReadDeadline(time.Now().Add(5 * time.Second))
	readGreeting(t, stalled)
	if err := stalled.WriteJSON(protocol.Message{Type: "request", Request: &protocol.Request{Protocol: protocol.Version, Operation: "session-new", Run: &protocol.Run{Workspace: t.TempDir(), Size: protocol.Size{Rows: 24, Cols: 80}}}}); err != nil {
		t.Fatal(err)
	}
	var session, role, frame protocol.Message
	if err := stalled.ReadJSON(&session); err != nil || session.Type != "session" {
		t.Fatalf("stalled session: %+v %v", session, err)
	}
	if err := stalled.ReadJSON(&role); err != nil || role.Type != "control" || !role.Controlling {
		t.Fatalf("control: %+v %v", role, err)
	}
	if err := stalled.ReadJSON(&frame); err != nil || frame.Type != "frame" {
		t.Fatalf("stalled frame: %+v %v", frame, err)
	}
	// Never acknowledge this frame. Shutdown must not wait for the usual 30s
	// acknowledgement deadline or interrupt process cleanup and durable capture.
	if output, err := testCommand("--dir", directory, "server", "stop").CombinedOutput(); err != nil {
		t.Fatalf("stop with idle clients: %s %v", output, err)
	}
	idleHTTP.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = idleHTTP.Read(make([]byte, 1)); err == nil {
		t.Fatal("HTTP connection survived stop")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("HTTP connection was not closed", err)
	}
	idleWS.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = idleWS.ReadMessage(); err == nil {
		t.Fatal("WebSocket connection survived stop")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("WebSocket connection was not closed", err)
	}
	stalled.SetReadDeadline(time.Now().Add(time.Second))
	for {
		var m protocol.Message
		if err = stalled.ReadJSON(&m); err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("stalled stream was not closed", err)
			}
			break
		}
		if m.Type != "control" || m.Controlling {
			t.Fatalf("unexpected stalled stream event: %+v", m)
		}
	}
	saved, err := archive(t, directory).Session(session.Session)
	if err != nil || saved.Run == nil || saved.Run.Status != "stopped" {
		t.Fatalf("stalled client prevented process cleanup: %+v %v", saved, err)
	}
}

func readGreeting(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	var message protocol.Message
	if err := conn.ReadJSON(&message); err != nil || message.Type != "server" || message.Server == nil || message.Server.Protocol != protocol.Version {
		t.Fatalf("invalid greeting: %+v %v", message, err)
	}
}

func TestStoppedSessionRestoresFinalScreenWithoutReplayingEvents(t *testing.T) {
	directory := t.TempDir()
	port := startTestServer(t, directory)
	view := launchAgent(t, t.TempDir(), "stream")
	waitFor(t, func() bool { return strings.Contains(view.text(), "READY") })
	id := sessionInfos(t, 1)[0].ID
	stopTestSession(t, id)
	store := archive(t, directory)
	before, err := recordedEvents(t, store, id)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		conn, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+port+"/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		readGreeting(t, conn)
		conn.WriteJSON(protocol.Message{Type: "request", Request: &protocol.Request{Protocol: protocol.Version, Operation: "session-resume", Session: id, Size: protocol.Size{Rows: 40, Cols: 120}}})
		var m protocol.Message
		if err := conn.ReadJSON(&m); err != nil || m.Type != "session" || m.Active || m.Status != "stopped" {
			t.Fatalf("session: %+v %v", m, err)
		}
		if err := conn.ReadJSON(&m); err != nil || m.Type != "frame" || !m.Full || !strings.Contains(string(m.Data), "READY") {
			t.Fatalf("screen: %+v %v", m, err)
		}
		conn.WriteJSON(protocol.Message{Type: "frame-ack", Revision: m.Revision})
		if err := conn.ReadJSON(&m); err != nil || m.Type != "history-end" {
			t.Fatalf("ended: %+v %v", m, err)
		}
		conn.Close()
	}
	after, err := recordedEvents(t, store, id)
	if err != nil || len(after) != len(before) {
		t.Fatalf("history reload changed recording: %v", err)
	}
}
