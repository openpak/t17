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

	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) // RFC 6455 §4.2.2
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
	_ = rw.Flush()
	log.Printf("[t17] socket: open %s%s from %s", r.Host, r.URL.Path, r.RemoteAddr)

	token := r.URL.Query().Get("token")

	// The client opens the socket and sends nothing, so this connection has to earn its keep as
	// an experiment. Three things happen, in order:
	//
	//  1. An empty `messages` envelope, immediately. `__ParseJSONBlob` looks for that key, so this
	//     is the smallest well-formed thing a server can say. If the transport is waiting to be
	//     greeted before it will "finalise new connection", this is what unblocks it.
	//  2. A ping on idle. A pong would prove the reader is alive — but only if this SDK answers
	//     pings itself, which is not established, so silence alone proves nothing.
	//  3. After three unanswered pings, a close frame. RFC 6455 requires a reader that is alive to
	//     echo a close, so "client closed" versus a bare timeout settles read-vs-not-reading
	//     without depending on ping behaviour at all.
	if err := writeText(rw, []byte(`{"messages":[]}`)); err != nil {
		log.Printf("[t17] socket: greeting failed (%v)", err)
		return true
	}
	log.Printf("[t17] socket >>> greeting {\"messages\":[]}")

	idle := 0
	for {
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		op, payload, err := readFrame(rw)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				idle++
				if idle > 3 {
					log.Printf("[t17] socket: %d idle periods, no pong — sending close to test the reader", idle)
					_, _ = rw.Write([]byte{0x88, 0}) // close, no payload
					_ = rw.Flush()
					// A live reader must echo the close; read once more to find out which it is.
					_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
					if op, _, err := readFrame(rw); err == nil && op == 0x8 {
						log.Printf("[t17] socket: close echoed — the reader IS alive, it was waiting on us")
					} else {
						log.Printf("[t17] socket: no close echo (%v) — the reader is not running", err)
					}
					return true
				}
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

// A T17BE request is {"action":…,"headers":{"request-id":…,"Authorization":…},"body":{…}} and the
// reply goes back inside a `messages` array — `__ParseJSONBlob` looks for that key, and
// `__ParseJSONObject` refuses any response without a `headers` object. `statusCode` is what
// InternalResponse_GoOnline and friends check before proceeding.
func reply(frame []byte, token string) []byte {
	var in struct {
		Action  string            `json:"action"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(frame, &in); err != nil || in.Action == "" {
		return nil
	}
	headers := map[string]any{
		"request-id": in.Headers["request-id"],
		"action":     in.Action,
		"statusCode": 200,
	}
	body := map[string]any{}
	switch in.Action {
	case "/websocket/go-online":
		// Nothing of ours to report yet; the status code is what it gates on.
	case "/websocket/ping":
		// Same — an empty body with a 200 is a healthy pong at the application level.
	}
	out, _ := json.Marshal(map[string]any{
		"messages": []any{map[string]any{"headers": headers, "body": body}},
	})
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
