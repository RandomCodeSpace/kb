package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const importFetchTimeout = forge.ImportFetchTimeout
const maxImportIssues = forge.MaxImportIssues
const maxForgeCommentLen = forge.MaxForgeCommentLen
const maxForgeComments = forge.MaxForgeComments
const importPartialTransformNote = "the assistant stopped early — some issues produced no draft"
const invalidIntegrationNameMessage = "invalid integration name"

type forgeSourceResponse struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	HasToken bool   `json:"has_token"`
}

type forgeSourcesResponse struct {
	Sources []forgeSourceResponse `json:"sources"`
}

type forgeIssue = forge.Issue

type importDuplicate struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Via   string `json:"via"`
}

type importPreviewDraft struct {
	storyDraft
	Link        string           `json:"link,omitempty"`
	ExternalKey string           `json:"external_key,omitempty"`
	URL         string           `json:"url,omitempty"`
	DuplicateOf *importDuplicate `json:"duplicate_of,omitempty"`
}

type importPreviewResponse struct {
	Kind      string               `json:"kind"`
	TotalHint int                  `json:"total_hint"`
	Fetched   int                  `json:"fetched"`
	Truncated bool                 `json:"truncated"`
	Note      string               `json:"note"`
	Drafts    []importPreviewDraft `json:"drafts"`
}

type importPreviewRequest struct {
	Source string `json:"source"`
	Ref    string `json:"ref"`
	Max    int    `json:"max"`
}

type importLinksRequest struct {
	Source string            `json:"source"`
	Items  []forge.LinkInput `json:"items"`
}

type importProvenanceRequest struct {
	Link string `json:"link"`
}

type importDriftRequest struct {
	Source      string `json:"source"`
	ExternalKey string `json:"external_key"`
}

type importDriftResponse struct {
	State         string `json:"state"`
	Link          string `json:"link"`
	URL           string `json:"url"`
	TitleChanged  *bool  `json:"title_changed,omitempty"`
	UpstreamTitle string `json:"upstream_title"`
	BaselineTitle string `json:"baseline_title"`
	BaselineAt    string `json:"baseline_at"`
	CheckedAt     string `json:"checked_at"`
	Summary       string `json:"summary"`
	Revision      string `json:"revision,omitempty"`
}

type importDriftAcceptResponse struct {
	BaselineAt string `json:"baseline_at"`
}

type importProvenanceItem struct {
	Source      string `json:"source"`
	ExternalKey string `json:"external_key"`
	Title       string `json:"title"`
	URL         string `json:"url"`
}

type importProvenanceResponse struct {
	Items []importProvenanceItem `json:"items"`
}

type importDriftAcceptRequest struct {
	Source      string `json:"source"`
	ExternalKey string `json:"external_key"`
	Revision    string `json:"revision"`
}

type forgeTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type forgeTestTarget struct {
	baseURL string
	pat     string
}

type forgeHTTPResponse struct {
	status int
	header http.Header
	body   []byte
}

type forgeRef struct {
	Source    store.ForgeSource
	Kind      string
	Project   string
	Issue     int
	Milestone int
	pat       string
}

func sharedRef(value forgeRef) forge.Ref {
	return forge.Ref{Source: value.Source, Kind: value.Kind, Project: value.Project, Issue: value.Issue, Milestone: value.Milestone}.WithCredential(value.pat)
}

func localRef(value forge.Ref) forgeRef {
	return forgeRef{Source: value.Source, Kind: value.Kind, Project: value.Project, Issue: value.Issue, Milestone: value.Milestone}
}

func parseForgeRef(sources []store.ForgeSource, source, raw string) (forgeRef, error) {
	value, err := forge.ParseRef(sources, source, raw)
	return localRef(value), err
}

func forgeSourceByName(sources []store.ForgeSource, name string) (store.ForgeSource, bool) {
	return forge.SourceByName(sources, name)
}

func importIssueProvenance(ref forgeRef, issue forgeIssue) (string, string) {
	return forge.IssueProvenance(sharedRef(ref), issue)
}

func stripModelLinkTags(tags []string) []string { return forge.StripModelLinkTags(tags) }

func newForgeClient() *http.Client { return forge.NewHTTPClient() }

