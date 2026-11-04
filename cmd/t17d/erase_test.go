package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeLinks map[string][]string

func (f fakeLinks) SwitchSubjects(_ context.Context, id string) ([]string, error) { return f[id], nil }

type fakeDB struct{ rows map[string]string }

func (f *fakeDB) deleteAccounts(_ context.Context, baas []string) (int, error) {
	n := 0
	for g, b := range f.rows {
		for _, x := range baas {
			if b == x {
				delete(f.rows, g)
				n++
			}
		}
	}
	return n, nil
}
func (f *fakeDB) upsert(_ context.Context, g, b string) error { f.rows[g] = b; return nil }
func (f *fakeDB) load(context.Context) (map[string]string, error) {
	return f.rows, nil
}

const (
	acctA = "11111111-1111-1111-1111-111111111111"
	acctB = "22222222-2222-2222-2222-222222222222"
	subA  = "aaaaaaaaaaaa0001"
	subB  = "bbbbbbbbbbbb0002"
)

func eraseFixture(t *testing.T) (*server, *fakeDB) {
	t.Helper()
	s := &server{links: fakeLinks{acctA: {subA}, acctB: {subB}}}
	db := &fakeDB{rows: map[string]string{}}
	if err := s.accounts.attach(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	a, b := globalID(subA), globalID(subB)
	s.accounts.set(a, subA)
	s.accounts.set(b, subB)
	s.names.set(a, "A#1")
	s.names.set(b, "B#2")
	s.storage.put(storageKey(map[string]any{"gameId": "g", "slot": "s"}, a), "x")
	s.storage.put(storageKey(map[string]any{"gameId": "g", "slot": "s"}, b), "y")
	s.sessions.put(&session{ID: "s1", Owner: a, Players: []string{a, b}})
	s.sessions.addInvite(invite{SessionID: "s1", Inviter: b, Invitee: a})
	s.sessions.addInvite(invite{SessionID: "s1", Inviter: a, Invitee: b})
	return s, db
}

func TestEraseRemovesOnlyThatAccount(t *testing.T) {
	s, db := eraseFixture(t)
	n, err := s.Erase(context.Background(), acctA)
	if err != nil || n == 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	a, b := globalID(subA), globalID(subB)
	if _, ok := db.rows[a]; ok || s.accounts.get(a) != "" || s.names.get(a) != "" {
		t.Fatal("A survived")
	}
	if db.rows[b] != subB || s.names.get(b) == "" || len(s.storage.values) != 1 {
		t.Fatal("B touched")
	}
	if got := s.sessions.get("s1").Players; len(got) != 1 || got[0] != b {
		t.Fatalf("players %v", got)
	}
	if len(s.sessions.invitesFor(a)) != 0 || len(s.sessions.invitesFor(b)) != 0 {
		t.Fatal("invites to/from A remain")
	}
	if n, err := s.Erase(context.Background(), acctA); err != nil || n != 0 {
		t.Fatalf("second run n=%d err=%v", n, err)
	}
}

func TestEraseEndpointAuth(t *testing.T) {
	s, _ := eraseFixture(t)
	call := func(key, hdr, id string) int {
		mux := http.NewServeMux()
		mux.HandleFunc("DELETE /internal/accounts/{account_id}/data", s.eraseHandler(key))
		r := httptest.NewRequest("DELETE", "/internal/accounts/"+id+"/data", nil)
		if hdr != "" {
			r.Header.Set("Authorization", hdr)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	for _, c := range []struct {
		key, hdr, id string
		want         int
	}{
		{"", "Bearer k", acctA, 503},
		{"k", "", acctA, 401},
		{"k", "Bearer wrong", acctA, 401},
		{"k", "Bearer k", "not-a-uuid", 400},
		{"k", "Bearer k", acctA, 200},
	} {
		if got := call(c.key, c.hdr, c.id); got != c.want {
			t.Errorf("%+v: got %d", c, got)
		}
	}
}
