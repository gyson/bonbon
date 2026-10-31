package server

import (
	"context"
	"testing"
	"time"

	"bonbon/internal/protocol"
)

func TestHistoryReadCancelsWhenClientDisconnects(t *testing.T) {
	conn, peer := attachmentPair(t)
	started, canceled, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s := &Server{}
	go func() {
		defer close(done)
		s.historyRead(context.Background(), conn, func(ctx context.Context) (any, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		})
	}()
	<-started
	peer.Close()
	for _, event := range []chan struct{}{canceled, done} {
		select {
		case <-event:
		case <-time.After(5 * time.Second):
			t.Fatal("disconnected history request did not stop")
		}
	}
}

func TestHistoryReadSendsResultAndStopsDisconnectReader(t *testing.T) {
	conn, peer := attachmentPair(t)
	defer peer.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Server{}).historyRead(context.Background(), conn, func(context.Context) (any, error) { return map[string]bool{"complete": true}, nil })
	}()
	peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	var result protocol.Message
	if err := peer.ReadJSON(&result); err != nil || result.Type != "result" {
		t.Fatal(result, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("history reader did not stop")
	}
}
