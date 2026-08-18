// Package forge implements configured, guarded issue imports and upstream
// drift checks without requiring the optional HTTP server.
package forge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	forgeTimeout                  = 20 * time.Second
	maxForgeDrainBytes            = 64 << 10
	maxForgeBodyBytes             = 2 << 20
	maxImportIssues               = 20
	maxForgeComments              = 20
	maxForgeCommentLen            = 1 << 10
	importFetchTimeout            = 25 * time.Second
	maxImportLinks                = 100
	invalidIntegrationNameMessage = "invalid integration name"
	linkTagPrefix                 = "link::"
	importTagPrefix               = "import::"
	maxImportPackBytes            = 48 << 10
	maxImportIssueBodyBytes       = 16 << 10
	maxImportCommentBytes         = 1 << 10
	maxForgePages                 = 20
	aiImportMaxTokens             = 8192
	aiDriftMaxTokens              = 1024
	importPackTruncationNote      = "assistant input limit reached — some fetched issues produced no draft"
)

// Error is a caller-safe failure category shared by local UIs and HTTP
// adapters. Cause is retained for errors.Is/As and logs, never display.
type Error struct {
	Code    int
	Message string
	Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// Service owns the single guarded forge path used by the TUI and server.
type Service struct {
	store            *store.Store
	runner           *ai.Runner
	forgeClient      *http.Client
	importDriftLocks boardLocks
}

// New constructs the direct-store forge service. Nil collaborators select
// the guarded production clients.
func New(st *store.Store, runner *ai.Runner, client *http.Client) *Service {
	if runner == nil {
		runner = ai.NewRunner(st, "", nil, nil)
	}
	if client == nil {
		client = NewHTTPClient()
	}
	return &Service{store: st, runner: runner, forgeClient: client}
}

type boardLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (l *boardLocks) get(user string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = make(map[string]*sync.Mutex)
	}
	if l.m[user] == nil {
		l.m[user] = new(sync.Mutex)
	}
	return l.m[user]
}

type forgeTestProbe struct {
	BaseURL *string `json:"base_url"`
	PAT     *string `json:"pat"`
}

type forgeTestTarget struct {
	baseURL string
	pat     string
}

type forgeRef struct {
	Source    store.ForgeSource
	Kind      string
	Project   string
	Issue     int
	Milestone int

	// pat is populated only after the request owner decrypts its selected source.
	// Keeping it private prevents the parser and response paths from exposing it.
	pat string
}

type forgeIssue struct {
	Ref      string
	Title    string
	Body     string
	URL      string
	Labels   []string
	Comments []string
}

type forgeHTTPResponse struct {
	status int
	header http.Header
	body   []byte
}

type gitLabIssue struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	WebURL      string   `json:"web_url"`
	Labels      []string `json:"labels"`
}

type gitHubIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest json.RawMessage `json:"pull_request"`
}

type gitLabNote struct {
	Body   string `json:"body"`
	System bool   `json:"system"`
}

