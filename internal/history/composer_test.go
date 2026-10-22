package history

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestSaveDraftLatestWinsButKeepsSends(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.CreateSession("Drafts", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	save := func(base int64, text string, pending bool) (int64, error) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"text": text, "attachments": []int64{}, "pending": pending})
		if err != nil {
			t.Fatal(err)
		}
		return s.SaveDraft(session.ID, base, data)
	}
	must := func(revision int64, err error) int64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	latest := func() string {
		t.Helper()
		event, err := s.LatestDraft(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		var draft struct{ Text string }
		if err = json.Unmarshal(event.Data, &draft); err != nil {
			t.Fatal(err)
		}
		return draft.Text
	}
	first := must(save(0, "first", false))
	must(save(first, "second", false))
	// A view that loaded an older revision still saves. The latest save wins.
	stale := must(save(first, "latest", false))
	if latest() != "latest" {
		t.Fatalf("latest save lost: %q", latest())
	}
	for _, base := range []int64{-1, stale + 100} {
		if _, err = save(base, "invalid", false); err == nil || errors.Is(err, ErrDraftSent) {
			t.Fatalf("accepted base %d: %v", base, err)
		}
	}
	// A pending revision records a send. A view with an older base must not replace it.
	pending := must(save(stale, "sending", true))
	if _, err = save(stale, "stale edit", false); !errors.Is(err, ErrDraftSent) {
		t.Fatalf("stale save replaced a send: %v", err)
	}
	// The sending view clears its own pending draft.
	cleared := must(save(pending, "", false))
	// After the send, an old view must not bring back the sent text.
	for _, base := range []int64{stale, pending} {
		if _, err = save(base, "sending", false); !errors.Is(err, ErrDraftSent) {
			t.Fatalf("base %d brought back sent text: %v", base, err)
		}
	}
	if latest() != "" {
		t.Fatalf("cleared draft changed: %q", latest())
	}
	must(save(cleared, "next message", false))
	var revisions int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM events WHERE session_id=? AND kind='draft'", session.ID).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 6 {
		t.Fatalf("archive kept %d draft revisions; want 6", revisions)
	}
}
