// Package server implements the kb HTTP handler: the JSON/markdown API and
// the embedded SPA.
//
// Auth mode is decided by Config at construction:
//  1. TenantID + ClientID -> Entra ID bearer tokens (RS256, tenant JWKS,
//     iss/aud/exp/nbf checks; no Graph calls). Identity is the immutable oid
//     claim — never email/preferred_username, which are mutable and
//     reassignable.
//  2. Token -> shared-secret bearer token, identity from X-KB-User.
//  3. otherwise -> open mode, identity from X-KB-User or "default".
//
// Every /api/* route except /api/health sits behind that auth check.
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
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const maxBodyBytes = 1 << 20 // 1 MiB request body limit

// Config holds everything the handler needs; no globals so tests can
// construct servers freely.
type Config struct {
	Token    string // KB_TOKEN shared-secret mode
	TenantID string // KB_AZURE_TENANT_ID
	ClientID string // KB_AZURE_CLIENT_ID

	// JWKSURL and Issuer default from TenantID when empty; overridable so
	// tests can point at a fake IdP.
	JWKSURL string
	Issuer  string
}

type server struct {
	cfg          Config
	static       fs.FS
	fileServer   http.Handler
	store        *store.Store
	aiClient     *http.Client // injectable for tests
	issuer       string
	jwks         *jwksCache
	authenticate func(*http.Request) (string, error)
}

// New builds the full HTTP handler (API + embedded SPA) for cfg backed by st.
func New(cfg Config, static fs.FS, st *store.Store) http.Handler {
	return newServer(cfg, static, st).handler()
}

// newServer wires the concrete server; split from New so tests can reach
// into the struct (e.g. to swap aiClient) before building the handler.
func newServer(cfg Config, static fs.FS, st *store.Store) *server {
	s := &server{
		cfg:        cfg,
		static:     static,
		fileServer: http.FileServerFS(static),
		store:      st,
		aiClient:   newAIClient(),
	}
	switch {
	case cfg.TenantID != "" && cfg.ClientID != "":
		s.issuer = cfg.Issuer
		if s.issuer == "" {
			s.issuer = fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", cfg.TenantID)
		}
		jwksURL := cfg.JWKSURL
		if jwksURL == "" {
			jwksURL = fmt.Sprintf("https://login.microsoftonline.com/%s/discovery/v2.0/keys", cfg.TenantID)
		}
		s.jwks = &jwksCache{url: jwksURL, client: &http.Client{Timeout: 10 * time.Second}}
		s.authenticate = s.authEntra
	case cfg.Token != "":
		s.authenticate = s.authToken
	default:
		s.authenticate = s.authOpen
	}
	return s
}

// handler assembles the route table.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/board", s.withAuth(s.handleGetBoard))
	mux.HandleFunc("PUT /api/board", s.withAuth(s.handlePutBoard))
	mux.HandleFunc("GET /api/labels", s.withAuth(s.handleLabels))
	mux.HandleFunc("GET /api/settings", s.withAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.withAuth(s.handlePutSettings))
	mux.HandleFunc("POST /api/ai/test", s.withAuth(s.handleAITest))
	mux.HandleFunc("POST /api/ai/story", s.withAuth(s.handleAIStory))
	mux.HandleFunc("/", s.handleStatic)
	return withLogging(mux)
}

// --- auth ---

// authError carries an HTTP status for auth failures.
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

// --- handlers ---

// writeJSON encodes v with the JSON content type; encode errors after the
// header is out can only be logged.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *server) handleGetBoard(w http.ResponseWriter, _ *http.Request, user string) {
	has, err := s.store.HasBoard(user)
	if err != nil {
		log.Printf("read board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	if !has {
		http.Error(w, "no board saved", http.StatusNotFound)
		return
	}
	b, err := s.store.Board(user)
	if err != nil {
		log.Printf("read board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, board.Serialize(b))
}

func (s *server) handlePutBoard(w http.ResponseWriter, r *http.Request, user string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	if err := s.store.ReplaceBoard(user, board.Parse(string(body))); err != nil {
		log.Printf("write board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleLabels(w http.ResponseWriter, _ *http.Request, user string) {
	labels, err := s.store.Labels(user)
	if err != nil {
		log.Printf("labels for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	if labels == nil {
		labels = []string{}
	}
	writeJSON(w, labels)
}

// settingsResponse is the client-visible settings view; the key itself is
// never returned, only whether one is stored.
type settingsResponse struct {
	BaseURL string `json:"ai_base_url"`
	Model   string `json:"ai_model"`
	HasKey  bool   `json:"has_key"`
}

func (s *server) handleGetSettings(w http.ResponseWriter, _ *http.Request, user string) {
	set, err := s.store.AISettings(user)
	if err != nil {
		log.Printf("settings for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, settingsResponse{BaseURL: set.BaseURL, Model: set.Model, HasKey: set.HasKey})
}

func (s *server) handlePutSettings(w http.ResponseWriter, r *http.Request, user string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		BaseURL *string `json:"ai_base_url"`
		Model   *string `json:"ai_model"`
		Key     *string `json:"ai_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.BaseURL != nil && strings.TrimSpace(*req.BaseURL) != "" {
		if _, err := aiEndpoint(*req.BaseURL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	keyCleared, err := s.store.SetAISettings(user, req.BaseURL, req.Model, req.Key)
	if err != nil {
		log.Printf("save settings for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	if keyCleared {
		// The base URL moved to a different scheme/host, so the stored key
		// was dropped with it; the client must ask for the key again.
		writeJSON(w, map[string]bool{"key_cleared": true})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readBody reads a size-capped request body, writing the error response
// itself when reading fails.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "read error", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// handleStatic serves the embedded SPA with an index.html fallback for
// unknown non-/api paths (client-side routing).
func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if _, err := fs.Stat(s.static, name); err == nil {
		s.fileServer.ServeHTTP(w, r)
		return
	}
	http.ServeFileFS(w, r, s.static, "index.html")
}

// --- logging ---

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// withLogging logs method, path, status, and duration. Never headers/tokens.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Microsecond))
	})
}