type gitHubComment struct {
	Body string `json:"body"`
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

	source, path, ok := configuredForgeSource(sources, sourceName, u)
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
// Equal-length configured bases are disambiguated by the caller's source name;
// a longer base always wins regardless of that selection.
func configuredForgeSource(sources []store.ForgeSource, sourceName string, request *url.URL) (store.ForgeSource, string, bool) {
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
		if length > bestLength || (length == bestLength && strings.EqualFold(source.Name, sourceName)) {
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
		return parseGitLabScopedRef(source, parts[:n-3], parts[n-2], parts[n-1])
	}
	if n >= 3 && (parts[n-2] == "issues" || parts[n-2] == "milestones") {
		ref, err := parseGitLabScopedRef(source, parts[:n-2], parts[n-2], parts[n-1])
		if err == nil {
			return ref, nil
		}
	}
	if slices.Contains(parts, "-") {
		return forgeRef{}, errors.New("invalid forge reference")
	}
	return forgeRef{Source: source, Kind: source.Kind, Project: strings.Join(parts, "/")}, nil
}

func parseGitLabScopedRef(source store.ForgeSource, projectParts []string, resource, rawID string) (forgeRef, error) {
	project := strings.Join(projectParts, "/")
	if project == "" {
		return forgeRef{}, errors.New("invalid forge reference")
	}
	id, err := forgeRefID(rawID)
	if err != nil {
		return forgeRef{}, err
	}
	ref := forgeRef{Source: source, Kind: source.Kind, Project: project}
	switch resource {
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
	if slices.Contains(parts, "") {
		return nil, errors.New("invalid forge reference")
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

// fetchIssue loads one forge issue and its bounded human discussion for the
// import pipeline. The caller already chose the configured source in D1.
func (s *Service) fetchIssue(ctx context.Context, ref forgeRef) (forgeIssue, error) {
	issue, apiBase, issuePath, err := s.fetchIssueSnapshot(ctx, ref)
	if err != nil {
		return forgeIssue{}, err
	}
	comments, err := s.fetchForgeComments(ctx, ref, apiBase, issuePath)
	if err != nil {
		return forgeIssue{}, err
	}
	issue.Comments = comments
	return issue, nil
}

// fetchIssueSnapshot reads only the issue record. Acceptance needs no
// discussion, so keeping this distinct prevents an unnecessary second egress.
func (s *Service) fetchIssueSnapshot(ctx context.Context, ref forgeRef) (forgeIssue, string, string, error) {
	if ref.Issue <= 0 {
		return forgeIssue{}, "", "", forgeRequestError(ref, "")
	}
	apiBase, err := forgeAPIBase(ref.Kind, ref.Source.BaseURL)
	if err != nil {
		return forgeIssue{}, "", "", forgeRequestError(ref, "")
	}
	issuePath, err := forgeIssuePath(ref)
	if err != nil {
		return forgeIssue{}, "", "", forgeRequestError(ref, "")
	}
	response, err := s.forgeGet(ctx, ref, apiBase, issuePath, nil)
	if err != nil {
		return forgeIssue{}, "", "", err
	}
	if response.status < http.StatusOK || response.status >= http.StatusMultipleChoices {
		return forgeIssue{}, "", "", forgeRequestError(ref, issuePath)
	}

	var issue forgeIssue
	switch ref.Kind {
	case "gitlab":
		var raw gitLabIssue
		if err := json.Unmarshal(response.body, &raw); err != nil {
			return forgeIssue{}, "", "", forgeRequestError(ref, issuePath)
		}
		issue = forgeIssue{Ref: fmt.Sprintf("gitlab#%d", raw.IID), Title: raw.Title, Body: raw.Description, URL: raw.WebURL, Labels: raw.Labels}
	case "github":
		var raw gitHubIssue
		if err := json.Unmarshal(response.body, &raw); err != nil || len(raw.PullRequest) != 0 {
			return forgeIssue{}, "", "", forgeRequestError(ref, issuePath)
		}
		issue = gitHubForgeIssue(raw)
	default:
		return forgeIssue{}, "", "", forgeRequestError(ref, issuePath)
	}
	return issue, apiBase, issuePath, nil
}

// fetchIssues loads open project, board, or milestone issues. Board filtering
// deliberately remains out of scope: D1 resolves boards to their project.
func (s *Service) fetchIssues(ctx context.Context, ref forgeRef, max int) (issues []forgeIssue, totalHint int, truncated bool, note string, err error) {
	if ref.Issue > 0 {
		issue, err := s.fetchIssue(ctx, ref)
		if err != nil {
			return nil, 0, false, "", err
		}
		return []forgeIssue{issue}, 1, false, "", nil
	}
	if max <= 0 || max > maxImportIssues {
		max = maxImportIssues
	}
	apiBase, err := forgeAPIBase(ref.Kind, ref.Source.BaseURL)
	if err != nil {
		return nil, 0, false, "", forgeRequestError(ref, "")
	}
	listPath, query, err := s.forgeIssuesList(ctx, ref, apiBase)
	if err != nil {
		return nil, 0, false, "", err
	}
	return s.fetchIssuePages(ctx, ref, apiBase, listPath, query, max)
}

type forgeIssuePageState struct {
	issues         []forgeIssue
	totalHint      int
	fallbackTotal  int
	hasGitLabTotal bool
	truncated      bool
	note           string
}

func (s *Service) fetchIssuePages(ctx context.Context, ref forgeRef, apiBase, listPath string, query url.Values, max int) ([]forgeIssue, int, bool, string, error) {
	state := forgeIssuePageState{}
	page := 1
	for {
		if page > 1 {
			query.Set("page", strconv.Itoa(page))
		}
		response, err := s.forgeGet(ctx, ref, apiBase, listPath, query)
		if err != nil {
			return nil, 0, false, "", err
		}
		state.observeTotal(ref.Kind, response.header)
		if forgeRateLimited(ref.Kind, response) {
			state.markRateLimited()
			return state.result()
		}
		if response.status < http.StatusOK || response.status >= http.StatusMultipleChoices {
			return nil, 0, false, "", forgeRequestError(ref, listPath)
		}

		batch, err := parseForgeIssueList(ref.Kind, response.body)
		if err != nil {
			return nil, 0, false, "", forgeRequestError(ref, listPath)
		}
		if state.appendBatch(batch, max, forgeHasNextPage(ref.Kind, response.header)) {
			break
		}
		if page >= maxForgePages {
			state.truncated = true
			state.note = appendImportNote(state.note, "upstream pagination limit reached — partial results")
			break
		}
		page++
	}
	return state.result()
}

func (state *forgeIssuePageState) observeTotal(kind string, header http.Header) {
	if kind != "gitlab" {
		return
	}
	total := forgeTotalHint(header)
	if total >= 0 {
		state.totalHint = total
		state.hasGitLabTotal = true
	}
}

func (state *forgeIssuePageState) markRateLimited() {
	if !state.hasGitLabTotal {
		state.totalHint = state.fallbackTotal
	}
	state.truncated = true
	state.note = fmt.Sprintf("rate limited — partial results (%d of %d)", len(state.issues), state.totalHint)
}

func (state *forgeIssuePageState) appendBatch(batch []forgeIssue, max int, hasNext bool) bool {
	state.fallbackTotal += len(batch)
	remaining := max - len(state.issues)
	if len(batch) > remaining {
		state.issues = append(state.issues, batch[:remaining]...)
		state.truncated = true
		return true
	}
	state.issues = append(state.issues, batch...)
	if len(state.issues) >= max {
		state.truncated = hasNext
		return true
	}
	return !hasNext
}

func (state *forgeIssuePageState) result() ([]forgeIssue, int, bool, string, error) {
	if !state.hasGitLabTotal {
		state.totalHint = state.fallbackTotal
	}
	if state.totalHint > len(state.issues) {
		state.truncated = true
	}
	return state.issues, state.totalHint, state.truncated, state.note, nil
}

func (s *Service) forgeIssuesList(ctx context.Context, ref forgeRef, apiBase string) (string, url.Values, error) {
	listPath, query, err := forgeIssueListRequest(ref)
	if err != nil {
		return "", nil, forgeRequestError(ref, listPath)
	}
	if ref.Milestone == 0 {
		return listPath, query, nil
	}
	milestonePath, err := forgeMilestonePath(ref)
	if err != nil {
		return "", nil, forgeRequestError(ref, "")
	}
	response, err := s.forgeGet(ctx, ref, apiBase, milestonePath, nil)
	if err != nil {
		return "", nil, err
	}
	if response.status < http.StatusOK || response.status >= http.StatusMultipleChoices {
		return "", nil, forgeRequestError(ref, milestonePath)
	}
	if !setForgeMilestoneQuery(ref.Kind, query, response.body) {
		return "", nil, forgeRequestError(ref, milestonePath)
	}
	return listPath, query, nil
}

func forgeIssueListRequest(ref forgeRef) (string, url.Values, error) {
	listPath, err := forgeProjectIssuesPath(ref)
	if err != nil {
		return "", nil, err
	}
	query := url.Values{"per_page": {"50"}}
	switch ref.Kind {
	case "gitlab":
		query.Set("state", "opened")
	case "github":
		query.Set("state", "open")
	default:
		return listPath, nil, errors.New("invalid forge kind")
	}
	return listPath, query, nil
}

func setForgeMilestoneQuery(kind string, query url.Values, body []byte) bool {
	switch kind {
	case "gitlab":
		var milestone struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(body, &milestone); err != nil || milestone.Title == "" {
			return false
		}
		query.Set("milestone", milestone.Title)
	case "github":
		var milestone struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal(body, &milestone); err != nil || milestone.Number <= 0 {
			return false
		}
		query.Set("milestone", strconv.Itoa(milestone.Number))
	default:
		return false
	}
	return true
}

func (s *Service) fetchForgeComments(ctx context.Context, ref forgeRef, apiBase, issuePath string) ([]string, error) {
	commentsPath := issuePath + "/comments"
	if ref.Kind == "gitlab" {
		commentsPath = issuePath + "/notes"
	}
	response, err := s.forgeGet(ctx, ref, apiBase, commentsPath, url.Values{"per_page": {"50"}})
	if err != nil {
		return nil, err
	}
	if response.status < http.StatusOK || response.status >= http.StatusMultipleChoices {
		return nil, forgeRequestError(ref, commentsPath)
	}

	comments, err := parseForgeComments(ref.Kind, response.body)
	if err != nil {
		return nil, forgeRequestError(ref, commentsPath)
	}
	return comments, nil
}

func parseForgeComments(kind string, body []byte) ([]string, error) {
	comments := make([]string, 0, maxForgeComments)
	switch kind {
	case "gitlab":
		var notes []gitLabNote
		if err := json.Unmarshal(body, &notes); err != nil {
			return nil, err
		}
		for _, note := range notes {
			if !note.System {
				comments = appendBoundedForgeComment(comments, note.Body)
			}
		}
	case "github":
		var raw []gitHubComment
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		for _, comment := range raw {
			comments = appendBoundedForgeComment(comments, comment.Body)
		}
	default:
		return nil, errors.New("invalid forge kind")
	}
	return comments, nil
}

func appendBoundedForgeComment(comments []string, body string) []string {
	if len(comments) >= maxForgeComments {
		return comments
	}
	for len(body) > maxForgeCommentLen {
		_, size := utf8.DecodeLastRuneInString(body)
		body = body[:len(body)-size]
	}
	return append(comments, body)
}

func forgeIssuePath(ref forgeRef) (string, error) {
	projectPath, err := forgeProjectPath(ref)
	if err != nil {
		return "", err
	}
	return projectPath + "/issues/" + strconv.Itoa(ref.Issue), nil
}

func forgeMilestonePath(ref forgeRef) (string, error) {
	projectPath, err := forgeProjectPath(ref)
	if err != nil {
		return "", err
	}
	return projectPath + "/milestones/" + strconv.Itoa(ref.Milestone), nil
}

func forgeProjectIssuesPath(ref forgeRef) (string, error) {
	projectPath, err := forgeProjectPath(ref)
	if err != nil {
		return "", err
	}
	return projectPath + "/issues", nil
}

func forgeProjectPath(ref forgeRef) (string, error) {
	if ref.Project == "" {
		return "", errors.New("invalid forge project")
	}
	switch ref.Kind {
	case "gitlab":
		return "/projects/" + url.PathEscape(ref.Project), nil
	case "github":
		parts := strings.Split(ref.Project, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", errors.New("invalid forge project")
		}
		return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
	default:
		return "", errors.New("invalid forge kind")
	}
}

func (s *Service) forgeGet(ctx context.Context, ref forgeRef, apiBase, path string, query url.Values) (forgeHTTPResponse, error) {
	endpoint := strings.TrimRight(apiBase, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return forgeHTTPResponse{}, forgeRequestError(ref, path)
	}
	switch ref.Kind {
	case "gitlab":
		if ref.pat != "" {
			request.Header.Set("PRIVATE-TOKEN", ref.pat)
		}
	case "github":
		if ref.pat != "" {
			request.Header.Set("Authorization", "Bearer "+ref.pat)
		}
		request.Header.Set("Accept", "application/vnd.github+json")
	default:
		return forgeHTTPResponse{}, forgeRequestError(ref, path)
	}
	if s.forgeClient == nil {
		return forgeHTTPResponse{}, forgeRequestError(ref, path)
	}
	response, err := s.forgeClient.Do(request)
	if err != nil {
		return forgeHTTPResponse{}, forgeRequestError(ref, path)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxForgeBodyBytes))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return forgeHTTPResponse{}, forgeRequestError(ref, path)
	}
	return forgeHTTPResponse{status: response.StatusCode, header: response.Header.Clone(), body: body}, nil
}

// The name and path are already constrained (names match ^[a-z0-9._-]{1,64}$ and
// paths are url.PathEscape'd), but both are stripped anyway so no future caller
// can turn this line into a log-forging primitive.
func forgeRequestError(ref forgeRef, path string) error {
	log.Printf("forge: request failed source=%s path=%s", logSafe(ref.Source.Name), logSafe(path))
	return &Error{Code: http.StatusBadGateway, Message: "forge request failed"}
}

func forgeTotalHint(header http.Header) int {
	total, err := strconv.Atoi(header.Get("X-Total"))
	if err != nil || total < 0 {
		return -1
	}
	return total
}

func forgeRateLimited(kind string, response forgeHTTPResponse) bool {
	return response.status == http.StatusTooManyRequests ||
		(kind == "github" && response.status == http.StatusForbidden && response.header.Get("X-RateLimit-Remaining") == "0")
}

func forgeHasNextPage(kind string, header http.Header) bool {
	if kind == "gitlab" {
		return header.Get("X-Next-Page") != ""
	}
	return strings.Contains(header.Get("Link"), "rel=\"next\"")
}

func parseForgeIssueList(kind string, body []byte) ([]forgeIssue, error) {
	switch kind {
	case "gitlab":
		var raw []gitLabIssue
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		issues := make([]forgeIssue, 0, len(raw))
		for _, issue := range raw {
			issues = append(issues, forgeIssue{Ref: fmt.Sprintf("gitlab#%d", issue.IID), Title: issue.Title, Body: issue.Description, URL: issue.WebURL, Labels: issue.Labels})
		}
		return issues, nil
	case "github":
		var raw []gitHubIssue
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		issues := make([]forgeIssue, 0, len(raw))
		for _, issue := range raw {
			if len(issue.PullRequest) == 0 {
				issues = append(issues, gitHubForgeIssue(issue))
			}
		}
		return issues, nil
	default:
		return nil, errors.New("invalid forge kind")
	}
}

func gitHubForgeIssue(issue gitHubIssue) forgeIssue {
	labels := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		if label.Name != "" {
			labels = append(labels, label.Name)
		}
	}
	return forgeIssue{Ref: fmt.Sprintf("github#%d", issue.Number), Title: issue.Title, Body: issue.Body, URL: issue.HTMLURL, Labels: labels}
}

func newForgeClient() *http.Client { return NewHTTPClient() }

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
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("forge base URL must not contain query or fragment")
	}
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

