package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// --- handlers ---

type similarItem struct {
	ID       string `json:"id,omitempty"`
	Title    string `json:"title"`
	Status   string `json:"status,omitempty"`
	Via      string `json:"via"`
	Link     string `json:"link,omitempty"`
	Reason   string `json:"reason,omitempty"`
	KilledAt string `json:"killed_at,omitempty"`
}

type similarResponse struct {
	Items []similarItem `json:"items"`
}

func (s *server) handleSimilar(w http.ResponseWriter, r *http.Request, user string) {
	query := r.URL.Query().Get("q")
	response := similarResponse{Items: []similarItem{}}
	if utf8.RuneCountInString(strings.TrimSpace(query)) < 3 {
		writeJSON(w, response)
		return
	}
	hits, err := s.store.SearchSimilar(user, query, r.URL.Query().Get("exclude"), 3)
	if err != nil {
		log.Printf("search similar for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	for _, hit := range hits {
		response.Items = append(response.Items, similarItem{
			ID: hit.ID, Title: hit.Title, Status: hit.Status, Via: hit.Via, Link: hit.Link,
			Reason: hit.Reason, KilledAt: hit.KilledAt,
		})
	}
	writeJSON(w, response)
}

type tombstoneRequest struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

func (s *server) handleTombstone(w http.ResponseWriter, r *http.Request, user string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req tombstoneRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.TaskID) == "" {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Reason) == "" ||
		len(req.Reason) > 2000 ||
		strings.ContainsAny(req.Reason, "\r\n") {
		http.Error(w, "invalid tombstone reason", http.StatusBadRequest)
		return
	}
	if err := s.store.RecordTombstone(user, req.TaskID, req.Reason); err != nil {
		log.Printf("record tombstone for %s: %v", logSafe(user), err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

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

// boardETag is the board's version token: a strong ETag over the serialized
// wire form, so any write by any surface (SPA, CLI, MCP) changes it.
func boardETag(md string) string {
	sum := sha256.Sum256([]byte(md))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// currentBoard returns the stored board's wire form and version token. A
// user with no board yet has the empty wire form, so a client that has never
// seen a board and a client whose board was deleted agree on the token.
func (s *server) currentBoard(user string) (string, string, error) {
	has, err := s.store.HasBoard(user)
	if err != nil {
		return "", "", err
	}
	if !has {
		return "", boardETag(""), nil
	}
	b, err := s.store.Board(user)
	if err != nil {
		return "", "", err
	}
	md := board.Serialize(b)
	return md, boardETag(md), nil
}

func (s *server) handleGetBoard(w http.ResponseWriter, _ *http.Request, user string) {
	md, etag, err := s.currentBoard(user)
	if err != nil {
		log.Printf("read board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	// The 404 carries the token too: it is the version of "no board", and a
	// client that never got one would send no If-Match at all, making its
	// first PUT unconditional — the very write most likely to land on a board
	// the CLI or MCP created in the meantime.
	w.Header().Set("ETag", etag)
	if md == "" {
		http.Error(w, "no board saved", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, md)
}

// handlePutBoard replaces the whole board. A full-board PUT would otherwise
// silently delete tasks the CLI or MCP created since the client last read,
// so a client that sends If-Match with the token from its GET is told 409
// instead, and can refetch and merge. Clients that send no If-Match keep the
// old last-writer-wins behavior.
//
// The token is content-derived rather than a store-side counter on purpose:
// the CLI and MCP write to SQLite in their own processes, where no counter
// this server keeps would ever be bumped. If the store later grows a
// compare-and-swap ReplaceBoard, the lock and the re-read below are the two
// places that would collapse into it.
func (s *server) handlePutBoard(w http.ResponseWriter, r *http.Request, user string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	// Held across the compare and the write, and the compare re-reads the
	// store inside the lock: comparing against anything cached from the
	// client's earlier GET would leave a window for a cross-process write.
	mu := s.boardLocks.get(user)
	mu.Lock()
	defer mu.Unlock()
	if want := strings.TrimSpace(r.Header.Get("If-Match")); want != "" {
		_, etag, err := s.currentBoard(user)
		if err != nil {
			log.Printf("read board for %s: %v", user, err)
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		if want != "*" && !etagMatches(want, etag) {
			w.Header().Set("ETag", etag)
			http.Error(w, "board changed since it was read", http.StatusConflict)
			return
		}
	}
	if err := s.store.ReplaceBoard(user, board.Parse(string(body))); err != nil {
		log.Printf("write board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	_, etag, err := s.currentBoard(user)
	if err != nil {
		log.Printf("read board for %s: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusNoContent)
}

// etagMatches reports whether any entry of an If-Match list equals etag. The
// weak prefix is stripped so a proxy that weakened the tag still matches;
// the token is content-derived either way.
func etagMatches(ifMatch, etag string) bool {
	for _, tag := range strings.Split(ifMatch, ",") {
		if strings.TrimPrefix(strings.TrimSpace(tag), "W/") == etag {
			return true
		}
	}
	return false
}

// configResponse carries the browser-side Entra IDs. Both are public by
// design — every MSAL SPA ships them in its bundle — but the endpoint must
// stay minimal: it is unauthenticated (the SPA needs it before login), so
// nothing else may ever be added here.
type configResponse struct {
	AzureClientID string `json:"azure_client_id"`
	AzureTenantID string `json:"azure_tenant_id"`
}

// handleConfig serves the runtime Entra configuration. The released binary
// is built without VITE_* values, so the SPA cannot learn them at build
// time; it reads them here instead. Unset env yields empty strings, which
// the SPA treats as "no Entra configured".
func (s *server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, configResponse{AzureClientID: s.cfg.ClientID, AzureTenantID: s.cfg.TenantID})
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
	if isAPIPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	s.setSecurityHeaders(w.Header())
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
// The path is percent-decoded by net/http, so a request for %0A would otherwise
// carry a raw newline into the log and let a caller forge whole entries.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, logSafe(r.URL.Path), sw.status, time.Since(start).Round(time.Microsecond))
	})
}
