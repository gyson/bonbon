// Package protocol defines BonBon's local client/server messages.
package protocol

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const Version = "bonbon/11"
const Path = "/ws"
const maxMessage = 16 << 20

type Size struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

type Run struct {
	Workspace string `json:"workspace"`
	Title     string `json:"title"`
	Size      Size   `json:"size"`
}

type Request struct {
	Protocol  string  `json:"protocol"`
	Operation string  `json:"operation"`
	Instance  string  `json:"instance,omitempty"`
	Run       *Run    `json:"run,omitempty"`
	SQL       string  `json:"sql,omitempty"`
	Session   string  `json:"session,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Size      Size    `json:"size,omitempty"`
	Draft     *Draft  `json:"draft,omitempty"`
	Upload    *Upload `json:"upload,omitempty"`
}

// Drafts are editable composition state, not agent messages. Pending means that
// an input write may have happened; clients must never retry it automatically.
type Draft struct {
	Revision    int64   `json:"revision"`
	Text        string  `json:"text"`
	Attachments []int64 `json:"attachments"`
	Pending     bool    `json:"pending"`
}

type Upload struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Data      []byte `json:"data"`
}

type Attachment struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
	Path      string `json:"path"`
}

type Composer struct {
	Draft       Draft        `json:"draft"`
	Attachments []Attachment `json:"attachments"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	Updated   string `json:"updated"`
	Status    string `json:"status"`
	Viewers   int    `json:"viewers"`
}

type ServerInfo struct {
	Protocol   string `json:"protocol"`
	Instance   string `json:"instance"`
	PID        int    `json:"pid"`
	DataDir    string `json:"dataDir"`
	Port       int    `json:"port"`
	Executable string `json:"executable"`
}

// Message carries one request, reply, or terminal event. Only the fields needed
// for its Type are populated. Input bytes and rendered frames use JSON/base64.
type Message struct {
	Type        string          `json:"type"`
	Revision    int64           `json:"revision,omitempty"`
	Full        bool            `json:"full,omitempty"`
	ID          string          `json:"id,omitempty"`
	Request     *Request        `json:"request,omitempty"`
	Server      *ServerInfo     `json:"server,omitempty"`
	Session     string          `json:"session,omitempty"`
	Active      bool            `json:"active,omitempty"`
	Status      string          `json:"status,omitempty"`
	Data        []byte          `json:"data,omitempty"`
	Size        Size            `json:"size,omitempty"`
	Signal      int             `json:"signal,omitempty"`
	Code        int             `json:"code,omitempty"`
	Error       string          `json:"error,omitempty"`
	Controlling bool            `json:"controlling,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type Conn struct {
	socket *websocket.Conn
	mu     sync.Mutex
}

func Wrap(socket *websocket.Conn) *Conn {
	socket.SetReadLimit(maxMessage)
	return &Conn{socket: socket}
}

func (c *Conn) Close() error {
	// Send a best-effort close frame so browser peers see a normal close.
	// Keep this short: slow peers must not delay recording or process cleanup.
	c.socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(100*time.Millisecond))
	return c.socket.Close()
}

// Deadlines belong to the underlying socket so connection shutdown can interrupt
// Receive without concurrently calling the WebSocket reader's methods.
func (c *Conn) SetReadDeadline(deadline time.Time) error {
	return c.socket.NetConn().SetReadDeadline(deadline)
}

// Send permits output and terminal control to share a connection. A stalled
// peer cannot block process cleanup indefinitely.
func (c *Conn) Send(message Message) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(data) > maxMessage {
		return errors.New("protocol message exceeds 16 MiB")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.socket.WriteMessage(websocket.TextMessage, data)
}

func (c *Conn) Receive() (Message, error) {
	kind, data, err := c.socket.ReadMessage()
	if err != nil {
		return Message{}, err
	}
	if kind != websocket.TextMessage {
		return Message{}, errors.New("protocol requires JSON text messages")
	}
	var message *Message
	err = json.Unmarshal(data, &message)
	if err != nil {
		return Message{}, err
	}
	if message == nil {
		return Message{}, errors.New("protocol requires a JSON object")
	}
	return *message, nil
}
