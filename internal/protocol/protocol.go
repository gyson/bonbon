// Package protocol defines messages between BonBon's local clients and server.
package protocol

import (
	"bonbon/internal/history"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const Version = "bonbon/20"
const Path = "/ws"
const maxMessage = 16 << 20

type Size struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

type Run struct {
	Command   string `json:"command,omitempty"`
	Workspace string `json:"workspace"`
	Title     string `json:"title"`
	Size      Size   `json:"size"`
}

// The server owns command and workspace snapshots. Forms cannot supply these fields.
type PreparationConfig struct {
	Revision int64  `json:"revision"`
	ToolID   string `json:"toolId"`
	Worktree bool   `json:"worktree"`
	Base     string `json:"base"`
	Branch   string `json:"branch"`
}

type Request struct {
	Archived    bool               `json:"archived,omitempty"`
	Offset      int                `json:"offset,omitempty"`
	Query       string             `json:"query,omitempty"`
	Settings    *history.Settings  `json:"settings,omitempty"`
	Preparation *PreparationConfig `json:"preparation,omitempty"`
	Revision    int64              `json:"revision,omitempty"`
	Project     string             `json:"project,omitempty"`
	Name        string             `json:"name,omitempty"`
	Workspace   string             `json:"workspace,omitempty"`
	Protocol    string             `json:"protocol"`
	Operation   string             `json:"operation"`
	Instance    string             `json:"instance,omitempty"`
	SQL         string             `json:"sql,omitempty"`
	Session     string             `json:"session,omitempty"`
	LatestRun   string             `json:"latestRun,omitempty"` // The latest run that the view saw.
	Run         string             `json:"run,omitempty"`       // An earlier run to show. Empty shows the latest run.
	Tool        string             `json:"tool,omitempty"`      // A tool ID from Settings. Empty selects Shell.
	Limit       int                `json:"limit,omitempty"`
	Size        Size               `json:"size,omitempty"`
	Draft       *Draft             `json:"draft,omitempty"`
	Upload      *Upload            `json:"upload,omitempty"`

	Search  *history.SearchFilter   `json:"search,omitempty"`
	Context *history.ContextRequest `json:"context,omitempty"`
}

// Drafts contain editable text and attachments. They are not agent messages. Pending
// means that an input write may have occurred. Clients must never retry it
// automatically.
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

// Composer holds a whole draft and its attachment metadata. Rejected is set only in a
// save reply. It reports that the server kept a send from another view and did not
// store the save. The reply then holds the latest draft.
type Composer struct {
	Draft       Draft        `json:"draft"`
	Attachments []Attachment `json:"attachments"`
	Rejected    bool         `json:"rejected,omitempty"`
}

// SentMessage is one send from the message editor. It is derived from the pending draft
// revision that the editor saves before it writes input. Confirmed reports that the
// editor got an input receipt and then cleared the draft. A receipt does not confirm
// that the application accepted the message.
type SentMessage struct {
	Revision    int64        `json:"revision"`
	Created     string       `json:"created"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments"`
	Confirmed   bool         `json:"confirmed"`
}

// SentPage lists sent messages, newest first. More reports older messages.
type SentPage struct {
	Messages []SentMessage `json:"messages"`
	More     bool          `json:"more"`
}

// Handoff identifies the folder that BonBon writes before it starts a new terminal in
// an ended session, and the prompt that points a new agent to it. Available reports
// that the folder has a README, so the prompt can be used now.
type Handoff struct {
	Folder    string `json:"folder"`
	Prompt    string `json:"prompt"`
	Available bool   `json:"available"`
}

// Terminal is one terminal of a session. Each terminal is one run. Number counts from 1
// in start order. A new terminal has no run ID until its process starts. Tool is empty
// when the start event of an older run has no tool.
type Terminal struct {
	Number  int    `json:"number"`
	Run     string `json:"run"`
	Tool    string `json:"tool"`
	Status  string `json:"status"`
	Started string `json:"started"`
	Ended   string `json:"ended"`
}

type SessionInfo struct {
	Archived    bool                 `json:"archived"`
	Preparation *history.Preparation `json:"preparation,omitempty"`
	Worktree    *history.Worktree    `json:"worktree,omitempty"`
	ProjectID   string               `json:"projectId"`
	ID          string               `json:"id"`
	Title       string               `json:"title"`
	Workspace   string               `json:"workspace"`
	Updated     string               `json:"updated"`
	Status      string               `json:"status"`
	Viewers     int                  `json:"viewers"`
}

type ServerInfo struct {
	Protocol   string `json:"protocol"`
	Instance   string `json:"instance"`
	PID        int    `json:"pid"`
	DataDir    string `json:"dataDir"`
	Port       int    `json:"port"`
	Executable string `json:"executable"`
}

// Message carries one request, reply, or terminal event. Set only the fields that its
// Type needs. Input bytes and rendered frames use base64 in JSON.
type Message struct {
	Type     string          `json:"type"`
	Revision int64           `json:"revision,omitempty"`
	Full     bool            `json:"full,omitempty"`
	ID       string          `json:"id,omitempty"`
	Request  *Request        `json:"request,omitempty"`
	Server   *ServerInfo     `json:"server,omitempty"`
	Session  string          `json:"session,omitempty"`
	Active   bool            `json:"active,omitempty"`
	Status   string          `json:"status,omitempty"`
	Data     []byte          `json:"data,omitempty"`
	Size     Size            `json:"size,omitempty"`
	Signal   int             `json:"signal,omitempty"`
	Code     int             `json:"code,omitempty"`
	Error    string          `json:"error,omitempty"`
	Composer *Composer       `json:"composer,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
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
	// Try to send a close frame so browser peers see a normal close. Keep the timeout
	// short. Slow peers must not delay recording or process cleanup.
	c.socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(100*time.Millisecond))
	return c.socket.Close()
}

// Set deadlines on the underlying socket. Then connection shutdown can interrupt
// Receive without concurrent calls to the WebSocket reader's methods.
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
