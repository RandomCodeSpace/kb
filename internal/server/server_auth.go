package server

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/RandomCodeSpace/kb/internal/store"
)

type authError struct {
	code int
	msg  string
}

func (e *authError) Error() string { return e.msg }

func (s *server) withAuth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authenticate(r)
		if err != nil {
			code := http.StatusUnauthorized
			var ae *authError
			if errors.As(err, &ae) {
				code = ae.code
			}
			http.Error(w, err.Error(), code)
			return
		}
		name, err := sanitizeUser(user)
		if err != nil {
			http.Error(w, "invalid user identity", http.StatusBadRequest)
			return
		}
		next(w, r, name)
	}
}

func (s *server) authOpen(r *http.Request) (string, error) {
	user := strings.TrimSpace(r.Header.Get("X-KB-User"))
	if user == "" {
		user = "default"
	}
	return user, nil
}

func (s *server) authToken(r *http.Request) (string, error) {
	raw, ok := bearerToken(r)
	if !ok {
		return "", &authError{http.StatusUnauthorized, "missing bearer token"}
	}
	if !tokensEqual(raw, s.cfg.Token) {
		return "", &authError{http.StatusUnauthorized, "invalid token"}
	}
	user := strings.TrimSpace(r.Header.Get("X-KB-User"))
	if user == "" {
		return "", &authError{http.StatusBadRequest, "X-KB-User header required"}
	}
	return user, nil
}

func (s *server) authEntra(r *http.Request) (string, error) {
	raw, ok := bearerToken(r)
	if !ok {
		return "", &authError{http.StatusUnauthorized, "missing bearer token"}
	}
	token, err := jwt.Parse(raw, s.entraKeyfunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.cfg.ClientID),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		log.Printf("auth: token rejected: %v", err)
		return "", &authError{http.StatusUnauthorized, "invalid token"}
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", &authError{http.StatusUnauthorized, "invalid token claims"}
	}
	// Key identity on the immutable object ID. email and preferred_username
	// are mutable and non-unique (UPN reassignment, self-service aliases,
	// unverified guest emails), so they must never select the board.
	identity, _ := claims["oid"].(string)
	if identity == "" {
		return "", &authError{http.StatusUnauthorized, "token has no oid claim"}
	}
	return identity, nil
}

func (s *server) entraKeyfunc(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, errors.New("token missing kid header")
	}
	return s.jwks.key(kid)
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	return tok, tok != ""
}

// tokensEqual compares in constant time; hashing first normalizes length so
// the comparison leaks nothing about the configured secret.
func tokensEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// --- JWKS cache ---

const (
	jwksTTL             = time.Hour
	jwksRefetchCooldown = 30 * time.Second
)

// jwksCache lazily fetches and caches the tenant signing keys. The mutex
// both guards the map and single-flights concurrent fetches.
type jwksCache struct {
	url    string
	client *http.Client

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func (c *jwksCache) key(kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fresh := !c.fetched.IsZero() && time.Since(c.fetched) < jwksTTL
	if k, ok := c.keys[kid]; ok && fresh {
		return k, nil
	}
	// Fetch lazily on first use, on the hourly refresh, and on unknown kid
	// (cooldown-limited so bogus kids cannot hammer the IdP).
	if !fresh || time.Since(c.fetched) > jwksRefetchCooldown {
		if err := c.fetchLocked(); err != nil {
			if len(c.keys) == 0 {
				return nil, err
			}
			log.Printf("jwks: refresh failed, using cached keys: %v", err)
		}
	}
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("no signing key for kid %q", kid)
}

func (c *jwksCache) fetchLocked() error {
	resp, err := c.client.Get(c.url)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read jwks: %w", err)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pub, err := rsaKeyFromJWK(k.N, k.E)
		if err != nil {
			log.Printf("jwks: skipping key %q: %v", k.Kid, err)
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("jwks: no usable RSA signing keys")
	}
	c.keys = keys
	c.fetched = time.Now()
	return nil
}

func rsaKeyFromJWK(n, e string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil {
		return nil, fmt.Errorf("modulus: %w", err)
	}
	eb, err := base64.RawURLEncoding.DecodeString(e)
	if err != nil {
		return nil, fmt.Errorf("exponent: %w", err)
	}
	for len(eb) > 1 && eb[0] == 0 {
		eb = eb[1:]
	}
	if len(nb) == 0 || len(eb) == 0 || len(eb) > 4 {
		return nil, errors.New("invalid modulus/exponent length")
	}
	exp := 0
	for _, b := range eb {
		exp = exp<<8 | int(b)
	}
	if exp < 3 {
		return nil, errors.New("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: exp}, nil
}

// --- user identity ---

// sanitizeUser maps a user identity to a safe storage key. The shared rules
// live in store.SanitizeUser so the CLI and MCP entry points normalize
// identically and every surface addresses the same board.
func sanitizeUser(user string) (string, error) {
	return store.SanitizeUser(user)
}
