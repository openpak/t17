package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a log sink the server's goroutines and the test can both touch.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

// wsClient is a masked-frame websocket client over a raw connection.
type wsClient struct {
	t  *testing.T
	c  net.Conn
	br *bufio.Reader
	id int
}

func dialSocket(t *testing.T, addr, token string) *wsClient {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(c, "GET /socket?token=%s HTTP/1.1\r\nHost: game.t17service.com\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", token)
	br := bufio.NewReader(c)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("handshake: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	return &wsClient{t: t, c: c, br: br}
}

// call sends one request and returns once its response arrives; pushes in between are skipped.
func (w *wsClient) call(action, body string) {
	w.t.Helper()
	w.id++
	payload := fmt.Sprintf(`{"action":%q,"headers":{"request-id":%d},"body":%s}`, action, w.id, body)
	mask := [4]byte{1, 2, 3, 4}
	f := []byte{0x81, byte(0x80 | len(payload))}
	if len(payload) >= 126 {
		f = []byte{0x81, 0x80 | 126, byte(len(payload) >> 8), byte(len(payload))}
	}
	f = append(f, mask[:]...)
	for i := 0; i < len(payload); i++ {
		f = append(f, payload[i]^mask[i%4])
	}
	if _, err := w.c.Write(f); err != nil {
		w.t.Fatal(err)
	}
	for {
		_ = w.c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, p, err := readFrame(w.br)
		if err != nil {
			w.t.Fatalf("%s: %v", action, err)
		}
		var m struct {
			Action  string `json:"action"`
			Headers struct {
				ID int `json:"request-id"`
			} `json:"headers"`
		}
		if json.Unmarshal(p, &m) == nil && m.Action == "response" && m.Headers.ID == w.id {
			return
		}
	}
}

func login(t *testing.T, base, sub, name string, key *rsa.PrivateKey) (id, token string) {
	t.Helper()
	body := fmt.Sprintf(`{"platformId":"nintendo","deviceId":"nintendo-switch","gameId":"ProjectJourney","displayName":%q,"nintendoSwitchAccessToken":%q}`,
		name, signed(t, key, "k1", map[string]any{"sub": sub}))
	req, _ := http.NewRequest("POST", base+"/prod/auth/get-token", strings.NewReader(body))
	req.Host = "xplay.t17service.com"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out struct {
		GlobalPlayerID string `json:"globalPlayerId"`
		Token          string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		t.Fatalf("get-token: %s", raw)
	}

	return out.GlobalPlayerID, out.Token
}

// Sign-in, the socket, sessions, invites, relays and the error paths must leave no player id,
// token, display name, session id, address, query or frame text in the log.
func TestLogsCarryNoPersonalData(t *testing.T) {
	for _, trace := range []bool{false, true} {
		t.Run(fmt.Sprintf("trace=%v", trace), func(t *testing.T) { logsCarryNoPersonalData(t, trace) })
	}
}

func logsCarryNoPersonalData(t *testing.T, trace bool) {
	var out syncBuf
	prev := log.Writer()
	log.SetOutput(&out)
	oldTrace := traceBodies
	traceBodies = trace
	t.Cleanup(func() { log.SetOutput(prev); traceBodies = oldTrace })

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	js := jwksServer(t, key, "k1")
	defer js.Close()
	s := &server{self: "t17service.com", keys: newKeyCache(js.URL, "")}
	srv := httptest.NewServer(http.HandlerFunc(s.route))
	defer srv.Close()

	const (
		nameA    = "Zorblaxa"
		nameB    = "Quuxnick"
		chat     = "chat-secret-text-77"
		ghost    = "ghostpid5551212"
		badSess  = "SESSION-SECRET-ZZ"
		pathSec  = "SecretPath9"
		querySec = "q-token-9f8e7d"
	)
	idA, tokA := login(t, srv.URL, "7788991122aabb01", nameA, key)
	idB, tokB := login(t, srv.URL, "7788991122aabb02", nameB, key)

	// A token the verifier cannot check because the key service is gone: the fetch error spells
	// out the URL.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	sBad := &server{self: "t17service.com", keys: newKeyCache(deadURL+"/keys?access="+querySec, "")}
	w := httptest.NewRecorder()
	sBad.getToken(w, httptest.NewRequest("POST", "/prod/auth/get-token", strings.NewReader(
		fmt.Sprintf(`{"displayName":%q,"nintendoSwitchAccessToken":%q}`, nameA, signed(t, key, "k1", map[string]any{"sub": "x"})))))

	a := dialSocket(t, srv.Listener.Addr().String(), tokA+"&access="+querySec)
	b := dialSocket(t, srv.Listener.Addr().String(), tokB)
	clientAddr := a.c.LocalAddr().String()
	a.call("/websocket/go-online", `{}`)
	a.call("/sessions/create", `{"type":"game","maxPlayers":4,"chunk":"{\"name\":\"`+chat+`\"}"}`)
	a.call("/sessions/send-invite", fmt.Sprintf(`{"sessionId":%q,"inviteeId":%q}`, badSess, idB))
	a.call("/sessions/send-invite", fmt.Sprintf(`{"sessionId":%q,"inviteeId":%q}`, badSess, ghost))
	b.call("/sessions/accept-invite", fmt.Sprintf(`{"sessionId":%q}`, badSess))
	a.call("/messaging/send", fmt.Sprintf(`{"to":{"globalPlayerIds":[%q,%q]},"messageSystemId":"XPSM","messageType":1,"message":%q}`, idB, ghost, chat))
	a.call("/chat/"+nameA+"/says", `{}`)
	// An unknown HTTP path (no upgrade) and a session invite that does reach the peer.
	res, err := http.Get(srv.URL + "/weird/" + pathSec + "?x=" + querySec)
	if err == nil {
		res.Body.Close()
	}
	a.c.Close()
	b.c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for strings.Count(out.String(), "socket: closed") < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	got := out.String()
	for _, want := range []string{"get-token: game=ProjectJourney player=~", "is online", "relay XPSM/1 to ~", "unhandled GET"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected line fragment %q missing:\n%s", want, got)
		}
	}
	_, cport, _ := net.SplitHostPort(clientAddr)
	secrets := []string{querySec, deadURL, strings.TrimPrefix(deadURL, "http://"), "?access=", cport}
	if !trace {
		// The opt-in frame dump (T17_TRACE_BODIES=1) is allowed to show frame text, ids and names.
		secrets = append(secrets, "7788991122", idA, idB, tokA, tokB, nameA, nameB, ghost, badSess, pathSec, chat, "request-id")
	}
	for _, secret := range secrets {
		if strings.Contains(got, secret) {
			t.Errorf("log contains %q:\n%s", secret, got)
		}
	}
	if trace && !strings.Contains(got, chat) {
		t.Errorf("T17_TRACE_BODIES did not turn the frame dump on")
	}
}
