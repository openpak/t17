package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

// A fake socket, so a push can be captured without a network.
func fakeConn() (*conn, *bytes.Buffer) {
	var out bytes.Buffer
	rw := bufio.NewReadWriter(bufio.NewReader(&bytes.Buffer{}), bufio.NewWriter(&out))

	return &conn{rw: rw}, &out
}

// frames pulls the text payloads out of what was written, so a test reads JSON rather than websocket
// framing.
func frames(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var got []map[string]any
	b := out.Bytes()
	for len(b) > 2 {
		n := int(b[1] & 0x7f)
		h := 2
		switch n {
		case 126:
			n = int(b[2])<<8 | int(b[3])
			h = 4
		case 127:
			t.Fatal("unexpected huge frame")
		}
		if len(b) < h+n {
			break
		}
		var m map[string]any
		if err := json.Unmarshal(b[h:h+n], &m); err != nil {
			t.Fatalf("frame is not JSON: %v (%q)", err, b[h:h+n])
		}
		got = append(got, m)
		b = b[h+n:]
	}

	return got
}

// The whole point of the store: an invite sent by one player has to reach the other one's socket as
// message 2007, appear in their list-invites, and resolve to the SAME session with both players in
// it. None of that can work from per-connection state.
func TestInviteReachesTheOtherPlayer(t *testing.T) {
	s := &server{}
	s.names.set("host1", "tobagin#7016")
	s.names.set("guest2", "Leia#8667")
	host := &client{s: s, token: "t17-host1-x"}
	guest := &client{s: s, token: "t17-guest2-y"}

	guestConn, guestOut := fakeConn()
	s.conns.add("guest2", guestConn)
	hostConn, _ := fakeConn()
	s.conns.add("host1", hostConn)

	created, _ := host.createSession(map[string]any{"type": "group", "maxPlayers": 4.0})["id"].(string)
	if created == "" {
		t.Fatal("no session id")
	}

	host.sendInvite(map[string]any{"sessionId": created, "inviteeId": "guest2"})

	// 1. the invitee's socket was pushed message 2007
	var pushed map[string]any
	for _, f := range frames(t, guestOut) {
		body, _ := f["body"].(map[string]any)
		if f["action"] == "message" && body != nil {
			if n, _ := body["messageType"].(float64); int(n) == msgInviteReceived {
				pushed = body
			}
		}
	}
	if pushed == nil {
		t.Fatal("the invitee was never pushed message 2007")
	}
	if pushed["messageSystemId"] != sessionSystemID {
		t.Errorf("messageSystemId = %v, want %q (a string)", pushed["messageSystemId"], sessionSystemID)
	}
	inner, ok := pushed["message"].(map[string]any)
	if !ok {
		t.Fatal("the push has no inner `message` — the client refuses that")
	}
	if inner["sessionId"] != created || inner["inviter"] != "host1" {
		t.Errorf("push payload wrong: %#v", inner)
	}

	// 2. it shows up in their invite list
	list := guest.listInvites()
	if len(list) != 1 {
		t.Fatalf("guest sees %d invites, want 1", len(list))
	}

	// 3. accepting resolves to the same session, with both players
	sess, _ := guest.acceptInvite(map[string]any{"sessionId": created})["session"].(map[string]any)
	if sess == nil {
		t.Fatal("accept-invite returned no session")
	}
	if sess["id"] != created {
		t.Errorf("accepted session id = %v, want %v", sess["id"], created)
	}
	players, _ := sess["players"].([]any)
	if len(players) != 2 {
		t.Fatalf("session has %d players after accept, want 2: %#v", len(players), players)
	}
	// and the invite is consumed
	if len(guest.listInvites()) != 0 {
		t.Error("the invite is still listed after being accepted")
	}
}

// The client names its Photon room after the session id, so two sessions from the same player must
// not share one: the second attempt would rejoin the first attempt's dead Fusion room.
func TestSessionIDsAreUniquePerSession(t *testing.T) {
	s := &server{}
	c := &client{s: s, token: "t17-host1-x"}
	first, _ := c.createSession(map[string]any{"type": "group"})["id"].(string)
	second, _ := c.createSession(map[string]any{"type": "group"})["id"].(string)
	if first == "" || second == "" {
		t.Fatal("no session id")
	}
	if first == second {
		t.Errorf("two sessions share the id %q — the second joins the first's Photon room", first)
	}
	if s.sessions.get(first) == nil || s.sessions.get(second) == nil {
		t.Error("both sessions should be retrievable")
	}
}
