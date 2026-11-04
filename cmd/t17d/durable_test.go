package main

import (
	"context"
	"os"
	"testing"
)

// Needs a scratch Postgres: T17_TEST_DATABASE_URL; skipped otherwise.
func TestStorageAndInvitesSurviveRestart(t *testing.T) {
	url := os.Getenv("T17_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("T17_TEST_DATABASE_URL unset")
	}
	ctx := context.Background()
	boot := func(pre func(*server)) (*server, *pgStore) {
		s := &server{}
		if pre != nil {
			pre(s) // writes made before the database was reachable
		}
		db, err := openStore(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.storage.attach(ctx, db.pool); err != nil {
			t.Fatal(err)
		}
		if err := s.sessions.attach(ctx, db.pool); err != nil {
			t.Fatal(err)
		}
		return s, db
	}
	clean := func(db *pgStore) {
		db.pool.Exec(ctx, `DELETE FROM t17_storage WHERE owner LIKE 'gt-%'`)
		db.pool.Exec(ctx, `DELETE FROM t17_invites WHERE invitee LIKE 'gt-%'`)
	}
	key := storageKey(map[string]any{"gameId": "g", "slot": "RecentlyMet"}, "gt-a")
	a, db := boot(func(s *server) { s.storage.put(storageKey(map[string]any{"gameId": "g", "slot": "early"}, "gt-a"), "E") })
	clean(db)
	a.storage.put(storageKey(map[string]any{"gameId": "g", "slot": "early"}, "gt-a"), "E")
	a.storage.put(key, "blob1")
	a.storage.put(key, "blob2")
	a.sessions.addInvite(invite{SessionID: "s1", Inviter: "gt-a", Invitee: "gt-b", Type: "x", Platforms: []any{"switch"}})
	a.sessions.addInvite(invite{SessionID: "s1", Inviter: "gt-a", Invitee: "gt-b", Type: "x"}) // duplicate
	a.sessions.addInvite(invite{SessionID: "s2", Inviter: "gt-a", Invitee: "gt-b", Type: "y"})
	db.pool.Close()

	b, db2 := boot(nil) // a restart: nothing in memory
	defer db2.pool.Close()
	defer clean(db2)
	if v, ok := b.storage.get(key); !ok || v != "blob2" {
		t.Fatalf("storage after restart: %q %v", v, ok)
	}
	if _, ok := b.storage.get(storageKey(map[string]any{"gameId": "g", "slot": "early"}, "gt-a")); !ok {
		t.Fatal("pre-attach write lost")
	}
	list := b.sessions.invitesFor("gt-b")
	if len(list) != 2 || list[0].SessionID != "s1" || list[1].SessionID != "s2" || list[0].Inviter != "gt-a" {
		t.Fatalf("invites after restart: %+v", list)
	}
	if in, ok := b.sessions.takeInvite("gt-b", "s1"); !ok || in.Type != "x" {
		t.Fatalf("take: %+v %v", in, ok)
	}
	if got := b.sessions.invitesFor("gt-b"); len(got) != 1 {
		t.Fatalf("after take: %+v", got)
	}
	if n, err := eraseDurable(ctx, db2.pool, "gt-a"); err != nil || n != 3 {
		t.Fatalf("erase: %d %v", n, err)
	}
	if _, ok := b.storage.get(key); ok {
		t.Fatal("storage survived erasure")
	}
}