func (s *server) sharedForge() *forge.Service {
	s.forgeOnce.Do(func() {
		s.forgeEngine = forge.New(s.store, s.aiRunner(), s.forgeClient)
	})
	return s.forgeEngine
}

func (s *server) fetchIssue(ctx context.Context, ref forgeRef) (forgeIssue, error) {
	issue, err := s.sharedForge().FetchIssue(ctx, sharedRef(ref))
	return issue, serverForgeError(err)
}

func (s *server) fetchIssues(ctx context.Context, ref forgeRef, max int) ([]forgeIssue, int, bool, string, error) {
	issues, total, truncated, note, err := s.sharedForge().FetchIssues(ctx, sharedRef(ref), max)
	return issues, total, truncated, note, serverForgeError(err)
}

func (s *server) forgeGet(ctx context.Context, ref forgeRef, base, path string, query url.Values) (forgeHTTPResponse, error) {
	response, err := s.sharedForge().Get(ctx, sharedRef(ref), base, path, query)
	return forgeHTTPResponse{status: response.Status, header: response.Header, body: response.Body}, serverForgeError(err)
}

func (s *server) fetchForgeComments(ctx context.Context, ref forgeRef, base, path string) ([]string, error) {
	comments, err := s.sharedForge().FetchComments(ctx, sharedRef(ref), base, path)
	return comments, serverForgeError(err)
}

func (s *server) fetchIssueSnapshot(ctx context.Context, ref forgeRef) (forgeIssue, string, string, error) {
	issue, body, path, err := s.sharedForge().FetchIssueSnapshot(ctx, sharedRef(ref))
	return issue, body, path, serverForgeError(err)
}

func (s *server) forgeIssuesList(ctx context.Context, ref forgeRef, base string) (string, url.Values, error) {
	path, query, err := s.sharedForge().IssuesList(ctx, sharedRef(ref), base)
	return path, query, serverForgeError(err)
}

func serverForgeError(err error) error {
	if err == nil {
		return nil
	}
	var categorized *forge.Error
	if errors.As(err, &categorized) {
		return &aiError{code: categorized.Code, msg: categorized.Message}
	}
	return err
}

func parseForgeIssueList(kind string, body []byte) ([]forgeIssue, error) {
	return forge.ParseIssueList(kind, body)
}
func forgeProjectPath(ref forgeRef) (string, error)   { return forge.ProjectPath(sharedRef(ref)) }
func forgeIssuePath(ref forgeRef) (string, error)     { return forge.IssuePath(sharedRef(ref)) }
func forgeMilestonePath(ref forgeRef) (string, error) { return forge.MilestonePath(sharedRef(ref)) }
func forgeProjectIssuesPath(ref forgeRef) (string, error) {
	return forge.ProjectIssuesPath(sharedRef(ref))
}
func forgeTotalHint(header http.Header) int { return forge.TotalHint(header) }
func parseGitLabRef(source store.ForgeSource, path string) (forgeRef, error) {
	value, err := forge.ParseGitLabRef(source, path)
	return localRef(value), err
}
func parseGitHubRef(source store.ForgeSource, path string) (forgeRef, error) {
	value, err := forge.ParseGitHubRef(source, path)
	return localRef(value), err
}
func forgeIssueListRequest(ref forgeRef) (string, url.Values, error) {
	return forge.IssueListRequest(sharedRef(ref))
}
func setForgeMilestoneQuery(kind string, query url.Values, body []byte) bool {
	return forge.SetMilestoneQuery(kind, query, body)
}
func parseForgeComments(kind string, body []byte) ([]string, error) {
	return forge.ParseComments(kind, body)
}
func appendBoundedForgeComment(comments []string, body string) []string {
	return forge.AppendBoundedComment(comments, body)
}
func forgeAPIBase(kind, base string) (string, error)       { return forge.APIBase(kind, base) }
func appendImportNote(note, extra string) string           { return forge.AppendImportNote(note, extra) }
func importRefKind(ref forgeRef) string                    { return forge.RefKind(sharedRef(ref)) }
func forgeRefID(raw string) (int, error)                   { return forge.RefID(raw) }
func normalizeForgeProbeBase(raw string) (*url.URL, error) { return forge.NormalizeProbeBase(raw) }
func forgeURLPort(value *url.URL) string                   { return forge.URLPort(value) }
func drainForgeResponse(response *http.Response) error     { return forge.DrainResponse(response) }
func validForgeSourceName(name string) bool                { return forge.ValidSourceName(name) }
func importDriftRevision(baseline store.ImportBaseline) string {
	return forge.DriftRevision(baseline)
}
func validImportDriftRevision(revision string) bool { return forge.ValidDriftRevision(revision) }

