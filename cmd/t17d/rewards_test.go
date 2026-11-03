package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	emit "github.com/openpak/rewards-emit"
)

func TestRewardSessions(t *testing.T) {
	var mu sync.Mutex
	var events []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		events = append(events, body.Events...)
		mu.Unlock()
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	rw := emit.New(emit.Config{URL: srv.URL, Key: "k", Source: "t17-test", Flush: 10 * time.Millisecond})

	s := &server{}
	a, b := globalID("aaaaaaaaaaaa0001"), globalID("bbbbbbbbbbbb0002")
	s.accounts.set(a, "aaaaaaaaaaaa0001")
	s.accounts.set(b, "bbbbbbbbbbbb0002")
	s.conns.add(a, &conn{})
	s.sessions.put(&session{ID: "s-1", Owner: a, Players: []string{a}})

	sess := emit.NewSessions(rw, "game_sessions.moving-out-2", 0)
	now := time.Now()
	if n := sess.Tick(now, s.rewardRooms()); n != 0 {
		t.Fatalf("owner alone emitted %d", n)
	}
	// b is in the session but offline: not counted.
	s.sessions.join("s-1", b)
	if n := sess.Tick(now.Add(5*time.Minute), s.rewardRooms()); n != 0 {
		t.Fatalf("an offline member counted: %d", n)
	}
	s.conns.add(b, &conn{})
	if n := sess.Tick(now.Add(6*time.Minute), s.rewardRooms()); n != 0 {
		t.Fatalf("clock must start when the pair is connected, emitted %d", n)
	}
	if n := sess.Tick(now.Add(9*time.Minute+time.Second), s.rewardRooms()); n != 2 {
		t.Fatalf("expected 2 events, got %d", n)
	}
	if n := sess.Tick(now.Add(12*time.Minute), s.rewardRooms()); n != 0 {
		t.Fatalf("repeat emitted %d", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rw.Close(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("rewards got %d events", len(events))
	}
	for _, e := range events {
		if e["metric"] != "game_sessions.moving-out-2" {
			t.Errorf("metric %v", e["metric"])
		}
	}
}

func TestLeftSessionDoesNotCount(t *testing.T) {
	s := &server{}
	a, b := globalID("aaaaaaaaaaaa0001"), globalID("bbbbbbbbbbbb0002")
	s.accounts.set(a, "aaaaaaaaaaaa0001")
	s.accounts.set(b, "bbbbbbbbbbbb0002")
	s.conns.add(a, &conn{})
	s.conns.add(b, &conn{})
	s.sessions.put(&session{ID: "s-1", Owner: a, Players: []string{a, b}})
	s.sessions.leave("s-1", b)
	if r := s.rewardRooms(); len(r) != 1 || len(r[0].Players) != 1 {
		t.Fatalf("rooms %+v", r)
	}
}
