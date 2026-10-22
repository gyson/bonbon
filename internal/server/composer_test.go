package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
)

func composerServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := history.Open(filepath.Join(dir, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session, err := store.CreateSession("Composer fixture", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: store, info: protocol.ServerInfo{DataDir: dir}}, session.ID
}

func TestComposerPersistsDraftAndOriginalFileWithoutCache(t *testing.T) {
	s, id := composerServer(t)
	data := []byte{0, 255, 27, 13, 10, 195, 169}
	file, err := s.upload(id, &protocol.Upload{Name: "example's file.bin", MediaType: "application/octet-stream", Data: data})
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.composer(id, &protocol.Draft{Text: "Review café\nsecond line", Attachments: []int64{file.ID}, Pending: true})
	if err != nil || state.Draft.Revision == 0 {
		t.Fatalf("save: %+v, %v", state, err)
	}
	stat, err := os.Stat(file.Path)
	if err != nil || stat.Mode().Perm() != 0400 {
		t.Fatalf("file permissions: %v, %v", stat, err)
	}
	if err = os.RemoveAll(filepath.Join(s.info.DataDir, "attachment-cache")); err != nil {
		t.Fatal(err)
	}
	if err = s.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(s.store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.store = store
	restored, err := s.composer(id, nil)
	if err != nil || !reflect.DeepEqual(restored, state) {
		t.Fatalf("draft changed across restart: %+v, %v", restored, err)
	}
	got, err := os.ReadFile(restored.Attachments[0].Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("lost original file: %x, %v", got, err)
	}
	if events, err := s.store.TerminalPage(id, 0, restored.Draft.Revision); err != nil || len(events) != 0 {
		t.Fatal("draft or upload entered terminal replay", events, err)
	}
	cleared, err := s.composer(id, &protocol.Draft{Revision: restored.Draft.Revision, Attachments: []int64{}})
	if err != nil || cleared.Draft.Pending || cleared.Draft.Text != "" {
		t.Fatalf("clear: %+v, %v", cleared, err)
	}
	if evidence, err := s.store.Attachment(id, file.ID); err != nil || !bytes.Equal(evidence.Data, data) {
		t.Fatal("clearing draft deleted original evidence", err)
	}
}

func TestComposerLatestSaveWinsAndKeepsSendsAndSessionFiles(t *testing.T) {
	s, id := composerServer(t)
	file, err := s.upload(id, &protocol.Upload{Name: "fixture.txt", Data: []byte("file")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.store.CreateSession("Other", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.composer(other.ID, &protocol.Draft{Attachments: []int64{file.ID}}); err == nil {
		t.Fatal("accepted another session's file")
	}
	if _, err = s.composer(id, &protocol.Draft{Attachments: []int64{file.ID, file.ID}}); err == nil {
		t.Fatal("accepted duplicate file")
	}
	if _, err = s.composer("missing", nil); err == nil {
		t.Fatal("accepted missing session")
	}
	// Two views save from the same base. Both saves are stored, and the latest wins.
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.composer(id, &protocol.Draft{Text: "concurrent edit"}); results <- err }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	base, err := s.composer(id, nil)
	if err != nil || base.Draft.Text != "concurrent edit" {
		t.Fatalf("latest save: %+v, %v", base, err)
	}
	sending, err := s.composer(id, &protocol.Draft{Revision: base.Draft.Revision, Text: "sending", Pending: true})
	if err != nil || sending.Rejected {
		t.Fatalf("pending save: %+v, %v", sending, err)
	}
	// A stale save gets the latest draft back instead of replacing the send.
	stale, err := s.composer(id, &protocol.Draft{Revision: base.Draft.Revision, Text: "stale edit"})
	if err != nil || !stale.Rejected || !stale.Draft.Pending || stale.Draft.Text != "sending" || stale.Draft.Revision != sending.Draft.Revision {
		t.Fatalf("stale save replaced a send: %+v, %v", stale, err)
	}
}

func TestComposerWatchPushesNewestDraft(t *testing.T) {
	s, id := composerServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, peer := attachmentPair(t)
	done := make(chan struct{})
	go func() { defer close(done); s.watchComposer(ctx, conn, id) }()
	read := func() protocol.Message {
		t.Helper()
		peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		var m protocol.Message
		if err := peer.ReadJSON(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := read(); m.Type != "draft" || m.Session != id || m.Composer == nil || m.Composer.Draft.Revision != 0 || m.Composer.Draft.Attachments == nil {
		t.Fatalf("initial draft: %+v", m)
	}
	saved, err := s.composer(id, &protocol.Draft{Text: "from another view"})
	if err != nil {
		t.Fatal(err)
	}
	if m := read(); m.Type != "draft" || m.Composer.Draft.Revision != saved.Draft.Revision || m.Composer.Draft.Text != "from another view" {
		t.Fatalf("pushed draft: %+v", m)
	}
	// Quick saves can combine. A view never receives an older revision after a newer one.
	for i := range 3 {
		if saved, err = s.composer(id, &protocol.Draft{Revision: saved.Draft.Revision, Text: fmt.Sprint("edit ", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for last := int64(0); last != saved.Draft.Revision; {
		m := read()
		if m.Type != "draft" || m.Composer.Draft.Revision <= last {
			t.Fatalf("old or unexpected push: %+v", m)
		}
		last = m.Composer.Draft.Revision
	}
	if saved, err = s.composer(id, &protocol.Draft{Revision: saved.Draft.Revision, Text: "sending", Pending: true}); err != nil {
		t.Fatal(err)
	}
	if m := read(); !m.Composer.Draft.Pending {
		t.Fatalf("pending draft was not pushed: %+v", m)
	}
	// A rejected save changes nothing, so it pushes nothing. The next message is pong.
	if stale, err := s.composer(id, &protocol.Draft{Revision: saved.Draft.Revision - 1, Text: "stale"}); err != nil || !stale.Rejected {
		t.Fatalf("stale save: %+v, %v", stale, err)
	}
	if err = peer.WriteJSON(protocol.Message{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	if m := read(); m.Type != "pong" {
		t.Fatalf("expected pong: %+v", m)
	}
	// Server shutdown ends the watch and removes it.
	cancel()
	<-done
	s.watchMu.Lock()
	remaining := len(s.watchers)
	s.watchMu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d sessions still have watchers", remaining)
	}
	missingConn, missingPeer := attachmentPair(t)
	go s.watchComposer(context.Background(), missingConn, "missing")
	missingPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m protocol.Message
	if err = missingPeer.ReadJSON(&m); err != nil || m.Type != "error" {
		t.Fatalf("watch accepted a missing session: %+v, %v", m, err)
	}
}

func TestComposerAutosaveLeavesCacheAloneAndSubmitRestoresIt(t *testing.T) {
	s, id := composerServer(t)
	file, err := s.upload(id, &protocol.Upload{Name: "fixture.txt", Data: []byte("original")})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	state, err := s.composer(id, &protocol.Draft{Text: "editing", Attachments: []int64{file.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("autosave rewrote cache", err)
	}
	state.Draft.Pending = true
	if _, err = s.composer(id, &state.Draft); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file.Path); err != nil || string(data) != "original" {
		t.Fatal("submission did not restore file", err)
	}
}

func TestComposerRejectsInvalidUploadsAndEscapingCache(t *testing.T) {
	s, id := composerServer(t)
	for _, name := range []string{"", ".", "..", "../outside", "/outside", `dir\file`, "escape\x1b.bin", "line\nfile"} {
		if _, err := s.upload(id, &protocol.Upload{Name: name}); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := s.upload(id, &protocol.Upload{Name: "large", Data: make([]byte, maxAttachment+1)}); err == nil {
		t.Fatal("accepted oversized file")
	}
	if _, err := s.composer(id, &protocol.Draft{Text: strings.Repeat("x", maxDraft+1)}); err == nil {
		t.Fatal("accepted oversized draft")
	}
	if _, err := s.composer(id, &protocol.Draft{Attachments: make([]int64, 9)}); err == nil {
		t.Fatal("accepted too many files")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(s.info.DataDir, "attachment-cache")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upload(id, &protocol.Upload{Name: "escape.txt", Data: []byte("data")}); err == nil {
		t.Fatal("cache escaped instance")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("modified outside cache root", entries, err)
	}
}

func TestSentMessagesDeriveDeliveryFromNextRevision(t *testing.T) {
	s, id := composerServer(t)
	file, err := s.upload(id, &protocol.Upload{Name: "notes.txt", MediaType: "text/plain", Data: []byte("notes")})
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	save := func(draft protocol.Draft) {
		t.Helper()
		draft.Revision = revision
		state, err := s.composer(id, &draft)
		if err != nil || state.Rejected {
			t.Fatalf("save %+v: %+v, %v", draft, state, err)
		}
		revision = state.Draft.Revision
	}
	// A send with a receipt clears the draft.
	save(protocol.Draft{Text: "first", Attachments: []int64{file.ID}, Pending: true})
	save(protocol.Draft{})
	// The user unlocked this send without a receipt.
	save(protocol.Draft{Text: "second", Pending: true})
	save(protocol.Draft{Text: "second"})
	// This send still waits for a check of the terminal.
	save(protocol.Draft{Text: "third", Pending: true})

	page, err := s.sentMessages(id, 0)
	if err != nil || page.More || len(page.Messages) != 3 {
		t.Fatalf("sent messages: %+v, %v", page, err)
	}
	got := []string{}
	for _, message := range page.Messages {
		got = append(got, fmt.Sprintf("%s %t %d", message.Text, message.Confirmed, len(message.Attachments)))
	}
	if want := []string{"third false 0", "second false 0", "first true 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sent messages = %q, want %q", got, want)
	}
	if attachment := page.Messages[2].Attachments[0]; attachment.ID != file.ID || attachment.Name != "notes.txt" || attachment.Path != file.Path {
		t.Fatalf("attachment metadata: %+v", attachment)
	}
	older, err := s.sentMessages(id, page.Messages[1].Revision)
	if err != nil || len(older.Messages) != 1 || older.Messages[0].Text != "first" || !older.Messages[0].Confirmed {
		t.Fatalf("older page: %+v, %v", older, err)
	}
	for i := 0; i < sentPage; i++ {
		save(protocol.Draft{Text: fmt.Sprint("bulk ", i), Pending: true})
		save(protocol.Draft{})
	}
	if page, err = s.sentMessages(id, 0); err != nil || !page.More || len(page.Messages) != sentPage || page.Messages[0].Text != fmt.Sprint("bulk ", sentPage-1) {
		t.Fatalf("full page: more %v, %d messages, %v", page.More, len(page.Messages), err)
	}
	if _, err = s.sentMessages(id, -1); err == nil {
		t.Fatal("accepted a negative revision")
	}
	if _, err = s.sentMessages("missing", 0); err == nil {
		t.Fatal("listed sent messages for a missing session")
	}
}
