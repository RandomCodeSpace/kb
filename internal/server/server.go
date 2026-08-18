// Package server implements the kb JSON/markdown HTTP API.
//
// Auth mode is decided by Config at construction:
//  1. TenantID + ClientID -> Entra ID bearer tokens (RS256, tenant JWKS,
//     iss/aud/exp/nbf checks; no Graph calls). Identity is the immutable oid
//     claim — never email/preferred_username, which are mutable and
//     reassignable.
//  2. Token -> shared-secret bearer token, identity from X-KB-User.
//  3. otherwise -> open mode, identity from X-KB-User or "default".
//
// Every registered API route except /api/health and /api/config sits behind
// that auth check. Every request matching a registered method and path first
// passes the Host/Origin guard in security.go; unmatched requests return 404.
package server

import (
	"fmt"
	"net/http"
	"path"
	"sync"
	"time"

	kbai "github.com/RandomCodeSpace/kb/internal/ai"
	kbforge "github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const maxBodyBytes = 1 << 20 // 1 MiB request body limit

// Config holds everything the handler needs; no globals so tests can
// construct servers freely.
type Config struct {
	Token    string // KB_TOKEN shared-secret mode
	TenantID string // KB_AZURE_TENANT_ID
	ClientID string // KB_AZURE_CLIENT_ID

	// AllowedHosts is the comma-separated KB_ALLOWED_HOSTS value: extra Host
	// header values the API accepts. Loopback and localhost are always
	// accepted, so this is only needed when kb is served on a real hostname.
	AllowedHosts string

	// JWKSURL and Issuer default from TenantID when empty; overridable so
	// tests can point at a fake IdP.
	JWKSURL string
	Issuer  string

	// SkillsDir holds user-supplied skill files that override the embedded
	// ones by name. An absent directory means "no overrides"; a broken file
	// in it is an error, never a silently dropped skill.
	SkillsDir string
}

func newLinkClient() *http.Client { return kbai.NewLinkClient() }

type server struct {
	cfg          Config
	store        *store.Store
	aiClient     *http.Client // injectable for tests
	forgeClient  *http.Client // injectable for tests
	forgeEngine  *kbforge.Service
	forgeOnce    sync.Once
	linkClient   *http.Client // injectable for tests
	issuer       string
	jwks         *jwksCache
	authenticate func(*http.Request) (string, error)
	allowedHosts map[string]bool
	pinHost      bool

	// afterConditionalBoardSnapshot is a deterministic test seam for a writer
	// between the handler's preliminary read and the store-owned predicate.
	afterConditionalBoardSnapshot func()
}

// New builds the API-only HTTP handler for cfg backed by st.
func New(cfg Config, st *store.Store) http.Handler {
	return newServer(cfg, st).handler()
}

// newServer wires the concrete server; split from New so tests can reach
// into the struct (e.g. to swap aiClient) before building the handler.
func newServer(cfg Config, st *store.Store) *server {
	s := &server{
		cfg:          cfg,
		store:        st,
		aiClient:     newAIClient(),
		forgeClient:  newForgeClient(),
		linkClient:   newLinkClient(),
		allowedHosts: parseAllowedHosts(cfg.AllowedHosts),
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
		// Nothing else stands between a rebound name and the API here, so
		// open mode always pins Host. See withAPIGuard.
		s.pinHost = true
	}
	if len(s.allowedHosts) > 0 {
		s.pinHost = true
	}
	return s
}

// handler assembles the route table.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, handler http.HandlerFunc) {
		// Guard only concrete API routes. Requests that do not match both a
		// documented path and method reach the 404 catch-all directly.
		mux.Handle(pattern, s.withAPIGuard(handler))
	}
	api("GET /api/health", s.handleHealth)
	api("GET /api/config", s.handleConfig)
	api("GET /api/board", s.withAuth(s.handleGetBoard))
	api("PUT /api/board", s.withAuth(s.handlePutBoard))
	api("GET /api/tasks", s.withAuth(s.handleListTasks))
	api("POST /api/tasks", s.withAuth(s.handleCreateTask))
	api("GET /api/tasks/{ref}", s.withAuth(s.handleGetTask))
	api("PATCH /api/tasks/{ref}", s.withAuth(s.handlePatchTask))
	api("DELETE /api/tasks/{ref}", s.withAuth(s.handleDeleteTask))
	api("GET /api/tasks/{ref}/comments", s.withAuth(s.handleListTaskComments))
	api("POST /api/tasks/{ref}/comments", s.withAuth(s.handleAddTaskComment))
	api("DELETE /api/comments/{id}", s.withAuth(s.handleDeleteComment))
	api("POST /api/links", s.withAuth(s.handleCreateLink))
	api("DELETE /api/links", s.withAuth(s.handleDeleteLink))
	api("GET /api/labels", s.withAuth(s.handleLabels))
	api("GET /api/similar", s.withAuth(s.handleSimilar))
	api("POST /api/tombstones", s.withAuth(s.handleTombstone))
	api("GET /api/settings", s.withAuth(s.handleGetSettings))
	api("PUT /api/settings", s.withAuth(s.handlePutSettings))
	api("POST /api/ai/test", s.withAuth(s.handleAITest))
	api("POST /api/ai/story", s.withAuth(s.handleAIStory))
	api("POST /api/ai/stories", s.withAuth(s.handleAIStories))
	api("POST /api/ai/run-skill", s.withAuth(s.handleAIRunSkill))
	api("POST /api/import/preview", s.withAuth(s.handleImportPreview))
	api("POST /api/import/links", s.withAuth(s.handleImportLinks))
	api("POST /api/import/provenance", s.withAuth(s.handleImportProvenance))
	api("POST /api/import/drift", s.withAuth(s.handleImportDrift))
	api("POST /api/import/drift/accept", s.withAuth(s.handleImportDriftAccept))
	api("GET /api/integrations", s.withAuth(s.handleGetIntegrations))
	api("PUT /api/integrations/{name}", s.withAuth(s.handlePutIntegration))
	api("DELETE /api/integrations/{name}", s.withAuth(s.handleDeleteIntegration))
	api("POST /api/integrations/{name}/test", s.withAuth(s.handleTestIntegration))
	// A method-agnostic catch-all deliberately returns 404 for non-API paths,
	// unknown API paths, and wrong methods. The optional server has no web UI.
	mux.HandleFunc("/", http.NotFound)
	return withLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux canonicalizes paths before matching and treats HEAD as GET.
		// Neither behavior is part of this API: undocumented paths and methods
		// are 404s, not redirects or implicit aliases.
		if r.Method == http.MethodHead || r.URL.Path != path.Clean(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

// --- auth ---

// authError carries an HTTP status for auth failures.
