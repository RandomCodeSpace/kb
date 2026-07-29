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
	"strconv"
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

type forgeRef struct {
	Source    store.ForgeSource
	Kind      string
	Project   string
	Issue     int
	Milestone int
}

// parseForgeRef accepts only configured forge URLs so later fetches can select
// the corresponding stored credential without ever following arbitrary hosts.
func parseForgeRef(sources []store.ForgeSource, sourceName, raw string) (forgeRef, error) {
	raw = strings.TrimSpace(raw)
	if isBareForgeProject(raw) {
		source, ok := forgeSourceByName(sources, sourceName)
		if !ok {
			if sourceName == "" {
				return forgeRef{}, errors.New("no configured source named")
			}
			return forgeRef{}, fmt.Errorf("no configured source named %s", sourceName)
		}
		if source.Kind != "github" {
			return forgeRef{}, errors.New("bare reference requires GitHub source")
		}
		return forgeRef{Source: source, Kind: source.Kind, Project: raw}, nil
	}

	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme == "" || u.Hostname() == "" || u.User != nil ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return forgeRef{}, errors.New("invalid forge reference")
	}

	source, path, ok := configuredForgeSource(sources, u)
	if !ok {
		return forgeRef{}, fmt.Errorf("no configured source for host %s", u.Hostname())
	}
	switch source.Kind {
	case "gitlab":
		return parseGitLabRef(source, path)
	case "github":
		return parseGitHubRef(source, path)
	default:
		return forgeRef{}, errors.New("invalid forge kind")
	}
}

func isBareForgeProject(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, ":?#") {
		return false
	}
	parts := strings.Split(raw, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func forgeSourceByName(sources []store.ForgeSource, name string) (store.ForgeSource, bool) {
	for _, source := range sources {
		if strings.EqualFold(source.Name, name) {
			return source, true
		}
	}
	return store.ForgeSource{}, false
}

// configuredForgeSource compares origins and whole path segments rather than
// raw strings, so a source at /forge cannot authorize /forgeish by accident.
func configuredForgeSource(sources []store.ForgeSource, request *url.URL) (store.ForgeSource, string, bool) {
	bestLength := -1
	var best store.ForgeSource
	bestPath := ""
	requestPath := strings.TrimRight(request.Path, "/")
	for _, source := range sources {
		base, err := normalizeForgeProbeBase(source.BaseURL)
		if err != nil || !sameForgeOrigin(base, request) {
			continue
		}
		basePath := strings.TrimRight(base.Path, "/")
		var path string
		switch {
		case basePath == "":
			path = strings.TrimPrefix(requestPath, "/")
		case requestPath == basePath:
			path = ""
		case strings.HasPrefix(requestPath, basePath+"/"):
			path = strings.TrimPrefix(requestPath, basePath+"/")
		default:
			continue
		}
		length := len(base.Scheme) + len(base.Host) + len(basePath)
		if length > bestLength {
			bestLength = length
			best = source
			bestPath = path
		}
	}
	return best, bestPath, bestLength >= 0
}

func sameForgeOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		forgeURLPort(a) == forgeURLPort(b)
}

func forgeURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func parseGitLabRef(source store.ForgeSource, path string) (forgeRef, error) {
	parts, err := forgePathParts(path)
	if err != nil {
		return forgeRef{}, err
	}
	n := len(parts)
	if n >= 3 && parts[n-3] == "-" {
		project := strings.Join(parts[:n-3], "/")
		if project == "" {
			return forgeRef{}, errors.New("invalid forge reference")
		}
		id, err := forgeRefID(parts[n-1])
		if err != nil {
			return forgeRef{}, err
		}
		ref := forgeRef{Source: source, Kind: source.Kind, Project: project}
		switch parts[n-2] {
		case "issues":
			ref.Issue = id
		case "milestones":
			ref.Milestone = id
		case "boards":
			// Phase 1 resolves a board to its project; list and label filters are out of scope.
		default:
			return forgeRef{}, errors.New("invalid forge reference")
		}
		return ref, nil
	}
	if n >= 3 && (parts[n-2] == "issues" || parts[n-2] == "milestones") {
		project := strings.Join(parts[:n-2], "/")
		if project == "" {
			return forgeRef{}, errors.New("invalid forge reference")
		}
		id, err := forgeRefID(parts[n-1])
		if err == nil {
			ref := forgeRef{Source: source, Kind: source.Kind, Project: project}
			if parts[n-2] == "issues" {
				ref.Issue = id
			} else {
				ref.Milestone = id
			}
			return ref, nil
		}
	}
	for _, part := range parts {
		if part == "-" {
			return forgeRef{}, errors.New("invalid forge reference")
		}
	}
	return forgeRef{Source: source, Kind: source.Kind, Project: strings.Join(parts, "/")}, nil
}

func parseGitHubRef(source store.ForgeSource, path string) (forgeRef, error) {
	parts, err := forgePathParts(path)
	if err != nil {
		return forgeRef{}, err
	}
	if len(parts) == 2 {
		return forgeRef{Source: source, Kind: source.Kind, Project: strings.Join(parts, "/")}, nil
	}
	if len(parts) != 4 || (parts[2] != "issues" && parts[2] != "milestone") {
		return forgeRef{}, errors.New("invalid forge reference")
	}
	id, err := forgeRefID(parts[3])
	if err != nil {
		return forgeRef{}, err
	}
	ref := forgeRef{Source: source, Kind: source.Kind, Project: strings.Join(parts[:2], "/")}
	if parts[2] == "issues" {
		ref.Issue = id
	} else {
		ref.Milestone = id
	}
	return ref, nil
}

func forgePathParts(path string) ([]string, error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil, errors.New("invalid forge reference")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" {
			return nil, errors.New("invalid forge reference")
		}
	}
	return parts, nil
}

func forgeRefID(raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid forge reference")
	}
	return id, nil
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
