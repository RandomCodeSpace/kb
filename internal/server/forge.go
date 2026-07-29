package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	forgeTimeout       = 20 * time.Second
	maxForgeDrainBytes = 64 << 10
)

type forgeSourceResponse struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	HasToken bool   `json:"has_token"`
}

type forgeSourcesResponse struct {
	Sources []forgeSourceResponse `json:"sources"`
}

type forgeTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func newForgeClient() *http.Client {
	raw := os.Getenv("KB_FORGE_ALLOW_PRIVATE")
	allowAll := raw == "1" || raw == "*"
	var allowHosts map[string]bool
	if !allowAll {
		allowHosts = parseAllowedHosts(raw)
	}

	return &http.Client{
		Timeout:       forgeTimeout,
		Transport:     guardedTransport(allowHosts, allowAll),
		CheckRedirect: sameHostRedirect,
	}
}

func validForgeSourceName(name string) bool {
	name = strings.ToLower(name)
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') &&
			c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func normalizeForgeProbeBase(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("invalid forge base URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("invalid forge base URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("forge base URL scheme must be http or https")
	}
	if u.User != nil {
		return nil, errors.New("forge base URL must not contain userinfo")
	}
	// A configured endpoint identifies an origin and optional path prefix, not
	// a caller-controlled query. Removing both also keeps every probe URL free
	// of credential-shaped or endpoint-specific query data.
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u, nil
}

// forgeAPIBase derives the stable REST prefix for each supported forge while
// preserving enterprise installations mounted below a path prefix.
func forgeAPIBase(kind, baseURL string) (string, error) {
	if kind != "gitlab" && kind != "github" {
		return "", errors.New("invalid forge kind")
	}
	u, err := normalizeForgeProbeBase(baseURL)
	if err != nil {
		return "", err
	}
	if kind == "github" && strings.EqualFold(u.Hostname(), "github.com") {
		return "https://api.github.com", nil
	}

	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	if kind == "gitlab" {
		u.Path += "/api/v4"
	} else {
		u.Path += "/api/v3"
	}
	return u.String(), nil
}

func (s *server) handleGetIntegrations(w http.ResponseWriter, _ *http.Request, user string) {
	sources, err := s.store.ForgeSources(user)
	if err != nil {
		log.Printf("forge: list integrations for %s failed: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	response := forgeSourcesResponse{Sources: make([]forgeSourceResponse, 0, len(sources))}
	for _, source := range sources {
		response.Sources = append(response.Sources, forgeSourceResponse{
			Name:     source.Name,
			Kind:     source.Kind,
			BaseURL:  source.BaseURL,
			HasToken: source.HasToken,
		})
	}
	writeJSON(w, response)
}

func (s *server) handlePutIntegration(w http.ResponseWriter, r *http.Request, user string) {
	name := r.PathValue("name")
	if !validForgeSourceName(name) {
		http.Error(w, "invalid integration name", http.StatusBadRequest)
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind    string  `json:"kind"`
		BaseURL *string `json:"base_url"`
		PAT     *string `json:"pat"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Kind != "gitlab" && req.Kind != "github" {
		http.Error(w, "invalid forge kind", http.StatusBadRequest)
		return
	}
	if req.BaseURL != nil {
		if _, err := forgeAPIBase(req.Kind, *req.BaseURL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	tokenCleared, err := s.store.SetForgeSource(user, name, req.Kind, req.BaseURL, req.PAT)
	if err != nil {
		if err.Error() == "store: forge base URL is required" {
			http.Error(w, "forge base URL is required", http.StatusBadRequest)
			return
		}
		log.Printf("forge: save integration for %s failed: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	if tokenCleared {
		writeJSON(w, map[string]bool{"token_cleared": true})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteIntegration(w http.ResponseWriter, r *http.Request, user string) {
	name := r.PathValue("name")
	if !validForgeSourceName(name) {
		http.Error(w, "invalid integration name", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteForgeSource(user, name); err != nil {
		log.Printf("forge: delete integration for %s failed: %v", user, err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleTestIntegration(w http.ResponseWriter, r *http.Request, user string) {
	name := r.PathValue("name")
	if !validForgeSourceName(name) {
		http.Error(w, "invalid integration name", http.StatusBadRequest)
		return
	}

	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var probe struct {
		BaseURL *string `json:"base_url"`
		PAT     *string `json:"pat"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &probe); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}

	kind, storedBase, storedPAT, err := s.store.ForgePAT(user, name)
	if err != nil {
		log.Printf("forge: load integration for %s failed: %v", user, err)
		writeJSON(w, forgeTestResponse{Error: "integration unavailable"})
		return
	}

	baseURL := storedBase
	suppliedBase := ""
	if probe.BaseURL != nil {
		suppliedBase = strings.TrimSpace(*probe.BaseURL)
	}
	if suppliedBase != "" {
		normalized, err := normalizeForgeProbeBase(suppliedBase)
		if err != nil {
			writeJSON(w, forgeTestResponse{Error: err.Error()})
			return
		}
		baseURL = normalized.String()
	}

	pat := storedPAT
	suppliedPAT := ""
	if probe.PAT != nil {
		suppliedPAT = strings.TrimSpace(*probe.PAT)
	}
	if suppliedPAT != "" {
		pat = suppliedPAT
	}
	if suppliedPAT == "" && suppliedBase != "" && storedPAT != "" &&
		!store.SameAIOrigin(storedBase, baseURL) {
		writeJSON(w, forgeTestResponse{Error: "enter the token to test a different endpoint"})
		return
	}

	apiBase, err := forgeAPIBase(kind, baseURL)
	if err != nil {
		writeJSON(w, forgeTestResponse{Error: err.Error()})
		return
	}
	endpoint := apiBase
	switch kind {
	case "gitlab":
		endpoint += "/version"
	case "github":
		endpoint += "/user"
	default:
		writeJSON(w, forgeTestResponse{Error: "invalid forge kind"})
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		writeJSON(w, forgeTestResponse{Error: "invalid forge base URL"})
		return
	}
	switch kind {
	case "gitlab":
		if pat != "" {
			request.Header.Set("PRIVATE-TOKEN", pat)
		}
	case "github":
		if pat != "" {
			request.Header.Set("Authorization", "Bearer "+pat)
		}
		request.Header.Set("Accept", "application/vnd.github+json")
	}

	response, err := s.forgeClient.Do(request)
	if err != nil {
		log.Printf("forge: connection test for %s failed: %v", user, err)
		writeJSON(w, forgeTestResponse{Error: "connection failed"})
		return
	}
	drainErr := drainForgeResponse(response)
	if drainErr != nil {
		log.Printf("forge: connection test for %s failed while closing response: %v", user, drainErr)
		writeJSON(w, forgeTestResponse{Error: "connection failed"})
		return
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		log.Printf("forge: connection test for %s failed: upstream status %d", user, response.StatusCode)
		writeJSON(w, forgeTestResponse{Error: "connection failed"})
		return
	}
	writeJSON(w, forgeTestResponse{OK: true})
}

func drainForgeResponse(response *http.Response) error {
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxForgeDrainBytes))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read: %v; close: %v", readErr, closeErr)
	}
	return nil
}
