package history

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func searchRun(t *testing.T, s *Store, sid string) *Run {
	t.Helper()
	run, err := s.NewRun(sid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Append(sid, run.ID, "start", []byte(`{"tool":"Fixture"}`), ""); err != nil {
		t.Fatal(err)
	}
	return run
}

func searchOutput(t *testing.T, s *Store, run *Run, text string) Event {
	t.Helper()
	e, err := s.Append(run.SessionID, run.ID, "output", []byte(text), text)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestHistorySearchScopesOldestFirstAndStablePagination(t *testing.T) {
	s := testStore(t)
	project, err := s.CreateProject("Example", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	own, err := s.CreateSession("Own", t.TempDir(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := s.CreateSession("Sibling", t.TempDir(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	outside := create(t, s)
	first := searchRun(t, s, own.ID)
	a := searchOutput(t, s, first, "migration first")
	other := searchRun(t, s, sibling.ID)
	b := searchOutput(t, s, other, "migration sibling")
	second := searchRun(t, s, own.ID)
	c := searchOutput(t, s, second, "migration later terminal")
	foreign := searchRun(t, s, outside.ID)
	d := searchOutput(t, s, foreign, "migration outside")
	if _, err = s.db.Exec("UPDATE sessions SET archived=1 WHERE id=?", sibling.ID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		filter SearchFilter
		want   []int64
	}{
		{SearchFilter{Query: "migration", Session: own.ID}, []int64{a.Seq, c.Seq}},
		{SearchFilter{Query: "migration", Scope: "project", Project: project.ID}, []int64{a.Seq, b.Seq, c.Seq}},
		{SearchFilter{Query: "migration", Scope: "all"}, []int64{a.Seq, b.Seq, c.Seq, d.Seq}},
	} {
		page, err := s.Search(context.Background(), test.filter)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for _, hit := range page.Matches {
			got = append(got, hit.Anchor.Seq)
		}
		if !reflect.DeepEqual(got, test.want) || page.More {
			t.Fatal(got, page.More, test.want)
		}
	}
	filter := SearchFilter{Query: "migration", Scope: "all", Limit: 1}
	page, err := s.Search(context.Background(), filter)
	if err != nil || len(page.Matches) != 1 || !page.More || page.Matches[0].Anchor.Seq != a.Seq {
		t.Fatal(page, err)
	}
	searchOutput(t, s, second, "migration after snapshot")
	filter.Through = page.Through
	var got []int64
	for {
		page, err = s.Search(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range page.Matches {
			got = append(got, hit.Anchor.Seq)
		}
		if !page.More {
			break
		}
		filter.Offset += len(page.Matches)
	}
	if !reflect.DeepEqual(got, []int64{a.Seq, b.Seq, c.Seq, d.Seq}) {
		t.Fatal(got)
	}
	if page.Matches[0].Tool != "Fixture" || page.Matches[0].Terminal != 1 {
		t.Fatal(page.Matches)
	}
}

func TestHistorySearchCrossesChunksWithUnicodeAndInterleavedRuns(t *testing.T) {
	s := testStore(t)
	own := create(t, s)
	other := create(t, s)
	run := searchRun(t, s, own.ID)
	outside := searchRun(t, s, other.ID)
	searchOutput(t, s, run, "before ")
	start := searchOutput(t, s, run, "ca")
	searchOutput(t, s, outside, "not part of this match")
	searchOutput(t, s, run, "")
	end := searchOutput(t, s, run, "fé after 100%_ <script>text</script>")
	page, err := s.Search(context.Background(), SearchFilter{Query: "CAFÉ", Session: own.ID})
	if err != nil || len(page.Matches) != 1 {
		t.Fatal(page, err)
	}
	hit := page.Matches[0]
	if hit.Anchor.Seq != start.Seq || hit.Context.LastSeq != end.Seq || hit.Context.Match != "café" || hit.Context.Before != "before " || hit.Terminal != 1 {
		t.Fatal(hit)
	}
	for _, text := range []string{"100%_", "<script>", "café after"} {
		page, err = s.Search(context.Background(), SearchFilter{Query: text, Session: own.ID})
		if err != nil || len(page.Matches) != 1 {
			t.Fatal(text, page, err)
		}
	}
	page, err = s.Search(context.Background(), SearchFilter{Query: "CAFÉ", Session: own.ID, MatchCase: true})
	if err != nil || len(page.Matches) != 0 {
		t.Fatal(page, err)
	}
	searchOutput(t, s, run, "neverjoin")
	next := searchRun(t, s, own.ID)
	searchOutput(t, s, next, "acrossterminals")
	page, err = s.Search(context.Background(), SearchFilter{Query: "neverjoinacrossterminals", Session: own.ID})
	if err != nil || len(page.Matches) != 0 {
		t.Fatal("crossed terminals", page, err)
	}
}

func TestHistorySearchContextBoundsScopeAndSnapshot(t *testing.T) {
	s := testStore(t)
	own := create(t, s)
	run := searchRun(t, s, own.ID)
	searchOutput(t, s, run, strings.Repeat("你", 1200))
	searchOutput(t, s, run, "😀needle")
	searchOutput(t, s, run, strings.Repeat("é", 1200))
	page, err := s.Search(context.Background(), SearchFilter{Query: "needle", Session: own.ID})
	if err != nil || len(page.Matches) != 1 {
		t.Fatal(page, err)
	}
	anchor := page.Matches[0].Anchor
	contextRequest := ContextRequest{Anchor: anchor, Before: 5, After: 7}
	text, err := s.SearchContext(context.Background(), contextRequest)
	if err != nil || text.Before != "你你你你😀" || text.Match != "needle" || text.After != strings.Repeat("é", 7) || !text.MoreAfter || !text.MoreBefore {
		t.Fatal(text, err)
	}
	contextRequest.Before, contextRequest.After = 2000, 2000
	searchOutput(t, s, run, "later output must stay outside context")
	text, err = s.SearchContext(context.Background(), contextRequest)
	if err != nil || strings.Contains(text.After, "later") || text.MoreAfter || text.MoreBefore {
		t.Fatal(text, err)
	}
	contextRequest.Anchor.Session = create(t, s).ID
	if _, err = s.SearchContext(context.Background(), contextRequest); err == nil {
		t.Fatal("accepted another session's anchor")
	}
	contextRequest.Anchor = anchor
	contextRequest.Anchor.Run = "other"
	if _, err = s.SearchContext(context.Background(), contextRequest); err == nil {
		t.Fatal("accepted another terminal's anchor")
	}
	contextRequest.Anchor = anchor
	contextRequest.Before = contextRunes + 1
	if _, err = s.SearchContext(context.Background(), contextRequest); err == nil {
		t.Fatal("accepted unbounded context")
	}
}

func TestHistorySearchOverlapsAndOriginalRecordingsSurviveReopen(t *testing.T) {
	s := testStore(t)
	session := create(t, s)
	run := searchRun(t, s, session.ID)
	var parser Text
	for _, raw := range [][]byte{[]byte("\x1b[32mban"), []byte("ana\x1b[0m "), {0xe4, 0xbd}, {0xa0}} {
		if _, err := s.Append(session.ID, run.ID, "output", raw, parser.Push(raw)); err != nil {
			t.Fatal(err)
		}
	}
	before, err := recordedEvents(t, s, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Search(context.Background(), SearchFilter{Query: "ana", Session: session.ID, Limit: 1})
	if err != nil || len(page.Matches) != 1 || !page.More {
		t.Fatal(page, err)
	}
	page, err = s.Search(context.Background(), SearchFilter{Query: "ana", Session: session.ID, Limit: 1, Offset: 1, Through: page.Through})
	if err != nil || len(page.Matches) != 1 || page.More || page.Matches[0].Context.Match != "ana" {
		t.Fatal(page, err)
	}
	reopened, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := recordedEvents(t, reopened, session.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("search changed recording", err)
	}
	page, err = reopened.Search(context.Background(), SearchFilter{Query: "你", Session: session.ID})
	if err != nil || len(page.Matches) != 1 {
		t.Fatal(page, err)
	}
}

func TestHistorySearchRejectsInvalidRequestsAndCancellation(t *testing.T) {
	s := testStore(t)
	session := create(t, s)
	run := searchRun(t, s, session.ID)
	searchOutput(t, s, run, "original")
	for _, filter := range []SearchFilter{
		{Query: ""}, {Query: "x"}, {Query: "x", Scope: "unknown"}, {Query: "x", Scope: "project"},
		{Query: strings.Repeat("x", 257), Scope: "all"}, {Query: "x\x00", Scope: "all"},
		{Query: "x", Scope: "all", Offset: 1}, {Query: "x", Scope: "all", Limit: 51}, {Query: "x", Scope: "all", Through: -1},
	} {
		if _, err := s.Search(context.Background(), filter); err == nil {
			t.Fatalf("accepted %+v", filter)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, SearchFilter{Query: "original", Session: session.ID}); err == nil {
		t.Fatal("ignored cancellation")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Search(ctx, SearchFilter{Query: "original", Session: session.ID}); done <- err }()
	searchOutput(t, s, run, "capture continues")
	if err := <-done; err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