func (s *server) authorizeImportDriftTarget(w http.ResponseWriter, user, source, key string) (store.ImportLink, forgeRef, bool) {
	link, ref, err := s.sharedForge().AuthorizeDrift(user, source, key)
	if err == nil {
		return link, localRef(ref), true
	}
	code, message := http.StatusInternalServerError, storageErrorMessage
	var categorized *forge.Error
	if errors.As(err, &categorized) {
		code, message = categorized.Code, categorized.Message
	}
	http.Error(w, message, code)
	return store.ImportLink{}, forgeRef{}, false
}

func newForgeTestRequest(ctx context.Context, kind string, target forgeTestTarget, project string) (*http.Request, error) {
	return forge.NewTestRequest(ctx, kind, target.baseURL, target.pat, project)
}

func (s *server) forgeConnectionOK(request *http.Request, _ string) bool {
	return forge.ExecuteTest(s.forgeClient, request) == nil
}

func (s *server) handleImportPreview(w http.ResponseWriter, r *http.Request, user string) {
	var request importPreviewRequest
	if !decodeForgeRequest(w, r, &request) {
		return
	}
	preview, err := s.sharedForge().Preview(r.Context(), user, forge.PreviewRequest{Source: request.Source, Ref: request.Ref, Max: request.Max})
	if err != nil {
		writeSharedForgeError(w, user, "import preview", err)
		return
	}
	response := importPreviewResponse{Kind: preview.Kind, TotalHint: preview.TotalHint, Fetched: preview.Fetched, Truncated: preview.Truncated, Note: preview.Note, Drafts: make([]importPreviewDraft, 0, len(preview.Drafts))}
	for _, draft := range preview.Drafts {
		item := importPreviewDraft{storyDraft: draft.Draft, Link: draft.Link, ExternalKey: draft.ExternalKey, URL: draft.URL}
		if draft.Duplicate != nil {
			item.DuplicateOf = &importDuplicate{ID: draft.Duplicate.ID, Title: draft.Duplicate.Title, Via: draft.Duplicate.Via}
		}
		response.Drafts = append(response.Drafts, item)
	}
	writeJSON(w, response)
}