// importTransformSkillName is the skill the import preview runs. The endpoint
// keeps its own request and response shape; only the way the drafts are
// produced is shared with the other skill callers.
const importTransformSkillName = "import-transform"

// importPartialTransformNote tells the caller the draft list is short because
// the run ran out of room, not because the issues were judged noise.
const importPartialTransformNote = "the assistant stopped early — some issues produced no draft"

// appendImportNote joins a second note onto whatever fetchIssues already said,
// so a rate-limited fetch and a truncated transform can both be reported.
func appendImportNote(note, extra string) string {
	if note == "" {
		return extra
	}
	return note + "; " + extra
}

// handleImportPreview transforms a bounded, configured forge selection once;
// it never writes cards or provenance, which remain an explicit later commit.
func importDriftRevision(baseline store.ImportBaseline) string {
	sum := sha256.Sum256([]byte(baseline.Title + "\x00" + baseline.Hash))
	return fmt.Sprintf("%x", sum)
}

func validImportDriftRevision(revision string) bool {
	if len(revision) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(revision); i++ {
		if (revision[i] < '0' || revision[i] > '9') && (revision[i] < 'a' || revision[i] > 'f') {
			return false
		}
	}
	return true
}

// importDriftSummaryPrompt is the whole instruction the drift summary gets.
// The run carries no tools: it compares two pieces of text the caller already
// holds, so a tool would only be another way for third-party issue text to
// reach the board.
const importDriftSummaryPrompt = "Summarize an imported issue change using only the supplied titles and excerpts."

