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
// Every /api/* route except /api/health and /api/config sits behind that
// auth check, and every /api/* request first passes the Host/Origin guard in
// security.go.
package server

import (
	"fmt"
	"io/fs"
	"net/http"
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
	static       fs.FS
	fileServer   http.Handler
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
	csp          string // SPA Content-Security-Policy, fixed at construction

	// afterConditionalBoardSnapshot is a deterministic test seam for a writer
	// between the handler's preliminary read and the store-owned predicate.
	afterConditionalBoardSnapshot func()
}

// New builds the full HTTP handler (API + embedded SPA) for cfg backed by st.
func New(cfg Config, static fs.FS, st *store.Store) http.Handler {
	return newServer(cfg, static, st).handler()
}

// newServer wires the concrete server; split from New so tests can reach
// into the struct (e.g. to swap aiClient) before building the handler.
func newServer(cfg Config, static fs.FS, st *store.Store) *server {
	s := &server{
		cfg:          cfg,
		static:       static,
		fileServer:   http.FileServerFS(static),
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
	// Entra mode is the only mode whose SPA may reach off-origin, so the
	// policy is decided once, from the same condition that selects authEntra.
	s.csp = contentSecurityPolicy(cfg.TenantID != "" && cfg.ClientID != "")
	return s
}

// handler assembles the route table.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/board", s.withAuth(s.handleGetBoard))
	mux.HandleFunc("PUT /api/board", s.withAuth(s.handlePutBoard))
	mux.HandleFunc("GET /api/tasks", s.withAuth(s.handleListTasks))
	mux.HandleFunc("POST /api/tasks", s.withAuth(s.handleCreateTask))
	mux.HandleFunc("GET /api/tasks/{ref}", s.withAuth(s.handleGetTask))
	mux.HandleFunc("PATCH /api/tasks/{ref}", s.withAuth(s.handlePatchTask))
	mux.HandleFunc("DELETE /api/tasks/{ref}", s.withAuth(s.handleDeleteTask))
	mux.HandleFunc("GET /api/tasks/{ref}/comments", s.withAuth(s.handleListTaskComments))
	mux.HandleFunc("POST /api/tasks/{ref}/comments", s.withAuth(s.handleAddTaskComment))
	mux.HandleFunc("DELETE /api/comments/{id}", s.withAuth(s.handleDeleteComment))
	mux.HandleFunc("POST /api/links", s.withAuth(s.handleCreateLink))
	mux.HandleFunc("DELETE /api/links", s.withAuth(s.handleDeleteLink))
	mux.HandleFunc("GET /api/labels", s.withAuth(s.handleLabels))
	mux.HandleFunc("GET /api/similar", s.withAuth(s.handleSimilar))
	mux.HandleFunc("POST /api/tombstones", s.withAuth(s.handleTombstone))
	mux.HandleFunc("GET /api/settings", s.withAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.withAuth(s.handlePutSettings))
	mux.HandleFunc("POST /api/ai/test", s.withAuth(s.handleAITest))
	mux.HandleFunc("POST /api/ai/story", s.withAuth(s.handleAIStory))
	mux.HandleFunc("POST /api/ai/stories", s.withAuth(s.handleAIStories))
	mux.HandleFunc("POST /api/ai/run-skill", s.withAuth(s.handleAIRunSkill))
	mux.HandleFunc("POST /api/import/preview", s.withAuth(s.handleImportPreview))
	mux.HandleFunc("POST /api/import/links", s.withAuth(s.handleImportLinks))
	mux.HandleFunc("POST /api/import/provenance", s.withAuth(s.handleImportProvenance))
	mux.HandleFunc("POST /api/import/drift", s.withAuth(s.handleImportDrift))
	mux.HandleFunc("POST /api/import/drift/accept", s.withAuth(s.handleImportDriftAccept))
	mux.HandleFunc("GET /api/integrations", s.withAuth(s.handleGetIntegrations))
	mux.HandleFunc("PUT /api/integrations/{name}", s.withAuth(s.handlePutIntegration))
	mux.HandleFunc("DELETE /api/integrations/{name}", s.withAuth(s.handleDeleteIntegration))
	mux.HandleFunc("POST /api/integrations/{name}/test", s.withAuth(s.handleTestIntegration))
	mux.HandleFunc("/", s.handleStatic)
	return withLogging(s.withAPIGuard(mux))
}

// --- auth ---

// authError carries an HTTP status for auth failures.
