package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/google/uuid"
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

const (
	maxSimilarExcludeLinks             = 100
	contentTypeHeader                  = "Content-Type"
	jsonMediaType                      = "application/json"
	storageErrorMessage                = "storage error"
	invalidJSONBodyMessage             = "invalid JSON body"
	invalidBoardPayloadMessage         = "invalid board payload"
	configuredSourceUnavailableMessage = "configured source unavailable"
	linkTagPrefix                      = "link::"
	connectionFailedMessage            = "connection failed"

	// missingIfMatchMessage is returned to any full-board PUT that would
	// otherwise get a last-writer-wins overwrite.
	missingIfMatchMessage = "board PUT requires If-Match: GET /api/board and send its ETag back as If-Match " +
		"(If-Match: * replaces whatever board already exists)"
)

func (s *server) handleSimilar(w http.ResponseWriter, r *http.Request, user string) {
	params := r.URL.Query()
	query := params.Get("q")
	response := similarResponse{Items: []similarItem{}}
	if utf8.RuneCountInString(strings.TrimSpace(query)) < 3 {
		writeJSON(w, response)
		return
	}
	excludeLinks := params["exclude_link"]
	if len(excludeLinks) > maxSimilarExcludeLinks {
		excludeLinks = excludeLinks[:maxSimilarExcludeLinks]
	}
	hits, err := s.store.SearchSimilar(user, query, params.Get("exclude"), excludeLinks, 3)
	if err != nil {
		log.Printf("search similar for %s: %s", logSafe(user), logSafe(err.Error()))
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
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
		http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
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
		if errors.Is(err, store.ErrTombstoneTaskNotCancelled) {
			http.Error(w, "tombstone target changed", http.StatusConflict)
			return
		}
		log.Printf("record tombstone for %s: %v", logSafe(user), err)
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON encodes v with the JSON content type; encode errors after the
// header is out can only be logged.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set(contentTypeHeader, jsonMediaType)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// acceptsJSON reports whether the caller explicitly accepts a JSON response.
// Wildcards retain each endpoint's legacy response because they do not opt in
// to the acknowledgement contract.
func acceptsJSON(r *http.Request) bool {
	for _, header := range r.Header.Values("Accept") {
		for _, value := range strings.Split(header, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(value))
			if err != nil || mediaType != jsonMediaType {
				continue
			}
			if q, ok := params["q"]; ok {
				quality, err := strconv.ParseFloat(q, 64)
				if err != nil || quality <= 0 || quality > 1 {
					continue
				}
			}
			return true
		}
	}
	return false
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(contentTypeHeader, jsonMediaType)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// boardETag makes the database revision opaque to HTTP clients. Revisions are
// monotonic rather than contiguous because one board replacement touches
// several trigger-observed rows.
func boardETag(revision int64) string {
	return `"r` + strconv.FormatInt(revision, 10) + `"`
}

// boardTaskIDs returns ids in the same status-first order Serialize writes.
// Board currently arrives in that order from the store too, but keeping this
// explicit prevents a future query-order change from corrupting positional
// browser-id acknowledgements.
func boardTaskIDs(b board.Board) []string {
	ids := make([]string, 0, len(b.Tasks))
	for _, status := range board.Statuses {
		for _, task := range b.Tasks {
			if task.Status == status {
				ids = append(ids, task.ID)
			}
		}
	}
	return ids
}

// currentBoard returns one transactionally consistent snapshot and its HTTP
// version token. Missing boards retain the last database revision so a delete
// and a never-created board cannot be mistaken for the same write version.
func (s *server) currentBoard(user string) (store.BoardSnapshot, string, error) {
	snapshot, err := s.store.ReadBoardSnapshot(user)
	if err != nil {
		return store.BoardSnapshot{}, "", err
	}
	return snapshot, boardETag(snapshot.Revision), nil
}

func (s *server) handleGetBoard(w http.ResponseWriter, r *http.Request, user string) {
	w.Header().Add("Vary", "Accept")
	snapshot, etag, err := s.currentBoard(user)
	if err != nil {
		log.Printf("read board for %s: %s", logSafe(user), logSafe(err.Error()))
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
		return
	}
	md := board.Serialize(snapshot.Board)
	// The 404 carries the token too: it is the version of "no board", so a
	// client whose first read found nothing still has something to send as
	// If-Match — without it every first write would be refused with 428.
	w.Header().Set("ETag", etag)
	if !snapshot.Exists {
		http.Error(w, "no board saved", http.StatusNotFound)
		return
	}
	if acceptsJSON(r) {
		writeJSON(w, struct {
			Board   string   `json:"board"`
			TaskIDs []string `json:"task_ids"`
		}{Board: md, TaskIDs: snapshot.TaskIDs})
		return
	}
	w.Header().Set(contentTypeHeader, "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, md)
}

func boardOperationID(r *http.Request) (string, error) {
	if len(r.Header.Values("Idempotency-Key")) > 1 {
		return "", errors.New("multiple idempotency keys")
	}
	operationID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if operationID == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(operationID)
	if err != nil || parsed.String() != strings.ToLower(operationID) {
		return "", errors.New("invalid idempotency key")
	}
	return operationID, nil
}

func boardPutRequestHash(mediaType string, body []byte) string {
	hashInput := append([]byte(mediaType+"\x00"), body...)
	return fmt.Sprintf("%x", sha256.Sum256(hashInput))
}

func (s *server) writeBoardJSONParseError(w http.ResponseWriter, user, want string) {
	condition := boardWriteCondition(want)
	if condition.Present {
		_, err := s.store.CheckBoardWriteCondition(user, condition)
		var conflict *store.RevisionConflictError
		switch {
		case errors.As(err, &conflict):
			writeBoardConflict(w, boardETag(conflict.CurrentRevision))
			return
		case err != nil:
			log.Printf("check board condition for %s: %s", logSafe(user), logSafe(err.Error()))
			http.Error(w, storageErrorMessage, http.StatusInternalServerError)
			return
		}
	}
	http.Error(w, invalidBoardPayloadMessage, http.StatusBadRequest)
}

func containsTaskCreation(ids []*string) bool {
	for _, id := range ids {
		if id == nil {
			return true
		}
	}
	return false
}

func (s *server) replaceBoard(
	user string,
	nextBoard board.Board,
	canonicalIDs []*string,
	condition store.BoardWriteCondition,
	operationID, requestHash string,
	isJSON bool,
) ([]string, int64, bool, error) {
	return s.store.ReplaceBoardConditionalWithReceipt(
		user, nextBoard, canonicalIDs, condition, operationID, requestHash,
		isJSON && containsTaskCreation(canonicalIDs),
	)
}

func writeBoardPutError(w http.ResponseWriter, user string, err error) {
	var conflict *store.RevisionConflictError
	switch {
	case errors.As(err, &conflict):
		writeBoardConflict(w, boardETag(conflict.CurrentRevision))
	case errors.Is(err, store.ErrInvalidTaskIDs):
		http.Error(w, invalidBoardPayloadMessage, http.StatusBadRequest)
	default:
		log.Printf("write board for %s: %s", logSafe(user), logSafe(err.Error()))
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
	}
}

// handlePutBoard replaces the whole board. A full-board PUT would otherwise
// silently delete tasks the CLI or MCP created since the client last read, so
// every PUT is conditional on a client-supplied If-Match carrying the token
// from its own GET, and a stale one is told 409 so it can refetch and merge.
// The header is required on both wire formats: a condition the handler
// synthesized from its own read would only cover the microseconds inside the
// request, not the client's read/edit/write interval, which is where the
// intervening task-level writes actually land.
func (s *server) handlePutBoard(w http.ResponseWriter, r *http.Request, user string) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get(contentTypeHeader))
	isJSON := mediaType == jsonMediaType
	want := strings.TrimSpace(strings.Join(r.Header.Values("If-Match"), ","))
	if want == "" {
		http.Error(w, missingIfMatchMessage, http.StatusPreconditionRequired)
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	operationID, err := boardOperationID(r)
	if err != nil {
		http.Error(w, invalidBoardPayloadMessage, http.StatusBadRequest)
		return
	}
	requestHash := boardPutRequestHash(mediaType, body)

	// The preliminary read only surfaces storage failures before any parsing;
	// the write predicate comes from If-Match, never from this snapshot.
	if _, _, err := s.currentBoard(user); err != nil {
		log.Printf("read board for %s: %s", logSafe(user), logSafe(err.Error()))
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
		return
	}
	if s.afterConditionalBoardSnapshot != nil {
		s.afterConditionalBoardSnapshot()
	}

	nextBoard := board.Parse(string(body))
	var canonicalIDs []*string
	if isJSON {
		var parseErr error
		nextBoard, canonicalIDs, parseErr = parseBoardJSONPut(body)
		if parseErr != nil {
			s.writeBoardJSONParseError(w, user, want)
			return
		}
	}
	taskIDs, revision, replayed, err := s.replaceBoard(
		user, nextBoard, canonicalIDs, boardWriteCondition(want), operationID, requestHash, isJSON,
	)
	if err != nil {
		writeBoardPutError(w, user, err)
		return
	}
	if replayed || acceptsJSON(r) {
		writeBoardAcknowledgement(w, taskIDs, revision, replayed)
		return
	}
	w.Header().Set("ETag", boardETag(revision))
	w.WriteHeader(http.StatusNoContent)
}

func writeBoardAcknowledgement(w http.ResponseWriter, taskIDs []string, revision int64, replayed bool) {
	w.Header().Set("ETag", boardETag(revision))
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, struct {
		TaskIDs []string `json:"task_ids"`
	}{TaskIDs: taskIDs})
}

func parseBoardJSONPut(body []byte) (board.Board, []*string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return board.Board{}, nil, errors.New("invalid JSON object")
	}
	var markdown string
	var ids []*string
	seen := make(map[string]bool, 2)
	for decoder.More() {
		field, err := nextBoardJSONField(decoder, seen)
		if err != nil {
			return board.Board{}, nil, err
		}
		switch field {
		case "board":
			markdown, err = decodeBoardJSONMarkdown(decoder)
		case "task_ids":
			ids, err = decodeBoardJSONTaskIDs(decoder)
		default:
			return board.Board{}, nil, errors.New("unknown board payload field")
		}
		if err != nil {
			return board.Board{}, nil, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return board.Board{}, nil, err
	}
	if !seen["board"] || !seen["task_ids"] {
		return board.Board{}, nil, errors.New("missing board payload field")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return board.Board{}, nil, err
	}
	return board.Parse(markdown), ids, nil
}

func nextBoardJSONField(decoder *json.Decoder, seen map[string]bool) (string, error) {
	fieldToken, err := decoder.Token()
	if err != nil {
		return "", err
	}
	field, ok := fieldToken.(string)
	if !ok || seen[field] {
		return "", errors.New("invalid or duplicate board payload field")
	}
	seen[field] = true
	return field, nil
}

func decodeBoardJSONMarkdown(decoder *json.Decoder) (string, error) {
	var decoded *string
	if err := decoder.Decode(&decoded); err != nil {
		return "", err
	}
	if decoded == nil {
		return "", errors.New("board must be a string")
	}
	return *decoded, nil
}

func decodeBoardJSONTaskIDs(decoder *json.Decoder) ([]*string, error) {
	var idsJSON json.RawMessage
	if err := decoder.Decode(&idsJSON); err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(idsJSON), []byte("null")) {
		return nil, errors.New("task_ids must be an array")
	}
	var ids []*string
	if err := json.Unmarshal(idsJSON, &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeBoardConflict(w http.ResponseWriter, etag string) {
	w.Header().Set("ETag", etag)
	http.Error(w, "board changed since it was read", http.StatusConflict)
}

func ifMatchContainsStar(ifMatch string) bool {
	for _, tag := range strings.Split(ifMatch, ",") {
		if strings.TrimSpace(tag) == "*" {
			return true
		}
	}
	return false
}

func boardWriteCondition(ifMatch string) store.BoardWriteCondition {
	condition := store.BoardWriteCondition{Present: ifMatch != ""}
	for _, tag := range strings.Split(ifMatch, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" {
			condition.Star = true
			condition.Revisions = nil
			return condition
		}
		if len(tag) < 4 || tag[0] != '"' || tag[1] != 'r' || tag[len(tag)-1] != '"' {
			continue
		}
		revision, err := strconv.ParseInt(tag[2:len(tag)-1], 10, 64)
		if err == nil && revision >= 0 {
			condition.Revisions = append(condition.Revisions, revision)
		}
	}
	return condition
}

// etagMatches reports whether any entry of an If-Match list equals etag. The
func etagMatches(ifMatch, etag string) bool {
	for _, tag := range strings.Split(ifMatch, ",") {
		if strings.TrimSpace(tag) == etag {
			return true
		}
	}
	return false
}

// configResponse carries the browser-side Entra IDs and the server's auth
// mode. The IDs are public by design — every MSAL SPA ships them in its
// bundle — and the mode only names which credential the API will demand,
// which an unauthenticated probe learns from a 401 anyway. The endpoint is
// unauthenticated (the SPA needs it before login), so nothing sensitive may
// ever be added here.
type configResponse struct {
	AzureClientID string `json:"azure_client_id"`
	AzureTenantID string `json:"azure_tenant_id"`
	// AuthMode is "open", "token", or "entra". The SPA skips the identity
	// gate entirely in open mode: with no credential to collect, the gate
	// was only choosing a board namespace, and open mode uses "default" —
	// the same namespace the CLI writes to without --user.
	AuthMode string `json:"auth_mode"`
}

// handleConfig serves the runtime Entra configuration and auth mode. The
// released binary is built without VITE_* values, so the SPA cannot learn
// them at build time; it reads them here instead. Unset env yields empty
// strings, which the SPA treats as "no Entra configured".
func (s *server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	mode := "open"
	switch {
	case s.cfg.TenantID != "":
		mode = "entra"
	case s.cfg.Token != "":
		mode = "token"
	}
	writeJSON(w, configResponse{
		AzureClientID: s.cfg.ClientID,
		AzureTenantID: s.cfg.TenantID,
		AuthMode:      mode,
	})
}

func (s *server) handleLabels(w http.ResponseWriter, _ *http.Request, user string) {
	labels, err := s.store.Labels(user)
	if err != nil {
		log.Printf("labels for %s: %v", user, err)
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
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
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
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
		http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
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
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
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
