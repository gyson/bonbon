package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		if err != nil {
			return result, err
		}
	}
	return result, nil
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
