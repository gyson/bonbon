package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

const maxAttachment = 4 << 20
const maxDraft = 64 << 10

func (s *Server) composer(sid string, draft *protocol.Draft) (protocol.Composer, error) {
	result := protocol.Composer{Draft: protocol.Draft{Attachments: []int64{}}, Attachments: []protocol.Attachment{}}
	if _, err := s.store.Session(sid); err != nil {
		return result, err
	}
	if draft == nil {
		event, err := s.store.LatestDraft(sid)
		if err != nil {
			return result, err
		}
		if event.Seq != 0 {
			if err = json.Unmarshal(event.Data, &result.Draft); err != nil {
				return result, err
			}
			result.Draft.Revision = event.Seq
		}
	} else {
		result.Draft = *draft
	}
	if result.Draft.Attachments == nil {
		result.Draft.Attachments = []int64{}
	}
	if len(result.Draft.Text) > maxDraft || len(result.Draft.Attachments) > 8 || !utf8.ValidString(result.Draft.Text) {
		return result, errors.New("draft limit is 64 KiB of text and 8 files")
	}
	seen := map[int64]bool{}
	for _, id := range result.Draft.Attachments {
		if seen[id] {
			return result, errors.New("duplicate attachment")
		}
		seen[id] = true
		attachment, err := s.attachment(sid, id, draft == nil || draft.Pending)
		if err != nil {
			return result, err
		}
		result.Attachments = append(result.Attachments, attachment)
	}
	if draft != nil {
		data, err := json.Marshal(result.Draft)
		if err != nil {
			return result, err
		}
		result.Draft.Revision, err = s.store.SaveDraft(sid, draft.Revision, data)
		if errors.Is(err, history.ErrDraftSent) {
			latest, err := s.composer(sid, nil)
			latest.Rejected = true
			return latest, err
		}
		if err != nil {
			return result, err
		}
		s.publishDraft(sid, result)
	}
	return result, nil
}

// sentPage keeps a page below the 16 MiB message limit, also with escaped draft text.
const sentPage = 20

// sentMessages lists sends from the message editor that are older than revision before,
// newest first. The status is derived from the next draft revision. The editor clears
// the draft only after an input receipt. Any other next revision, or none, leaves the
// delivery unconfirmed.
func (s *Server) sentMessages(sid string, before int64) (protocol.SentPage, error) {
	page := protocol.SentPage{Messages: []protocol.SentMessage{}}
	if _, err := s.store.Session(sid); err != nil {
		return page, err
	}
	if before < 0 {
		return page, errors.New("invalid revision")
	}
	drafts, more, err := s.store.SentDrafts(sid, before, sentPage)
	if err != nil {
		return page, err
	}
	for _, sent := range drafts {
		var draft protocol.Draft
		if err = json.Unmarshal(sent.Data, &draft); err != nil {
			return page, fmt.Errorf("read draft revision %d: %w", sent.Seq, err)
		}
		message := protocol.SentMessage{Revision: sent.Seq, Created: sent.Created, Text: draft.Text, Attachments: []protocol.Attachment{}}
		for _, id := range draft.Attachments {
			attachment, err := s.attachment(sid, id, false)
			if err != nil {
				return page, err
			}
			message.Attachments = append(message.Attachments, attachment)
		}
		var next protocol.Draft
		if sent.Next != nil && json.Unmarshal(sent.Next, &next) == nil {
			message.Confirmed = !next.Pending && next.Text == "" && len(next.Attachments) == 0
		}
		page.Messages = append(page.Messages, message)
	}
	page.More = more
	return page, nil
}

// A draft watcher keeps only the newest draft. A slow view never queues old revisions.
type draftWatcher struct {
	mu      sync.Mutex
	latest  protocol.Composer
	changed chan struct{}
}

func (w *draftWatcher) offer(state protocol.Composer) {
	w.mu.Lock()
	if state.Draft.Revision > w.latest.Draft.Revision {
		w.latest = state
	}
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

func (s *Server) publishDraft(sid string, state protocol.Composer) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	for w := range s.watchers[sid] {
		w.offer(state)
	}
}

