// Command t17d serves Team17's backend (T17BE) for the Switch titles that use it.
//
// A T17BE title asks three names in turn, all of them redirected to OpenPak by the signed
// ceiling's `.t17service.com` family:
//
//	directory.<host>/                  the addressing: where the game and crossplay tiers live
//	xplay.<host>/prod/auth/get-token   authentication, answered from the console's own BAAS token
//	game.<host>/socket?token=…         one websocket carrying every other operation
//
// Plain HTTP inside the pod network; Traefik terminates the guest's TLS for the three names, the
// same arrangement as the Among Us front and servers/battlenet.
//
// The protocol was recovered from Moving Out 2 (010065D018172000 v1.3.315) — see
// docs/protocol.md, and keep that file in step with this code.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
)

var (
	addr       = flag.String("addr", ":22630", "HTTP listen address (Traefik terminates TLS in front)")
	healthAddr = flag.String("health-addr", ":22631", "health listen address")
	selfHost   = flag.String("self-host", envOr("T17_SELF_HOST", "t17service.com"),
		"the domain handed back in the directory, so the game returns to us")
	jwksURL = flag.String("jwks-url", os.Getenv("T17_JWKS_URL"),
		"where to fetch the BAAS public keys that sign a console's id_token")
	jwksHost = flag.String("jwks-host", os.Getenv("T17_JWKS_HOST"),
		"Host header for -jwks-url; nx-baas serves the BAAS surface only for its own names")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	flag.Parse()
	s := &server{self: *selfHost, keys: newKeyCache(*jwksURL, *jwksHost)}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.route)
	go func() {
		h := http.NewServeMux()
		h.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, map[string]any{"status": "ok"})
		})
		log.Printf("[t17] health on %s", *healthAddr)
		log.Fatal(http.ListenAndServe(*healthAddr, h))
	}()

	log.Printf("[t17] serving %s on %s (directory -> game.%s / xplay.%s)",
		s.self, *addr, s.self, s.self)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type server struct {
	self     string
	keys     *keyCache
	names    names
	sessions sessionStore
	storage  storageStore
	conns    connections
}

// conn is the live websocket of a player, or nil when they are not online.
func (s *server) conn(id string) *conn { return s.conns.get(id) }

// nameFor is the display name minted for a player at get-token, remembered because T17BE needs a
// full name (with its '#') everywhere it reports one back.
func (s *server) nameFor(id string) string { return s.names.get(id) }

// route dispatches on the hostname, because which of the three tiers a request belongs to is the
// only thing that distinguishes them: all three arrive on one port behind Traefik.
func (s *server) route(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	switch {
	case strings.HasPrefix(host, "directory."):
		s.directory(w, r)
	case strings.HasSuffix(r.URL.Path, "/auth/get-token"):
		s.getToken(w, r)
	case s.socket(w, r):
		// upgraded and handled for the life of the connection
	default:
		log.Printf("[t17] !! unhandled %s %s%s", r.Method, host, r.URL.Path)
		writeJSON(w, 404, map[string]any{"error": "not found"})
	}
}

// directory is one GET and its reply is the whole of the addressing: the game takes every other
// request to the hosts named here. Answering it is what points a title at OpenPak without
// redirecting anything else — notably not the AWS API Gateway the real service names, which is
// far too broad a family to put in the ceiling.
func (s *server) directory(w http.ResponseWriter, r *http.Request) {
	log.Printf("[t17] directory: title=%q platform=%q tier=%q", r.Header.Get("Api-Title"),
		r.Header.Get("Api-Platform"), r.Header.Get("Api-Tier"))
	writeJSON(w, 200, map[string]any{
		"game":       map[string]any{"url": "https://game." + s.self + "/"},
		"xplayurl":   "https://xplay." + s.self + "/prod/",
		"AllowXPlay": "1",
		"ga":         map[string]any{"ga": "0"},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
