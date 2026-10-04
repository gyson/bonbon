package server

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	session, err := store.CreateSession("Composer fixture", t.TempDir())
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

func TestComposerRejectsStaleSavesAndCrossSessionFiles(t *testing.T) {
	s, id := composerServer(t)
	file, err := s.upload(id, &protocol.Upload{Name: "fixture.txt", Data: []byte("file")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.store.CreateSession("Other", t.TempDir())
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
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.composer(id, &protocol.Draft{Text: "concurrent edit"}); results <- err }()
	}
	var successes, conflicts int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, history.ErrDraftConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("lost update: %d successes, %d conflicts", successes, conflicts)
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