// watchComposer sends the latest draft of a session, then each newer saved draft. The
// view must send ping at least every 30 seconds. Closing the view only removes the
// watcher.
func (s *Server) watchComposer(ctx context.Context, conn *protocol.Conn, sid string) {
	// A draft that was never saved has revision 0. Start below it.
	w := &draftWatcher{latest: protocol.Composer{Draft: protocol.Draft{Revision: -1}}, changed: make(chan struct{}, 1)}
	s.watchMu.Lock()
	if s.watchers == nil {
		s.watchers = make(map[string]map[*draftWatcher]struct{})
	}
	if s.watchers[sid] == nil {
		s.watchers[sid] = make(map[*draftWatcher]struct{})
	}
	s.watchers[sid][w] = struct{}{}
	s.watchMu.Unlock()
	defer func() {
		s.watchMu.Lock()
		delete(s.watchers[sid], w)
		if len(s.watchers[sid]) == 0 {
			delete(s.watchers, sid)
		}
		s.watchMu.Unlock()
	}()
	// Register before the first read. Then a save during the read is never missed.
	state, err := s.composer(sid, nil)
	if err != nil {
		s.reply(conn, nil, err)
		return
	}
	w.offer(state)
	incoming := make(chan protocol.Message)
	readerDone := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			message, err := conn.Receive()
			if err != nil {
				return
			}
			select {
			case incoming <- message:
			case <-closed:
				return
			}
		}
	}()
	defer func() { close(closed); conn.Close(); <-readerDone }()
	sent := int64(-1)
	for {
		select {
		case <-ctx.Done():
			return
		case <-readerDone:
			return
		case <-w.changed:
			w.mu.Lock()
			next := w.latest
			w.mu.Unlock()
			if next.Draft.Revision > sent {
				if conn.Send(protocol.Message{Type: "draft", Session: sid, Composer: &next}) != nil {
					return
				}
				sent = next.Draft.Revision
			}
		case message := <-incoming:
			if message.Type != "ping" {
				s.reply(conn, nil, errors.New("unsupported draft watch message"))
				return
			}
			if conn.Send(protocol.Message{Type: "pong"}) != nil {
				return
			}
		}
	}
}

func (s *Server) upload(sid string, upload *protocol.Upload) (protocol.Attachment, error) {
	var attachment protocol.Attachment
	if _, err := s.store.Session(sid); err != nil {
		return attachment, err
	}
	if upload == nil || len(upload.Data) > maxAttachment || upload.Name == "" || len(upload.Name) > 255 || len(upload.MediaType) > 200 ||
		!utf8.ValidString(upload.Name) || !utf8.ValidString(upload.MediaType) || strings.ContainsAny(upload.Name, "/\\") || upload.Name == "." || upload.Name == ".." ||
		strings.IndexFunc(upload.Name+upload.MediaType, unicode.IsControl) >= 0 {
		return attachment, errors.New("invalid file name or file larger than 4 MiB")
	}
	attachment = protocol.Attachment{Name: upload.Name, MediaType: upload.MediaType, Size: len(upload.Data)}
	metadata, err := json.Marshal(attachment)
	if err != nil {
		return attachment, err
	}
	event, err := s.store.Append(sid, "", "attachment", upload.Data, string(metadata))
	if err != nil {
		return attachment, err
	}
	return s.attachment(sid, event.Seq, true)
}

func (s *Server) attachment(sid string, id int64, restore bool) (protocol.Attachment, error) {
	var attachment protocol.Attachment
	var event history.Event
	var err error
	if restore {
		event, err = s.store.Attachment(sid, id)
	} else {
		event, err = s.store.AttachmentInfo(sid, id)
	}
	if err != nil {
		return attachment, fmt.Errorf("attachment is not available in this session: %w", err)
	}
	if err = json.Unmarshal([]byte(event.Text), &attachment); err != nil {
		return attachment, err
	}
	attachment.ID = event.Seq
	directory := filepath.Join("attachment-cache", strconv.FormatInt(id, 10))
	relative := filepath.Join(directory, attachment.Name)
	attachment.Path = filepath.Join(s.info.DataDir, relative)
	if !restore {
		return attachment, nil
	}
	// Files are private, disposable copies. SQLite holds the authoritative bytes.
	// Use os.Root so a replaced cache directory cannot redirect writes outside it.
	root, err := os.OpenRoot(s.info.DataDir)
	if err != nil {
		return attachment, err
	}
	defer root.Close()
	if err = root.MkdirAll(directory, 0700); err != nil {
		return attachment, err
	}
	// Keep the agent-facing basename independent of user-supplied path syntax.
	temporary := filepath.Join(directory, "."+history.ID())
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return attachment, err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(event.Data)
	closeErr := file.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return attachment, err
	}
	if err = root.Rename(temporary, relative); err != nil {
		return attachment, err
	}
	return attachment, nil
}
