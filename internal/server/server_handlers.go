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
	// The 404 carries the token too: it is the version of "no board", and a
	// client that never got one would send no If-Match at all, making its
	// first PUT unconditional — the very write most likely to land on a board
	// the CLI or MCP created in the meantime.
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

// handlePutBoard replaces the whole board. A full-board PUT would otherwise
// silently delete tasks the CLI or MCP created since the client last read,
// so a client that sends If-Match with the token from its GET is told 409
// instead, and can refetch and merge. Clients that send no If-Match keep the
// old last-writer-wins behavior.
func shouldInvokeBoardSnapshotHook(want string, isJSON bool) bool {
	return want != "" || isJSON
}

func (s *server) handlePutBoard(w http.ResponseWriter, r *http.Request, user string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	operationValues := r.Header.Values("Idempotency-Key")
	if len(operationValues) > 1 {
		http.Error(w, invalidBoardPayloadMessage, http.StatusBadRequest)
		return
	}
	operationID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if operationID != "" {
		parsed, err := uuid.Parse(operationID)
		if err != nil || parsed.String() != strings.ToLower(operationID) {
			http.Error(w, invalidBoardPayloadMessage, http.StatusBadRequest)
			return
		}
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get(contentTypeHeader))
	isJSON := mediaType == jsonMediaType
	hashInput := append([]byte(mediaType+"\x00"), body...)
	requestHash := fmt.Sprintf("%x", sha256.Sum256(hashInput))

	snapshot, etag, err := s.currentBoard(user)
	if err != nil {
		log.Printf("read board for %s: %s", logSafe(user), logSafe(err.Error()))
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
		return
	}
	want := strings.TrimSpace(strings.Join(r.Header.Values("If-Match"), ","))
	if s.afterConditionalBoardSnapshot != nil && shouldInvokeBoardSnapshotHook(want, isJSON) {
		s.afterConditionalBoardSnapshot()
	}

	nextBoard := board.Parse(string(body))
	var canonicalIDs []*string
	if isJSON {
		var parseErr error
		nextBoard, canonicalIDs, parseErr = parseBoardJSONPut(body)
		if parseErr != nil {
			condition := boardWriteCondition(want)
			if condition.Present {
				_, conditionErr := s.store.CheckBoardWriteCondition(user, condition)
				var conflict *store.RevisionConflictError
				switch {
				case errors.As(conditionErr, &conflict):
					writeBoardConflict(w, boardETag(conflict.CurrentRevision))
					return
				case conditionErr != nil:
					log.Printf("check board condition for %s: %s", logSafe(user), logSafe(conditionErr.Error()))
					http.Error(w, storageErrorMessage, http.StatusInternalServerError)
					return
				}
			}
			http.Error(w, invalidBoardPayloadMessage, http.StatusBadRequest)
			return
		}
	}
	hasCreates := false
	for _, id := range canonicalIDs {
		if id == nil {
			hasCreates = true
			break
		}
	}
	condition := boardWriteCondition(want)
	if want == "" && isJSON {
		condition = store.BoardWriteCondition{Present: true, Revisions: []int64{snapshot.Revision}}
	}

	var taskIDs []string
	var revision int64
	var replayed bool
	switch {
	case condition.Present || operationID != "":
		taskIDs, revision, replayed, err = s.store.ReplaceBoardConditionalWithReceipt(
			user, nextBoard, canonicalIDs, condition, operationID, requestHash, isJSON && hasCreates,
		)
	default:
		taskIDs, revision, err = s.store.ReplaceBoardWithTaskIDsAndRevision(user, nextBoard)
		if err == nil && s.afterUnconditionalBoardReplace != nil {
			s.afterUnconditionalBoardReplace()
		}
	}
	if err != nil {
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
		return
	}
	if replayed || acceptsJSON(r) {
		writeBoardAcknowledgement(w, taskIDs, revision, replayed)
		return
	}
	etag = boardETag(revision)
	w.Header().Set("ETag", etag)
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
		fieldToken, err := decoder.Token()
		if err != nil {
			return board.Board{}, nil, err
		}
		field, ok := fieldToken.(string)
		if !ok || seen[field] {
			return board.Board{}, nil, errors.New("invalid or duplicate board payload field")
		}
		seen[field] = true
		switch field {
		case "board":
			var decoded *string
			if err := decoder.Decode(&decoded); err != nil {
				return board.Board{}, nil, err
			}
			if decoded == nil {
				return board.Board{}, nil, errors.New("board must be a string")
			}
			markdown = *decoded
		case "task_ids":
			var idsJSON json.RawMessage
			if err := decoder.Decode(&idsJSON); err != nil {
				return board.Board{}, nil, err
			}
			if bytes.Equal(bytes.TrimSpace(idsJSON), []byte("null")) {
				return board.Board{}, nil, errors.New("task_ids must be an array")
			}
			if err := json.Unmarshal(idsJSON, &ids); err != nil {
				return board.Board{}, nil, err
			}
		default:
			return board.Board{}, nil, errors.New("unknown board payload field")
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
