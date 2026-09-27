package main

import (
	"strings"
	"testing"
)

// mintToken's hash is base64url, so it often contains a dash. Splitting the token at the last dash
// left part of it glued to the player id, and that id became the session owner, the player entry and
// the display name — the game created a session whose owner was not the player, and left at once.
func TestPlayerIDStopsAtTheFirstDash(t *testing.T) {
	for _, tc := range []struct{ token, want string }{
		{"t17-aaaaaaaaaaaa0002-YIJI930d", "aaaaaaaaaaaa0002"},
		{"t17-aaaaaaaaaaaa0002-Ec-nQ-t7", "aaaaaaaaaaaa0002"},
		{"t17-aaaaaaaaaaaa0001-kF8drMj-dE-cTGehG1LWVg", "aaaaaaaaaaaa0001"},
		{"t17-aaaaaaaaaaaa0001", "aaaaaaaaaaaa0001"},
	} {
		if got := (&client{token: tc.token}).playerID(); got != tc.want {
			t.Errorf("playerID(%q) = %q, want %q", tc.token, got, tc.want)
		}
	}
}

// With the id right, the remembered display name is found instead of the synthetic fallback.
func TestDisplayNameUsesTheMintedName(t *testing.T) {
	s := &server{}
	s.names.set("aaaaaaaaaaaa0002", "Leia#8667")
	c := &client{s: s, token: "t17-aaaaaaaaaaaa0002-YIJI930d-xx"}
	if got := c.displayName(); got != "Leia#8667" {
		t.Errorf("displayName = %q, want Leia#8667", got)
	}
}

// The game parses a global player id with Guid.Parse, so it must be 32 hex digits, must survive
// the token's cut at the first dash, and must give the account id back.
func TestGlobalIDIsAGuidAndRoundTrips(t *testing.T) {
	const account = "aaaaaaaaaaaa0001"
	id := globalID(account)
	if len(id) != 32 || strings.ContainsRune(id, '-') {
		t.Fatalf("globalID(%q) = %q, want 32 undashed digits", account, id)
	}
	if got := platformID(id); got != account {
		t.Fatalf("platformID(%q) = %q, want %q", id, got, account)
	}
	c := &client{s: &server{}, token: (&server{}).mintToken(id)}
	if got := c.playerID(); got != id {
		t.Fatalf("playerID() = %q, want %q", got, id)
	}
	if got := c.playerOf(id)["platformPlayerId"]; got != account {
		t.Fatalf("platformPlayerId = %v, want %q", got, account)
	}
}
