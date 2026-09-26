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

// __ParseJSONBlob looks for a `messages` array and __ParseJSONObject refuses any response with no
// `headers` object, so a reply missing either is discarded without a word.
func TestReplyCarriesMessagesAndHeaders(t *testing.T) {
	out := reply([]byte(`{"action":"/websocket/go-online","headers":{"request-id":"abc"}}`), "tok")
	if out == nil {
		t.Fatal("a request with an action was not answered")
	}
	var env struct {
		Messages []struct {
			Headers map[string]any `json:"headers"`
			Body    map[string]any `json:"body"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Messages) != 1 {
		t.Fatalf("expected one message, got %d: %s", len(env.Messages), out)
	}
	h := env.Messages[0].Headers
	if h["request-id"] != "abc" || h["action"] != "/websocket/go-online" || h["statusCode"] != 200.0 {
		t.Errorf("headers do not match the request: %s", out)
	}
	// A frame with no action is not a request and must not be answered.
	if reply([]byte(`{"headers":{}}`), "tok") != nil {
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
