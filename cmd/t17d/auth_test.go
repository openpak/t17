package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func signed(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	b64 := base64.RawURLEncoding.EncodeToString
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	body := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return body + "." + b64(sig)
}

// jwksServer publishes one RSA key as the BAAS does, so verification is exercised end to end
// rather than stubbed.
func jwksServer(t *testing.T, key *rsa.PrivateKey, kid string) *httptest.Server {
	t.Helper()
	b64 := base64.RawURLEncoding.EncodeToString
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kid": kid, "kty": "RSA",
			"n": b64(key.PublicKey.N.Bytes()),
			"e": b64(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}})
	}))
}

func TestGetTokenAnswersTheManagedClass(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	js := jwksServer(t, key, "openpak-baas-key-1")
	defer js.Close()
	s := &server{self: "t17service.com", keys: newKeyCache(js.URL, "")}

	tok := signed(t, key, "openpak-baas-key-1", map[string]any{"sub": "aaaaaaaaaaaa0001"})
	body := fmt.Sprintf(`{"platformId":"nintendo","deviceId":"nintendo-switch","gameId":"ProjectJourney",
		"displayName":"hansolo","nintendoSwitchAccessToken":%q}`, tok)
	w := httptest.NewRecorder()
	s.getToken(w, httptest.NewRequest("POST", "/prod/auth/get-token", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// JsonUtility throws on a type mismatch and takes the auth step down silently, so every one
	// of these has to be the type Team17.CrossPlay.Auth declares.
	want := map[string]string{
		"websocketUrl": "string", "token": "string", "displayName": "string",
		"globalPlayerId": "string", "role": "string", "version": "string",
		"deviceId": "string", "platformId": "string",
		"exp": "number", "ttl": "number", "killSwitchActive": "bool", "blockList": "array",
	}
	if len(got) != len(want) {
		t.Errorf("reply has %d fields, the managed class has %d: %s", len(got), len(want), w.Body)
	}
	for field, kind := range want {
		var actual string
		switch got[field].(type) {
		case string:
			actual = "string"
		case float64:
			actual = "number"
		case bool:
			actual = "bool"
		case []any:
			actual = "array"
		default:
			actual = "missing"
		}
		if actual != kind {
			t.Errorf("%s is %s, the managed field is %s", field, actual, kind)
		}
	}
	// The separator is load-bearing: without it T17BE fails its whole init and logs nothing.
	if name, _ := got["displayName"].(string); !strings.Contains(name, "#") {
		t.Errorf("displayName %q has no '#'", name)
	}
	if got["globalPlayerId"] != "aaaaaaaaaaaa0001" || got["role"] != "client" {
		t.Errorf("wrong identity or role: %s", w.Body)
	}
}

// The token names the player, so anything we cannot verify must be refused rather than trusted.
func TestGetTokenRefusesWhatItCannotVerify(t *testing.T) {
	good, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	js := jwksServer(t, good, "openpak-baas-key-1")
	defer js.Close()

	for name, tok := range map[string]string{
		"not a jwt":   "nonsense",
		"wrong key":   signed(t, other, "openpak-baas-key-1", map[string]any{"sub": "x"}),
		"unknown kid": signed(t, good, "someone-elses-key", map[string]any{"sub": "x"}),
		"expired":     signed(t, good, "openpak-baas-key-1", map[string]any{"sub": "x", "exp": 1}),
		"no subject":  signed(t, good, "openpak-baas-key-1", map[string]any{}),
	} {
		s := &server{self: "t17service.com", keys: newKeyCache(js.URL, "")}
		w := httptest.NewRecorder()
		s.getToken(w, httptest.NewRequest("POST", "/prod/auth/get-token",
			strings.NewReader(fmt.Sprintf(`{"nintendoSwitchAccessToken":%q}`, tok))))
		if w.Code != 401 {
			t.Errorf("%s: got HTTP %d, want 401 (%s)", name, w.Code, w.Body)
		}
	}
	// With no JWKS configured there is no way to know who the caller is; refuse.
	s := &server{self: "t17service.com", keys: newKeyCache("", "")}
	w := httptest.NewRecorder()
	s.getToken(w, httptest.NewRequest("POST", "/prod/auth/get-token",
		strings.NewReader(fmt.Sprintf(`{"nintendoSwitchAccessToken":%q}`,
			signed(t, good, "openpak-baas-key-1", map[string]any{"sub": "x"})))))
	if w.Code != 401 {
		t.Errorf("no jwks url: got HTTP %d, want 401", w.Code)
	}
}
