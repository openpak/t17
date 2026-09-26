package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
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
