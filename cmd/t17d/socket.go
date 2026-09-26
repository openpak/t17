package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// The T17BE websocket: everything after get-token — /player/*, /sessions/*, /friends/*,
// /storage/*, /live/* — is JSON over this one socket, each message carrying a `headers` object
// the SDK matches back to its original request. Nothing is answered yet; this accepts the
// upgrade and logs both directions so the envelope can be read off a real client.
//
// ponytail: RFC 6455 by hand rather than a dependency — the handshake, a length and an unmask.
// Add a library when this needs fragmentation or permessage-deflate, which the client offers and
// we decline; declining is legal and it must then not compress.
func (s *server) socket(w http.ResponseWriter, r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return false
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("[t17] socket: connection cannot be hijacked")
		return false
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		log.Printf("[t17] socket: hijack: %v", err)
		return false
	}
	defer conn.Close()

	// The client's own request has never been read back, and it is the one part of this exchange
	// we can see without instrumenting the console. Log it verbatim.
	for _, h := range []string{"Sec-WebSocket-Version", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions", "User-Agent", "Origin"} {
		if v := r.Header.Get(h); v != "" {
			log.Printf("[t17] socket: req %s: %s", h, v)
		}
	}

	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) // RFC 6455 §4.2.2
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n"
	// RFC 6455 §4.1: a client that offered subprotocols and is answered without one has no
	// agreed protocol, and is entitled to treat the connection as failed — which from outside
	// looks exactly like our symptom, an open socket that never speaks. Echo the first offer.
	// Extensions are left unacknowledged on purpose: we cannot deflate.
	if p := r.Header.Get("Sec-WebSocket-Protocol"); p != "" {
		first := strings.TrimSpace(strings.Split(p, ",")[0])
		resp += "Sec-WebSocket-Protocol: " + first + "\r\n"
		log.Printf("[t17] socket: echoing subprotocol %q", first)
	}
	_, _ = rw.WriteString(resp + "\r\n")
	_ = rw.Flush()
	log.Printf("[t17] socket: open %s%s from %s", r.Host, r.URL.Path, r.RemoteAddr)

	token := r.URL.Query().Get("token")

	// Nothing is sent unprompted. An empty `messages` array reaches the branch in
	// __ParseJSONBlob that marks a pending request complete, so a "greeting" is not harmless, and
	// the real contract is known now (docs/protocol.md) rather than something to probe for.

	idle := 0
	for {
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		op, payload, err := readFrame(rw)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				idle++
				// ponytail: a keepalive ping and nothing more. The close-frame experiment that used
				// to live here existed to find out whether the client was reading at all; it is
				// not, and the reason was the emulator answering nn::ssl Pending with a hardcoded
				// zero. A server that hangs up on an idle client would break the working case.
				log.Printf("[t17] socket: idle 20s, pinging (%d)", idle)
				if _, err := rw.Write([]byte{0x89, 0}); err != nil { // ping, no payload
					log.Printf("[t17] socket: ping failed (%v)", err)
					return true
				}
				_ = rw.Flush()
				continue
			}
			log.Printf("[t17] socket: closed (%v)", err)
			return true
		}
		switch op {
		case 0x8:
			log.Printf("[t17] socket: client closed")
			return true
		case 0x9: // ping -> pong
			_, _ = rw.Write([]byte{0x8a, byte(len(payload))})
			_, _ = rw.Write(payload)
			_ = rw.Flush()
		case 0xa:
			log.Printf("[t17] socket: pong — the client's reader is alive and it is waiting on us")
		default:
			idle = 0
			log.Printf("[t17] socket <<< op=%d len=%d %s", op, len(payload), payload)
			if reply := reply(payload, token); reply != nil {
				log.Printf("[t17] socket >>> %s", reply)
				if err := writeText(rw, reply); err != nil {
					log.Printf("[t17] socket: write failed (%v)", err)
					return true
				}
			}
		}
	}
}

// A T17BE request is {"action":"<path>","headers":{"request-id":N,…},"body":{…}} and the reply
// goes back inside a `messages` array. The shape below is read off the client's own parsers rather
// than guessed (see docs/protocol.md):
//
//	__ParseJSONBlob   (0x6930f0) wants the root key `messages`, an array. Each element goes to
//	__ParseJSONObject (0x693af0) which switches on the element's `action` — and it accepts exactly
//	                  three literals, "response", "message" and "multipart". Anything else, the
//	                  request path included, logs "Unknown Action Type" and is dropped.
//	the response path (0x693cf0) then requires `headers` holding `request-id` as a NUMBER, and
//	                  `statusCode` as a NUMBER at the element root, not inside headers. `body` is
//	                  optional; absent, the client zeroes it. 200 is the success path — 400 maps to
//	                  its error 8, 403 to 7, 404 to 5, and >=500 or 429 fails the request outright.
func reply(frame []byte, token string) []byte {
	var in struct {
		Action  string                     `json:"action"`
		Headers map[string]json.RawMessage `json:"headers"`
	}
	if err := json.Unmarshal(frame, &in); err != nil || in.Action == "" {
		return nil
	}

	headers := map[string]any{}
	// Echoed verbatim so its JSON type survives: the client reads request-id as an int, and
	// re-encoding it as a string would make its own parser drop the reply.
	if id, ok := in.Headers["request-id"]; ok {
		headers["request-id"] = id
	} else {
		log.Printf("[t17] socket: request %q carries no request-id — the client will ignore any reply", in.Action)
	}

	msg := map[string]any{
		"action":     "response",
		"headers":    headers,
		"statusCode": 200,
		"body":       map[string]any{},
	}
	out, _ := json.Marshal(map[string]any{"messages": []any{msg}})

	return out
}

// writeText sends one unfragmented, unmasked text frame; servers never mask (RFC 6455 §5.1).
func writeText(rw *bufio.ReadWriter, payload []byte) error {
	h := []byte{0x81}
	switch n := len(payload); {
	case n < 126:
		h = append(h, byte(n))
	case n < 65536:
		h = append(h, 126, byte(n>>8), byte(n))
	default:
		h = append(h, 127)
		for i := 7; i >= 0; i-- {
			h = append(h, byte(n>>(8*i)))
		}
	}
	if _, err := rw.Write(h); err != nil {
		return err
	}
	if _, err := rw.Write(payload); err != nil {
		return err
	}
	return rw.Flush()
}

// readFrame returns one frame's opcode and unmasked payload. Client frames are always masked.
func readFrame(r io.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	op, masked, n := h[0]&0x0f, h[1]&0x80 != 0, uint64(h[1]&0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}
