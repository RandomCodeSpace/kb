// Command kb serves the embedded kanban SPA and a small markdown-board API.
//
// Auth mode is decided by environment at startup:
//  1. KB_AZURE_TENANT_ID + KB_AZURE_CLIENT_ID -> Entra ID bearer tokens
//     (RS256, tenant JWKS, iss/aud/exp/nbf checks; no Graph calls). Identity
//     is the immutable oid claim — never email/preferred_username, which are
//     mutable and reassignable.
//  2. KB_TOKEN -> shared-secret bearer token, identity from X-KB-User.
//  3. otherwise -> open mode, identity from X-KB-User or "default".
package main

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

//go:embed all:dist
var distFS embed.FS

const maxBodyBytes = 1 << 20 // 1 MiB PUT limit

// Config holds everything the handler needs; no globals so tests can
// construct servers freely.
type Config struct {
	DataDir  string
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
	issuer       string
	jwks         *jwksCache
	authenticate func(*http.Request) (string, error)
}

// newServer builds the full HTTP handler (API + embedded SPA) for cfg.
func newServer(cfg Config, static fs.FS) http.Handler {
	s := &server{cfg: cfg, static: static, fileServer: http.FileServerFS(static)}
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/board", s.withAuth(s.handleGetBoard))
	mux.HandleFunc("PUT /api/board", s.withAuth(s.handlePutBoard))
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
	// unverified guest emails), so they must never select the board file.
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

// --- user -> filename ---

// sanitizeUser maps a user identity to a safe filename stem: lowercased, and
// any char outside [a-z0-9._@-] is rejected (never substituted — substitution
// would collapse distinct identities onto the same board file). Empty
// identities, names starting with '.', and over-long names are rejected. Path
// separators are outside the allowed set, so traversal is impossible.
func sanitizeUser(user string) (string, error) {
	if user == "" {
		return "", errors.New("empty user identity")
	}
	out := strings.ToLower(user)
	for _, r := range out {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '@', r == '-':
		default:
			return "", fmt.Errorf("invalid character in user identity %q", user)
		}
	}
	if strings.HasPrefix(out, ".") {
		return "", fmt.Errorf("invalid user identity %q", user)
	}
	if len(out) > 250 {
		return "", errors.New("user identity too long")
	}
	return out, nil
}

// --- handlers ---

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *server) handleGetBoard(w http.ResponseWriter, _ *http.Request, user string) {
	b, err := os.ReadFile(filepath.Join(s.cfg.DataDir, user+".md"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "no board saved", http.StatusNotFound)
			return
		}
		log.Printf("read board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *server) handlePutBoard(w http.ResponseWriter, r *http.Request, user string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if err := s.writeBoard(user, body); err != nil {
		log.Printf("write board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeBoard writes atomically: temp file in the same directory, then rename.
func (s *server) writeBoard(user string, body []byte) error {
	tmp, err := os.CreateTemp(s.cfg.DataDir, ".kb-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(s.cfg.DataDir, user+".md"))
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

// --- main ---

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	defaultData := os.Getenv("KB_DATA")
	if defaultData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory, set KB_DATA or --data: %v", err)
		}
		defaultData = filepath.Join(home, ".local", "share", "kb")
	}
	port := flag.String("port", envOr("KB_PORT", "8080"), "listen port (env KB_PORT)")
	dataDir := flag.String("data", defaultData, "board storage directory (env KB_DATA)")
	flag.Parse()

	cfg := Config{
		DataDir:  *dataDir,
		Token:    os.Getenv("KB_TOKEN"),
		TenantID: os.Getenv("KB_AZURE_TENANT_ID"),
		ClientID: os.Getenv("KB_AZURE_CLIENT_ID"),
	}
	if (cfg.TenantID != "") != (cfg.ClientID != "") {
		log.Fatal("KB_AZURE_TENANT_ID and KB_AZURE_CLIENT_ID must be set together")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}
	static, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatalf("embedded dist: %v", err)
	}

	mode := "open"
	switch {
	case cfg.TenantID != "":
		mode = "entra"
	case cfg.Token != "":
		mode = "token"
	}
	log.Printf("kb listening on :%s (auth: %s, data: %s)", *port, mode, cfg.DataDir)
	if err := http.ListenAndServe(":"+*port, newServer(cfg, static)); err != nil {
		log.Fatal(err)
	}
}