func (s *server) handleImportLinks(w http.ResponseWriter, r *http.Request, user string) {
	var request importLinksRequest
	if !decodeForgeRequest(w, r, &request) {
		return
	}
	if err := s.sharedForge().RecordLinks(user, request.Source, request.Items); err != nil {
		writeSharedForgeError(w, user, "import links", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleImportProvenance(w http.ResponseWriter, r *http.Request, user string) {
	var request importProvenanceRequest
	if !decodeForgeRequest(w, r, &request) {
		return
	}
	links, err := s.sharedForge().Provenance(user, request.Link)
	if err != nil {
		writeSharedForgeError(w, user, "import provenance", err)
		return
	}
	response := importProvenanceResponse{Items: make([]importProvenanceItem, 0, len(links))}
	for _, link := range links {
		response.Items = append(response.Items, importProvenanceItem{Source: link.Source, ExternalKey: link.ExternalKey, Title: link.Title, URL: link.URL})
	}
	writeJSON(w, response)
}

func (s *server) handleImportDrift(w http.ResponseWriter, r *http.Request, user string) {
	var request importDriftRequest
	if !decodeForgeRequest(w, r, &request) {
		return
	}
	result, err := s.sharedForge().CheckDrift(r.Context(), user, request.Source, request.ExternalKey)
	if err != nil {
		writeSharedForgeError(w, user, "import drift", err)
		return
	}
	writeJSON(w, importDriftResponse{State: result.State, Link: result.Link, URL: result.URL, TitleChanged: result.TitleChanged, UpstreamTitle: result.UpstreamTitle, BaselineTitle: result.BaselineTitle, BaselineAt: result.BaselineAt, CheckedAt: result.CheckedAt, Summary: result.Summary, Revision: result.Revision})
}

func (s *server) handleImportDriftAccept(w http.ResponseWriter, r *http.Request, user string) {
	var request importDriftAcceptRequest
	if !decodeForgeRequest(w, r, &request) {
		return
	}
	at, err := s.sharedForge().AcceptDrift(r.Context(), user, request.Source, request.ExternalKey, request.Revision)
	if err != nil {
		if errors.Is(err, forge.ErrUpstreamChanged) {
			http.Error(w, "upstream changed; check again", http.StatusConflict)
			return
		}
		writeSharedForgeError(w, user, "import drift accept", err)
		return
	}
	writeJSON(w, importDriftAcceptResponse{BaselineAt: at})
}

func decodeForgeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	body, ok := readBody(w, r)
	if !ok {
		return false
	}
	if err := json.Unmarshal(body, target); err != nil {
		http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
		return false
	}
	return true
}

func writeSharedForgeError(w http.ResponseWriter, user, operation string, err error) {
	code, message := http.StatusBadGateway, connectionFailedMessage
	var categorized *forge.Error
	if errors.As(err, &categorized) {
		code = categorized.Code
		if code != http.StatusBadGateway {
			message = categorized.Message
		}
	}
	if code == http.StatusBadGateway {
		log.Printf("forge: %s for %s failed: %v", operation, user, err)
	}
	http.Error(w, message, code)
}

func (s *server) handleGetIntegrations(w http.ResponseWriter, r *http.Request, user string) {
	sources, err := s.sharedForge().Sources(user)
	if err != nil {
		writeSharedForgeError(w, user, "list integrations", err)
		return
	}
	response := forgeSourcesResponse{Sources: make([]forgeSourceResponse, 0, len(sources))}
	for _, source := range sources {
		response.Sources = append(response.Sources, forgeSourceResponse{Name: source.Name, Kind: source.Kind, BaseURL: source.BaseURL, HasToken: source.HasToken})
	}
	writeJSON(w, response)
}

func (s *server) handlePutIntegration(w http.ResponseWriter, r *http.Request, user string) {
	var request struct {
		Kind    string  `json:"kind"`
		BaseURL *string `json:"base_url"`
		PAT     *string `json:"pat"`
	}
	if !decodeForgeRequest(w, r, &request) {
		return
	}
	cleared, err := s.sharedForge().SaveSource(user, r.PathValue("name"), request.Kind, request.BaseURL, request.PAT)
	if err != nil {
		writeSharedForgeError(w, user, "save integration", err)
		return
	}
	if cleared {
		writeJSON(w, map[string]bool{"token_cleared": true})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteIntegration(w http.ResponseWriter, r *http.Request, user string) {
	if err := s.sharedForge().DeleteSource(user, r.PathValue("name")); err != nil {
		writeSharedForgeError(w, user, "delete integration", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleTestIntegration(w http.ResponseWriter, r *http.Request, user string) {
	if !forge.ValidSourceName(r.PathValue("name")) {
		http.Error(w, invalidIntegrationNameMessage, http.StatusBadRequest)
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var request struct {
		BaseURL *string `json:"base_url"`
		PAT     *string `json:"pat"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &request); err != nil {
			http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
			return
		}
	}
	config := forge.ForgeProbeConfig{Name: r.PathValue("name"), Saved: true}
	if request.BaseURL != nil {
		config.BaseURL = *request.BaseURL
	}
	if request.PAT != nil {
		config.Token = *request.PAT
	}
	if err := s.sharedForge().Probe(r.Context(), user, config); err != nil {
		writeJSON(w, forgeTestResponse{Error: err.Error()})
		return
	}
	writeJSON(w, forgeTestResponse{OK: true})
}

type ForgeProbeConfig = forge.ForgeProbeConfig

type ForgeProber struct {
	store  *store.Store
	client *http.Client
}

func NewForgeProber(st *store.Store) *ForgeProber {
	return &ForgeProber{store: st, client: forge.NewHTTPClient()}
}

func (p *ForgeProber) Probe(ctx context.Context, user string, config ForgeProbeConfig) error {
	return forge.NewForgeProberWithClient(p.store, p.client).Probe(ctx, user, config)
}
