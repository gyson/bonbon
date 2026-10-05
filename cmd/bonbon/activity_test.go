package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bonbon/internal/protocol"
)

func TestSessionViewsDoNotChangeActivityOrder(t *testing.T) {
	directory := t.TempDir()
	startTestServer(t, directory)
	store := archive(t, directory)
	trigger := filepath.Join(t.TempDir(), "emit")
	first := launchAgent(t, t.TempDir(), "activity", trigger)
	waitFor(t, func() bool { return strings.Contains(first.text(), "READY") })
	firstID := sessionInfos(t, 10)[0].ID
	first.Close()
	waitFor(t, func() bool { return sessionInfos(t, 10)[0].Viewers == 0 })
	second := newShell(t, t.TempDir(), "more recent")
	before := sessionInfos(t, 10)
	if len(before) != 2 || before[0].ID == firstID {
		t.Fatalf("newer shell is not first: %+v", before)
	}
	resumed := resumeView(t, firstID)
	waitFor(t, func() bool { return strings.Contains(resumed.text(), "READY") })
	// Both control transfer and an unchanged viewport must remain display-only.
	for _, kind := range []string{"take-control", "resize"} {
		if err := resumed.conn.Send(protocol.Message{Type: kind, Size: protocol.Size{Rows: 24, Cols: 80}}); err != nil {
			t.Fatal(err)
		}
	}
	// Allow the runtime to process controls and any accidental SIGWINCH output.
	time.Sleep(250 * time.Millisecond)
	after := sessionInfos(t, 10)
	if len(after) != len(before) {
		t.Fatalf("viewing changed session count: %+v", after)
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].Updated != before[i].Updated {
			t.Fatalf("viewing changed activity order: before=%+v after=%+v", before, after)
		}
	}
	events, err := recordedEvents(t, store, firstID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == "resize" {
			t.Fatal("same-size reattachment recorded a resize")
		}
	}
	// A real dimension change still reaches the PTY and notifies the application.
	if err = resumed.conn.Send(protocol.Message{Type: "resize", Size: protocol.Size{Rows: 30, Cols: 100}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(recordedOutput(t, store, firstID), "RESIZED=30x100") })
	events, err = recordedEvents(t, store, firstID)
	if err != nil {
		t.Fatal(err)
	}
	resizes := 0
	for _, event := range events {
		if event.Kind == "resize" {
			resizes++
		}
	}
	if resizes != 1 {
		t.Fatalf("recorded %d resizes; want one actual size change", resizes)
	}
	resumed.Close()
	waitFor(t, func() bool {
		for _, info := range sessionInfos(t, 10) {
			if info.ID == firstID {
				return info.Viewers == 0
			}
		}
		return false
	})
	if _, err = second.Write([]byte("printf 'newer-%s\\n' activity\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(second.text(), "newer-activity") })
	if sessionInfos(t, 10)[0].ID == firstID {
		t.Fatal("terminal interaction did not promote the second session")
	}
	if err = os.WriteFile(trigger, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Contains(recordedOutput(t, store, firstID), "BACKGROUND_ACTIVITY") })
	if sessionInfos(t, 10)[0].ID != firstID {
		t.Fatal("detached terminal output did not promote the first session")
	}
}
