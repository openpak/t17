package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// getToken is the whole of T17BE authentication: one POST carrying the console's own BAAS
// id_token, answered with the addressing the client needs.
//
//	POST <xplayurl>auth/get-token
//	{"coreVersion":…,"platformId":"nintendo","deviceId":"nintendo-switch","gameId":…,
//	 "authType":"nintendo-switch","displayName":…,"nintendoSwitchAccessToken":"<JWT>"}
//
// The JWT is one OpenPak signed, because OpenPak is the BAAS the console logged into — so this
// service verifies its own token instead of asking Nintendo, the way servers/demonware does.
func (s *server) getToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": "method not allowed"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	var in struct {
		PlatformID  string `json:"platformId"`
		DeviceID    string `json:"deviceId"`
		GameID      string `json:"gameId"`
		DisplayName string `json:"displayName"`
		Token       string `json:"nintendoSwitchAccessToken"`
	}
	_ = json.Unmarshal(body, &in)

	claims, err := s.keys.verify(in.Token)
	if err != nil {
		log.Printf("[t17] get-token: id_token rejected: %v", err)
		writeJSON(w, 401, map[string]any{"error": "invalid token"})
		return
	}
	id, _ := claims["sub"].(string)
	if id == "" {
		writeJSON(w, 401, map[string]any{"error": "token names no subject"})
		return
	}

	name := in.DisplayName
	if name == "" {
		name = id
	}
	// T17BE splits displayNameFull at '#' to get the short name, and its split helper returns
	// FALSE when the separator is absent — which fails PreInitialise through "Problem splitting
	// the displayNameFull into short!!", a path with no error string of its own, so the game goes
	// quiet with no socket and no diagnostic. A name without a tag can never work.
	if !strings.Contains(name, "#") {
		name = fmt.Sprintf("%s#%04d", name, crc32.ChecksumIEEE([]byte(id))%10000)
	}

	const ttl = 12 * time.Hour
	log.Printf("[t17] get-token: game=%s player=%s (%s) platform=%s", in.GameID, name, id, in.PlatformID)
	// Exactly the twelve fields of the managed response class, with the types it declares:
	// JsonUtility.FromJsonOverwrite ignores unknown keys and defaults missing ones, but THROWS on
	// a type mismatch and takes the whole auth step down silently. `blockList` is an array here
	// even though the native SDK wants a stringified chunk — the native side never sees this
	// JSON, the C# rebuilds one for it.
	writeJSON(w, 200, map[string]any{
		"websocketUrl":     "wss://game." + s.self + "/socket",
		"token":            s.mintToken(id),
		"exp":              time.Now().Add(ttl).Unix(),
		"ttl":              int(ttl.Seconds()),
		"displayName":      name,
		"globalPlayerId":   id,
		"role":             "client",
		"blockList":        []any{},
		"version":          "1",
		"deviceId":         in.DeviceID,
		"platformId":       in.PlatformID,
		"killSwitchActive": false,
	})
}

func (s *server) mintToken(id string) string {
	sum := sha256.Sum256([]byte(id + time.Now().Format(time.RFC3339Nano)))
	return "t17-" + id + "-" + base64.RawURLEncoding.EncodeToString(sum[:16])
}

// keyCache fetches and caches the BAAS signing keys. The console's id_token is RS256 signed by
// nx-baas, so verifying it is a JWKS lookup and an RSA check — no call back to the account service.
type keyCache struct {
	url string
	// host overrides the Host header. nx-baas routes by hostname and answers the BAAS surface
	// only for its own names, so a request to the container address needs the BAAS name on it or
	// it lands in the catch-all 404.
	host string
	mu   sync.Mutex
	at   time.Time
	key  map[string]*rsa.PublicKey
}

func newKeyCache(url, host string) *keyCache { return &keyCache{url: url, host: host} }

func (k *keyCache) verify(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a jwt")
	}
	var hdr struct{ Alg, Kid string }
	h, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(h, &hdr) != nil {
		return nil, fmt.Errorf("unreadable header")
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("unexpected alg %q", hdr.Alg)
	}
	pub, err := k.lookup(hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("unreadable signature")
	}
	signed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, signed[:], sig); err != nil {
		return nil, fmt.Errorf("signature does not verify")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("unreadable claims")
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, fmt.Errorf("unreadable claims")
	}
	if exp, ok := claims["exp"].(float64); ok && time.Now().Unix() > int64(exp) {
		return nil, fmt.Errorf("token expired")
	}
	return claims, nil
}

// lookup refuses rather than guessing when the keys cannot be fetched: the token is what names
// the player, so an unverified one would let any caller be anybody.
func (k *keyCache) lookup(kid string) (*rsa.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.key != nil && time.Since(k.at) < 10*time.Minute {
		if p, ok := k.key[kid]; ok {
			return p, nil
		}
	}
	if k.url == "" {
		return nil, fmt.Errorf("no -jwks-url configured, cannot verify a console token")
	}
	keys, err := fetchJWKS(k.url, k.host)
	if err != nil {
		return nil, err
	}
	k.key, k.at = keys, time.Now()
	p, ok := keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	return p, nil
}

func fetchJWKS(url, host string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if host != "" {
		req.Host = host
	}
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("jwks: HTTP %d", res.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kid, Kty, N, E string
			X5c            []string
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, j := range doc.Keys {
		if p := rsaFromJWK(j.N, j.E, j.X5c); p != nil {
			out[j.Kid] = p
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("jwks carried no usable RSA key")
	}
	return out, nil
}

// rsaFromJWK accepts either the modulus/exponent pair or an x5c certificate, because BAAS JWKS
// documents in this stack have carried both shapes.
func rsaFromJWK(nStr, eStr string, x5c []string) *rsa.PublicKey {
	if nStr != "" && eStr != "" {
		n, err1 := base64.RawURLEncoding.DecodeString(nStr)
		e, err2 := base64.RawURLEncoding.DecodeString(eStr)
		if err1 == nil && err2 == nil {
			return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		}
	}
	for _, c := range x5c {
		der, err := base64.StdEncoding.DecodeString(c)
		if err != nil {
			continue
		}
		if crt, err := x509.ParseCertificate(der); err == nil {
			if p, ok := crt.PublicKey.(*rsa.PublicKey); ok {
				return p
			}
		}
	}
	return nil
}

// pemPublicKey is here for the operator path: a key file mounted beside the service instead of a
// JWKS URL, which is how the rig runs when nx-baas is not reachable.
func pemPublicKey(b []byte) *rsa.PublicKey {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil
	}
	if p, err := x509.ParsePKIXPublicKey(blk.Bytes); err == nil {
		if r, ok := p.(*rsa.PublicKey); ok {
			return r
		}
	}
	return nil
}