// importDriftSummary is best-effort prose about what changed upstream. Every
// failure — no configuration, a bad endpoint, an upstream that never answers —
// degrades to no summary, because a drift comparison the caller asked for is
// valid without one. One run is one round trip: the loop is capped at a single
// iteration, and a toolless request cannot ask for a second.
func stripModelLinkTags(tags []string) []string {
	filtered := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !strings.HasPrefix(tag, linkTagPrefix) && !strings.HasPrefix(tag, importTagPrefix) {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

func importIssueProvenance(ref forgeRef, issue forgeIssue) (link, externalKey string) {
	link = issue.Ref
	issueID := strings.TrimPrefix(link, ref.Kind+"#")
	identity := strings.ToLower(strings.TrimSpace(ref.Source.Name))
	if base, err := url.Parse(ref.Source.BaseURL); err == nil {
		origin := strings.ToLower(base.Scheme) + "://" + strings.ToLower(base.Host)
		identity += "@" + origin + strings.TrimRight(base.EscapedPath(), "/")
	}
	externalKey = fmt.Sprintf("%s:%s/%s#%s", ref.Kind, identity, ref.Project, issueID)
	return link, externalKey
}

func resolveForgeTestTarget(storedBase, storedPAT string, probe forgeTestProbe) (forgeTestTarget, error) {
	target := forgeTestTarget{baseURL: storedBase, pat: storedPAT}
	suppliedBase := trimmedForgeProbeValue(probe.BaseURL)
	if suppliedBase != "" {
		normalized, err := normalizeForgeProbeBase(suppliedBase)
		if err != nil {
			return forgeTestTarget{}, err
		}
		target.baseURL = normalized.String()
	}
	suppliedPAT := trimmedForgeProbeValue(probe.PAT)
	if suppliedPAT != "" {
		target.pat = suppliedPAT
	}
	if suppliedPAT == "" && suppliedBase != "" && storedPAT != "" &&
		!store.SameAIOrigin(storedBase, target.baseURL) {
		return forgeTestTarget{}, errors.New("enter the token to test a different endpoint")
	}
	return target, nil
}

func trimmedForgeProbeValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func newForgeTestRequest(ctx context.Context, kind string, target forgeTestTarget, project string) (*http.Request, error) {
	apiBase, err := forgeAPIBase(kind, target.baseURL)
	if err != nil {
		return nil, err
	}
	project = strings.TrimSpace(project)
	endpoint := apiBase
	if project != "" {
		projectPath, err := forgeProjectPath(forgeRef{Kind: kind, Project: project})
		if err != nil {
			return nil, err
		}
		endpoint += projectPath
	} else {
		switch kind {
		case "gitlab":
			endpoint += "/version"
		case "github":
			endpoint += "/user"
		default:
			return nil, errors.New("invalid forge kind")
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid forge base URL")
	}
	setForgeTestHeaders(request, kind, target.pat)
	return request, nil
}

func setForgeTestHeaders(request *http.Request, kind, pat string) {
	if kind == "gitlab" && pat != "" {
		request.Header.Set("PRIVATE-TOKEN", pat)
	}
	if kind == "github" {
		if pat != "" {
			request.Header.Set("Authorization", "Bearer "+pat)
		}
		request.Header.Set("Accept", "application/vnd.github+json")
	}
}

func executeForgeTest(client *http.Client, request *http.Request) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	if err := drainForgeResponse(response); err != nil {
		return fmt.Errorf("close response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("upstream status %d", response.StatusCode)
	}
	return nil
}

func drainForgeResponse(response *http.Response) error {
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxForgeDrainBytes))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("read: %v; close: %v", readErr, closeErr)
	}
	return nil
}
