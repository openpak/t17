package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

type memBacking struct {
	m     map[string]string
	clock []byte
	down  bool
}

func (b *memBacking) upsert(_ context.Context, gid, baas string) error {
	if b.down {
		return errors.New("db down")
	}
	b.m[gid] = baas
	return nil
}
func (b *memBacking) load(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range b.m {
		out[k] = v
	}
	return out, nil
}
func (b *memBacking) saveClock(_ context.Context, _ string, raw []byte) error {
	if b.down {
		return errors.New("db down")
	}
	b.clock = raw
	return nil
}
func (b *memBacking) loadClock(context.Context, string) ([]byte, error) { return b.clock, nil }

func fakeRewards(t *testing.T) (*emit.Client, func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		n += len(body.Events)
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	rw := emit.New(emit.Config{URL: srv.URL, Key: "k", Source: "t17-test", Flush: 10 * time.Millisecond})
	return rw, func() int {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		rw.Close(ctx)
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func TestAccountMapSurvivesRestart(t *testing.T) {
	disk := &memBacking{m: map[string]string{}}
	s1 := &server{}
	if err := s1.connect(context.Background(), disk, emit.NewSessions(nil, sessionMetric, 0), true); err != nil {
		t.Fatal(err)
	}
	gid := globalID("aaaaaaaaaaaa0001")
	s1.accounts.set(gid, "aaaaaaaaaaaa0001")

	s2 := &server{} // restart
	if err := s2.connect(context.Background(), disk, emit.NewSessions(nil, sessionMetric, 0), true); err != nil {
		t.Fatal(err)
	}
	s2.conns.add(gid, &conn{})
	other := globalID("bbbbbbbbbbbb0002")
	s2.accounts.set(other, "bbbbbbbbbbbb0002")
	s2.conns.add(other, &conn{})
	s2.sessions.put(&session{ID: "s-1", Owner: gid, Players: []string{gid, other}})
	r := s2.rewardRooms()
	if len(r) != 1 || len(r[0].Players) != 2 {
		t.Fatalf("accounts not mapped after restart: %+v", r)
	}
}

func TestSessionClockSurvivesRestart(t *testing.T) {
	disk := &memBacking{m: map[string]string{}}
	rw, count := fakeRewards(t)
	room := []emit.Room{{ID: "mo2:s-1", Players: []emit.Identity{{Namespace: "switch", Subject: "a"}, {Namespace: "switch", Subject: "b"}}}}
	now := time.Now()

	s1 := &server{}
	sess1 := emit.NewSessions(rw, sessionMetric, 0)
	if err := s1.connect(context.Background(), disk, sess1, true); err != nil {
		t.Fatal(err)
	}
	sess1.Tick(now, room)
	s1.clock.save(sess1.State())
	sess1.Tick(now.Add(2*time.Minute), room)
	s1.clock.save(sess1.State())

	s2 := &server{}
	sess2 := emit.NewSessions(rw, sessionMetric, 0)
	if err := s2.connect(context.Background(), disk, sess2, true); err != nil {
		t.Fatal(err)
	}
	if n := sess2.Tick(now.Add(2*time.Minute+20*time.Second), room); n != 0 {
		t.Fatalf("emitted early: %d", n)
	}
	if n := sess2.Tick(now.Add(3*time.Minute+5*time.Second), room); n != 2 {
		t.Fatalf("restored clock should finish the session, got %d", n)
	}
	s2.clock.save(sess2.State())

	s3 := &server{}
	sess3 := emit.NewSessions(rw, sessionMetric, 0)
	_ = s3.connect(context.Background(), disk, sess3, true)
	if n := sess3.Tick(now.Add(5*time.Minute), room); n != 0 {
		t.Fatalf("emitted again after restart: %d", n)
	}
	if got := count(); got != 2 {
		t.Fatalf("rewards got %d events", got)
	}
}

func TestStoreDownThenBack(t *testing.T) {
	disk := &memBacking{m: map[string]string{}, down: true}
	s := &server{}
	s.accounts.set("g1", "a1") // not connected yet
	s.accounts.back = disk     // connected but failing
	s.accounts.set("g2", "a2")
	if len(disk.m) != 0 || s.accounts.get("g2") == "" {
		t.Fatal("memory must keep serving while the store is down")
	}
	disk.down = false
	s.accounts.flush(context.Background())
	if len(disk.m) != 2 {
		t.Fatalf("pending entries not written through: %v", disk.m)
	}
}

func TestPostgresRestart(t *testing.T) {
	url := os.Getenv("T17_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("T17_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := openStore(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.pool.Close()
	_, _ = db.pool.Exec(ctx, `DELETE FROM t17_accounts; DELETE FROM t17_session_clock`)
	rw, _ := fakeRewards(t)
	s1 := &server{}
	sess1 := emit.NewSessions(rw, sessionMetric, 0)
	if err := s1.connect(ctx, db, sess1, true); err != nil {
		t.Fatal(err)
	}
	s1.accounts.set("g1", "a1")
	s1.accounts.set("g1", "a1")
	room := []emit.Room{{ID: "x", Players: []emit.Identity{{Namespace: "switch", Subject: "a"}, {Namespace: "switch", Subject: "b"}}}}
	sess1.Tick(time.Now(), room)
	s1.clock.save(sess1.State())

	s2 := &server{}
	sess2 := emit.NewSessions(rw, sessionMetric, 0)
	if err := s2.connect(ctx, db, sess2, true); err != nil {
		t.Fatal(err)
	}
	if s2.accounts.get("g1") != "a1" || len(sess2.State().Since) != 2 {
		t.Fatalf("state lost across restart: %q %+v", s2.accounts.get("g1"), sess2.State())
	}
}
