package server

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

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
	st := newTestStore(t)
	h := New(Config{
		TenantID: tenant,
		ClientID: clientID,
		JWKSURL:  jwksSrv.URL,
	}, st)

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
			w := putBoard(t, h, "# board\n", hdr)
			if w.Code != tt.want {
				t.Fatalf("got %d, want %d (body=%s)", w.Code, tt.want, w.Body)
			}
		})
	}

	// The immutable oid claim decides the board identity — never email.
	if has, err := st.HasBoard(oid); err != nil || !has {
		t.Errorf("HasBoard(oid) = %v, %v; want true", has, err)
	}
	if has, err := st.HasBoard("user@example.com"); err != nil || has {
		t.Errorf("board keyed on mutable email claim; want oid only (has=%v err=%v)", has, err)
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
