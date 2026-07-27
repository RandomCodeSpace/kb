package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testStatic = fstest.MapFS{
	"index.html": &fstest.MapFile{Data: []byte("<html>spa</html>")},
}

func newTestServer(t *testing.T, cfg Config) (http.Handler, string) {
	t.Helper()
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	return newServer(cfg, testStatic), cfg.DataDir
}

func doReq(t *testing.T, h http.Handler, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSanitizeUser(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Alice@Example.COM", "alice@example.com", false},
		{"bob", "bob", false},
		{"first.last_1-2", "first.last_1-2", false},
		{"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", false},
		// Chars outside [a-z0-9._@-] are rejected, never substituted:
		// substitution would map distinct identities to the same file.
		{"user name+tag", "", true},
		{"foo/bar", "", true},
		{`foo\..\bar`, "", true},
		{"héllo", "", true},
		{"../../etc/passwd", "", true},
		{"..", "", true},
		{".", "", true},
		{".hidden", "", true},
		{"", "", true},
		{strings.Repeat("a", 300), "", true},
	}
	for _, tt := range tests {
		got, err := sanitizeUser(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("sanitizeUser(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sanitizeUser(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("sanitizeUser(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenModeRoundTrip(t *testing.T) {
	h, dataDir := newTestServer(t, Config{})

	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("GET before save: got %d, want 404", w.Code)
	}
	const board = "# My Board\n\n## todo\n- thing\n"
	if w := doReq(t, h, "PUT", "/api/board", board, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET after save: got %d, want 200", w.Code)
	}
	if w.Body.String() != board {
		t.Errorf("GET body = %q, want %q", w.Body.String(), board)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "default.md")); err != nil {
		t.Errorf("expected default.md on disk: %v", err)
	}

	// Named user goes to its own sanitized file.
	hdr := map[string]string{"X-KB-User": "Alice"}
	if w := doReq(t, h, "PUT", "/api/board", "# alice\n", hdr); w.Code != http.StatusNoContent {
		t.Fatalf("PUT as Alice: got %d, want 204", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "alice.md")); err != nil {
		t.Errorf("expected alice.md on disk: %v", err)
	}
	if w := doReq(t, h, "GET", "/api/board", "", hdr); w.Body.String() != "# alice\n" {
		t.Errorf("GET as Alice = %q, want %q", w.Body.String(), "# alice\n")
	}
}

func TestTokenMode(t *testing.T) {
	h, _ := newTestServer(t, Config{Token: "s3cret"})

	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d, want 401", w.Code)
	}
	wrong := map[string]string{"Authorization": "Bearer wrong", "X-KB-User": "bob"}
	if w := doReq(t, h, "GET", "/api/board", "", wrong); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", w.Code)
	}
	noUser := map[string]string{"Authorization": "Bearer s3cret"}
	if w := doReq(t, h, "PUT", "/api/board", "# b\n", noUser); w.Code != http.StatusBadRequest {
		t.Errorf("missing X-KB-User: got %d, want 400", w.Code)
	}
	good := map[string]string{"Authorization": "Bearer s3cret", "X-KB-User": "bob"}
	if w := doReq(t, h, "PUT", "/api/board", "# b\n", good); w.Code != http.StatusNoContent {
		t.Fatalf("PUT with token: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", good)
	if w.Code != http.StatusOK || w.Body.String() != "# b\n" {
		t.Errorf("GET with token: got %d body %q", w.Code, w.Body.String())
	}
	// Health stays unauthenticated even in token mode.
	if w := doReq(t, h, "GET", "/api/health", "", nil); w.Code != http.StatusOK {
		t.Errorf("health: got %d, want 200", w.Code)
	}
}

func TestPutBodyLimit(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	over := strings.Repeat("x", maxBodyBytes+1)
	if w := doReq(t, h, "PUT", "/api/board", over, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized PUT: got %d, want 413", w.Code)
	}
	exact := strings.Repeat("x", maxBodyBytes)
	if w := doReq(t, h, "PUT", "/api/board", exact, nil); w.Code != http.StatusNoContent {
		t.Errorf("exactly 1 MiB PUT: got %d, want 204", w.Code)
	}
}

func TestHealthAndStatic(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d, want 200", w.Code)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Errorf("health body = %q", w.Body.String())
	}
	if w := doReq(t, h, "GET", "/", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("GET /: got %d body %q", w.Code, w.Body.String())
	}
	// SPA fallback for unknown non-/api paths.
	if w := doReq(t, h, "GET", "/some/client/route", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("SPA fallback: got %d body %q", w.Code, w.Body.String())
	}
	// Unknown /api paths must 404, not fall back to index.html.
	if w := doReq(t, h, "GET", "/api/nope", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("GET /api/nope: got %d, want 404", w.Code)
	}
	// Wrong method on /api/board falls through to the catch-all, whose /api
	// guard rejects it rather than serving the SPA.
	if w := doReq(t, h, "POST", "/api/board", "x", nil); w.Code != http.StatusNotFound {
		t.Errorf("POST /api/board: got %d, want 404", w.Code)
	}
}

func writeJWKS(w http.ResponseWriter, pub *rsa.PublicKey, kid string) {
	doc := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func TestEntraAuth(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const kid = "test-kid"
	var fetches atomic.Int32
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		writeJWKS(w, &key.PublicKey, kid)
	}))
	defer jwksSrv.Close()

	const tenant = "11111111-2222-3333-4444-555555555555"
	const clientID = "app-client-id"
	issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
	dataDir := t.TempDir()
	h := newServer(Config{
		DataDir:  dataDir,
		TenantID: tenant,
		ClientID: clientID,
		JWKSURL:  jwksSrv.URL,
	}, testStatic)

	const oid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	base := func() jwt.MapClaims {
		now := time.Now()
		return jwt.MapClaims{
			"iss":   issuer,
			"aud":   clientID,
			"exp":   now.Add(time.Hour).Unix(),
			"nbf":   now.Add(-time.Minute).Unix(),
			"iat":   now.Add(-time.Minute).Unix(),
			"oid":   oid,
			"email": "user@example.com",
		}
	}
	sign := func(claims jwt.MapClaims, kid string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		if kid != "" {
			tok.Header["kid"] = kid
		}
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}

	tests := []struct {
		name  string
		token func() string
		want  int
	}{
		{"valid", func() string { return sign(base(), kid) }, http.StatusNoContent},
		{"wrong issuer", func() string {
			c := base()
			c["iss"] = "https://login.microsoftonline.com/other-tenant/v2.0"
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"wrong audience", func() string {
			c := base()
			c["aud"] = "other-app"
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"expired", func() string {
			c := base()
			c["exp"] = time.Now().Add(-time.Hour).Unix()
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"not yet valid", func() string {
			c := base()
			c["nbf"] = time.Now().Add(time.Hour).Unix()
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"missing exp", func() string {
			c := base()
			delete(c, "exp")
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"unknown kid", func() string { return sign(base(), "other-kid") }, http.StatusUnauthorized},
		{"missing kid", func() string { return sign(base(), "") }, http.StatusUnauthorized},
		{"hs256 rejected", func() string {
			tok := jwt.NewWithClaims(jwt.SigningMethodHS256, base())
			tok.Header["kid"] = kid
			s, err := tok.SignedString([]byte("shared-secret"))
			if err != nil {
				t.Fatalf("sign hs256: %v", err)
			}
			return s
		}, http.StatusUnauthorized},
		{"no oid claim", func() string {
			c := base()
			delete(c, "oid")
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"garbage token", func() string { return "not.a.jwt" }, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := map[string]string{"Authorization": "Bearer " + tt.token()}
			w := doReq(t, h, "PUT", "/api/board", "# board\n", hdr)
			if w.Code != tt.want {
				t.Fatalf("got %d, want %d (body=%s)", w.Code, tt.want, w.Body)
			}
		})
	}

	// The immutable oid claim decides the board file — never email.
	if _, err := os.Stat(filepath.Join(dataDir, oid+".md")); err != nil {
		t.Errorf("expected %s.md on disk: %v", oid, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "user@example.com.md")); err == nil {
		t.Error("board file keyed on mutable email claim; want oid")
	}

	// Mutable claims are never accepted as identity, even when present.
	c := base()
	delete(c, "oid")
	c["preferred_username"] = "Fallback.User@Example.com"
	hdr := map[string]string{"Authorization": "Bearer " + sign(c, kid)}
	if w := doReq(t, h, "PUT", "/api/board", "# fb\n", hdr); w.Code != http.StatusUnauthorized {
		t.Fatalf("email/preferred_username without oid: got %d, want 401 (body=%s)", w.Code, w.Body)
	}

	// No Authorization header at all.
	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("missing header: got %d, want 401", w.Code)
	}

	// JWKS is fetched once and cached (unknown kid within cooldown does not refetch).
	if n := fetches.Load(); n != 1 {
		t.Errorf("jwks fetched %d times, want 1", n)
	}
}
