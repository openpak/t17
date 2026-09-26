package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A client frame is always masked and its length is encoded three different ways depending on
// size — the one place here where a wrong byte silently corrupts every message.
func TestReadFrameUnmasksEverySizeClass(t *testing.T) {
	for _, n := range []int{5, 200, 70000} {
		payload := bytes.Repeat([]byte("t17."), n/4+1)[:n]
		mask := [4]byte{0xa1, 0xb2, 0xc3, 0xd4}
		var f bytes.Buffer
		f.WriteByte(0x81)
		switch {
		case n < 126:
			f.WriteByte(byte(0x80 | n))
		case n < 65536:
			f.WriteByte(0x80 | 126)
			_ = binary.Write(&f, binary.BigEndian, uint16(n))
		default:
			f.WriteByte(0x80 | 127)
			_ = binary.Write(&f, binary.BigEndian, uint64(n))
		}
		f.Write(mask[:])
		for i, b := range payload {
			f.WriteByte(b ^ mask[i%4])
		}
		op, got, err := readFrame(&f)
		if err != nil {
			t.Fatalf("len %d: %v", n, err)
		}
		if op != 1 || !bytes.Equal(got, payload) {
			t.Fatalf("len %d: op=%d, payload round-trip failed", n, op)
		}
	}
}

// A frame with no action is not a request and must not be answered.
func TestFrameWithoutActionIsNotAnswered(t *testing.T) {
	if (&server{}).reply([]byte(`{"headers":{}}`), "tok") != nil {
		t.Error("a frame with no action was answered")
	}
}

// A client that offers a subprotocol must be answered with one of its offers, or per RFC 6455
// §4.1 it may treat the connection as failed while leaving it open.
func TestSubprotocolIsEchoed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(&server{}).socket(w, r)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	c, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = fmt.Fprintf(c, "GET /socket HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Protocol: t17be, other\r\n\r\n", u.Host)

	br := bufio.NewReader(c)
	var got string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading handshake: %v", err)
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(line, "Sec-WebSocket-Protocol:") {
			got = strings.TrimSpace(strings.TrimPrefix(line, "Sec-WebSocket-Protocol:"))
		}
	}
	if got != "t17be" {
		t.Fatalf("subprotocol echoed as %q, want %q", got, "t17be")
	}
}

// The client's parsers accept exactly one shape, and each part of it was read out of the binary:
// `action` must be the literal "response" (0x693af0 switches on it and drops anything else),
// `request-id` must stay a number inside `headers`, and `statusCode` must be a number at the
// element root rather than inside `headers`.
func TestReplyMatchesTheClientsParser(t *testing.T) {
	out := (&server{}).reply([]byte(`{"action":"/websocket/go-online","headers":{"request-id":41,"Authorization":"Bearer x"},"body":{}}`), "tok")
	if out == nil {
		t.Fatal("no reply produced")
	}

	var got struct {
		Messages []struct {
			Action     string          `json:"action"`
			StatusCode *int            `json:"statusCode"`
			Headers    map[string]any  `json:"headers"`
			Body       json.RawMessage `json:"body"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reply is not valid JSON: %v (%s)", err, out)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("got %d messages, want 1: %s", len(got.Messages), out)
	}
	m := got.Messages[0]
	if m.Action != "response" {
		t.Errorf(`action = %q, want "response" — anything else is "Unknown Action Type --> Ignoring"`, m.Action)
	}
	if m.StatusCode == nil || *m.StatusCode != 200 {
		t.Errorf("statusCode at the element root = %v, want 200", m.StatusCode)
	}
	if _, inHeaders := m.Headers["statusCode"]; inHeaders {
		t.Error("statusCode must be at the element root, not inside headers")
	}
	id, ok := m.Headers["request-id"].(float64)
	if !ok || id != 41 {
		t.Errorf("headers[request-id] = %#v, want the number 41 — a string is dropped by the client", m.Headers["request-id"])
	}
}

// A frame whose request-id is a JSON number must not be refused outright: decoding headers into
// map[string]string used to fail here and the whole frame was ignored.
func TestNumericRequestIDIsAccepted(t *testing.T) {
	if (&server{}).reply([]byte(`{"action":"/player/get-account","headers":{"request-id":7}}`), "tok") == nil {
		t.Fatal("a numeric request-id was refused")
	}
}

// Each of these is a field some handler in the client refuses to work without, named in its own
// "Missing …" log line. They are recovered facts, not preferences, so they get pinned here.
func TestRoutesAnswerWhatTheHandlersRequire(t *testing.T) {
	s := &server{}
	s.names.set("p1", "tobagin#7016")
	tok := "t17-p1-abc"

	body := func(action, reqBody string) map[string]any {
		frame := `{"action":"` + action + `","headers":{"request-id":1},"body":` + reqBody + `}`
		out := s.reply([]byte(frame), tok)
		if out == nil {
			t.Fatalf("%s was not answered", action)
		}
		var env struct {
			Messages []struct {
				Body map[string]any `json:"body"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(out, &env); err != nil || len(env.Messages) != 1 {
			t.Fatalf("%s: bad envelope %s", action, out)
		}
		return env.Messages[0].Body
	}

	// InternalResponse_GetPlayerAccount reads both, and the display name must be a full name --
	// T17BE splits it at '#' and fails when there is no separator.
	acct := body("/player/get-account", `{"platformId":"nintendo"}`)
	if acct["globalPlayerId"] != "p1" {
		t.Errorf("get-account globalPlayerId = %v, want p1", acct["globalPlayerId"])
	}
	if name, _ := acct["displayName"].(string); !strings.Contains(name, "#") {
		t.Errorf("get-account displayName = %q, must contain '#'", name)
	}

	// "Missing pResponseBody" / "Missing Response Body" handlers.
	if _, ok := body("/friends/get-friends-and-status", `{}`)["friends"]; !ok {
		t.Error("get-friends-and-status must carry friends")
	}
	if _, ok := body("/friends/get-blocked-players", `{}`)["blockList"]; !ok {
		t.Error("get-blocked-players must carry blockList")
	}
	if _, ok := body("/friends/subscribe-to-friend-status-updates", `{}`)["blockList"]; !ok {
		t.Error("subscribe-to-friend-status-updates must carry blockList")
	}
	if _, ok := body("/sessions/list-invites", `{}`)["invites"]; !ok {
		t.Error("list-invites must carry invites")
	}
	if _, ok := body("/live/get", `{"dataKey":"k"}`)["chunk"]; !ok {
		t.Error("live/get must carry a chunk — the handler logs 'Missing Response Body'")
	}

	// InternalResponse_SessionsCreate rejects an empty type, a bad maxPlayers, a bad sessionId and
	// a bad sessionOwner, each with its own log line.
	sess, ok := body("/sessions/create", `{"type":"","maxPlayers":0}`)["session"].(map[string]any)
	if !ok {
		t.Fatal("sessions/create must carry a session object")
	}
	if sess["type"] == "" || sess["sessionId"] == "" || sess["owner"] == "" {
		t.Errorf("sessions/create left a required field empty: %#v", sess)
	}
	if n, _ := sess["maxPlayers"].(float64); n < 1 {
		t.Errorf("sessions/create maxPlayers = %v, must be at least 1", sess["maxPlayers"])
	}
}
