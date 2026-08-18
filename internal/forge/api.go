package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// ImportFetchTimeout bounds one forge selection independently of an AI run.
const ImportFetchTimeout = importFetchTimeout

// ErrUpstreamChanged is returned when a baseline revision no longer names the
// current upstream snapshot.
var ErrUpstreamChanged = errors.New("upstream changed; check again")

// Ref is a parsed configured forge selection. Its credential remains private.
type Ref struct {
	Source     store.ForgeSource
	Kind       string
	Project    string
	Issue      int
	Milestone  int
	credential string
}

func publicRef(value forgeRef) Ref {
	return Ref{Source: value.Source, Kind: value.Kind, Project: value.Project, Issue: value.Issue, Milestone: value.Milestone, credential: value.pat}
}

func privateRef(value Ref) forgeRef {
	return forgeRef{Source: value.Source, Kind: value.Kind, Project: value.Project, Issue: value.Issue, Milestone: value.Milestone, pat: value.credential}
}

func parseRef(sources []store.ForgeSource, source, raw string) (Ref, error) {
	value, err := parseForgeRef(sources, source, raw)
	return publicRef(value), err
}

func (r Ref) withCredential(value string) Ref { r.credential = value; return r }

// Issue is one bounded upstream forge issue.
type Issue = forgeIssue

func sourceByName(sources []store.ForgeSource, name string) (store.ForgeSource, bool) {
	return forgeSourceByName(sources, name)
}

// ValidSourceName reports whether a source name is safe for persisted lookup.
func ValidSourceName(name string) bool { return validForgeSourceName(name) }

// BaselineRevision identifies one accepted upstream snapshot for compare-and-swap.
func BaselineRevision(baseline store.ImportBaseline) string { return importDriftRevision(baseline) }

// ValidRevision reports whether a drift revision is a canonical digest.
func ValidRevision(revision string) bool { return validImportDriftRevision(revision) }

func issueProvenance(ref Ref, issue Issue) (string, string) {
	return importIssueProvenance(privateRef(ref), issue)
}

// ResolveIssueDocument authorizes and fetches one configured issue for the
// server's existing ADR split endpoint.
func (s *Service) ResolveIssueDocument(ctx context.Context, user, source, raw string) (Issue, string, string, error) {
	ref, err := s.authorizeRef(user, source, raw)
	if err != nil {
		return Issue{}, "", "", err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, importFetchTimeout)
	defer cancel()
	issue, err := s.fetchIssue(fetchCtx, privateRef(ref))
	if err != nil {
		return Issue{}, "", "", err
	}
	link, _ := issueProvenance(ref, issue)
	return issue, link, issue.URL, nil
}

// Draft is one import proposal with its forge provenance and duplicate hint.
type Draft struct {
	ai.Draft
	Link        string
	ExternalKey string
	URL         string
	Duplicate   *Duplicate
}

type Duplicate struct {
	ID    string
	Title string
	Via   string
}

type PreviewRequest struct {
	Source string
	Ref    string
	Max    int
}

type Preview struct {
	Kind      string
	TotalHint int
	Fetched   int
	Truncated bool
	Note      string
	Drafts    []Draft
}

func (s *Service) Sources(user string) ([]store.ForgeSource, error) {
	sources, err := s.store.ForgeSources(user)
	if err != nil {
		return nil, storageFailure(err)
	}
	return sources, nil
}

// SaveSource validates and persists one configured forge source.
func (s *Service) SaveSource(user, name, kind string, baseURL, token *string) (bool, error) {
	if !validForgeSourceName(name) {
		return false, badRequest(invalidIntegrationNameMessage, nil)
	}
	if kind != "gitlab" && kind != "github" {
		return false, badRequest("invalid forge kind", nil)
	}
	if baseURL != nil {
		if _, err := forgeAPIBase(kind, *baseURL); err != nil {
			return false, badRequest(err.Error(), err)
		}
	}
	cleared, err := s.store.SetForgeSource(user, name, kind, baseURL, token)
	if err != nil {
		switch err.Error() {
		case "store: forge base URL is required", "store: forge base URL must not contain query or fragment":
			return false, badRequest(strings.TrimPrefix(err.Error(), "store: "), err)
		default:
			return false, storageFailure(err)
		}
	}
	return cleared, nil
}

func (s *Service) DeleteSource(user, name string) error {
	if !validForgeSourceName(name) {
		return badRequest(invalidIntegrationNameMessage, nil)
	}
	if err := s.store.DeleteForgeSource(user, name); err != nil {
		return storageFailure(err)
	}
	return nil
}

func (s *Service) Probe(ctx context.Context, user string, config ForgeProbeConfig) error {
	return NewForgeProberWithClient(s.store, s.forgeClient).Probe(ctx, user, config)
}

// Preview fetches, classifies and transforms one configured selection. It is
// read-only: third-party forge text cannot receive write or fetch tools.
func (s *Service) Preview(ctx context.Context, user string, request PreviewRequest) (Preview, error) {
	ref, err := s.authorizeRef(user, request.Source, request.Ref)
	if err != nil {
		return Preview{}, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, importFetchTimeout)
	issues, total, truncated, note, err := s.fetchIssues(fetchCtx, privateRef(ref), request.Max)
	cancel()
	if err != nil {
		return Preview{}, err
	}
	duplicates, err := s.duplicates(user, ref, issues)
	if err != nil {
		return Preview{}, storageFailure(err)
	}
	fetched := len(issues)
	packed, sourceCount := packImportIssues(issues)
	result := Preview{Kind: refKind(ref), TotalHint: total, Fetched: fetched, Truncated: truncated, Note: note, Drafts: []Draft{}}
	if sourceCount < len(issues) {
		result.Truncated = true
		result.Note = appendImportNote(result.Note, importPackTruncationNote)
	}
	issues, duplicates = issues[:sourceCount], duplicates[:sourceCount]
	if sourceCount == 0 {
		return result, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, skillRunDeadline)
	defer cancel()
	run, err := s.runner.RunSkill(runCtx, user, ai.ScopeReadOnly, importTransformSkillName,
		"Transform these numbered forge issues into kanban-card proposals:\n\n"+packed, maxImportIssues, aiImportMaxTokens)
	if err != nil {
		return Preview{}, err
	}
	if run.Partial {
		result.Note = appendImportNote(result.Note, importPartialTransformNote)
	}
	result.Drafts = attachDrafts(ref, run.Cards, issues, duplicates)
	return result, nil
}

func (s *Service) authorizeRef(user, sourceName, raw string) (Ref, error) {
	sources, err := s.store.ForgeSources(user)
	if err != nil {
		return Ref{}, storageFailure(err)
	}
	ref, err := parseRef(sources, sourceName, raw)
	if err != nil {
		return Ref{}, badRequest(err.Error(), err)
	}
	selected, found := sourceByName(sources, sourceName)
	if !found {
		return Ref{}, badRequest(configuredSourceUnavailableMessage, nil)
	}
	if ref.Source.Name != selected.Name {
		return Ref{}, badRequest("reference does not match selected source", nil)
	}
	kind, baseURL, token, err := s.store.ForgePAT(user, selected.Name)
	if err != nil || kind != selected.Kind || baseURL != selected.BaseURL {
		return Ref{}, badRequest(configuredSourceUnavailableMessage, err)
	}
	return ref.withCredential(token), nil
}

func (s *Service) duplicates(user string, ref Ref, issues []Issue) ([]*Duplicate, error) {
	result := make([]*Duplicate, len(issues))
	for index, issue := range issues {
		_, externalKey := issueProvenance(ref, issue)
		exact, err := s.store.TasksByLink(user, importTagPrefix+externalKey)
		if err != nil {
			return nil, err
		}
		if len(exact) > 0 {
			result[index] = &Duplicate{ID: exact[0].ID, Title: exact[0].Title, Via: "link"}
			continue
		}
		similar, err := s.store.SearchSimilar(user, issue.Title, "", nil, 1)
		if err != nil {
			return nil, err
		}
		if len(similar) > 0 {
			result[index] = &Duplicate{ID: similar[0].ID, Title: similar[0].Title, Via: "similar"}
		}
	}
	return result, nil
}

func attachDrafts(ref Ref, drafts []ai.Draft, issues []Issue, duplicates []*Duplicate) []Draft {
	result := make([]Draft, 0, len(drafts))
	claimed := make(map[int]bool, len(drafts))
	for _, proposal := range drafts {
		proposal.Tags = stripModelLinkTags(proposal.Tags)
		item := Draft{Draft: proposal}
		if proposal.Source > 0 && proposal.Source <= len(issues) && !claimed[proposal.Source] {
			claimed[proposal.Source] = true
			issue := issues[proposal.Source-1]
			item.Link, item.ExternalKey = issueProvenance(ref, issue)
			item.URL = issue.URL
			item.Duplicate = duplicates[proposal.Source-1]
			item.Tags = append(item.Tags, linkTagPrefix+item.Link, importTagPrefix+item.ExternalKey)
		}
		result = append(result, item)
	}
	return result
}

func refKind(ref Ref) string {
	if ref.Issue > 0 {
		return "issue"
	}
	if ref.Milestone > 0 {
		return "milestone"
	}
	return "project"
}

type LinkInput struct {
	ExternalKey string `json:"external_key"`
	Link        string `json:"link"`
	URL         string `json:"url"`
	Title       string `json:"title"`
}

// RecordLinks journals provenance for compatibility clients that already
// created their cards. The TUI uses CreateTask for an atomic write.
func (s *Service) RecordLinks(user, sourceName string, items []LinkInput) error {
	if len(items) > maxImportLinks {
		return badRequest("too many import links (max 100)", nil)
	}
	if strings.TrimSpace(sourceName) == "" {
		return badRequest("source required", nil)
	}
	sources, err := s.store.ForgeSources(user)
	if err != nil {
		return storageFailure(err)
	}
	source, found := sourceByName(sources, sourceName)
	if !found {
		return badRequest(configuredSourceUnavailableMessage, nil)
	}
	links := make([]store.ImportLink, len(items))
	for index, item := range items {
		link, err := importLink(source, item)
		if err != nil {
			return err
		}
		links[index] = link
	}
	if err := s.store.RecordImportLinks(user, links); err != nil {
		if strings.HasPrefix(err.Error(), "store: import ") {
			return badRequest("invalid import link", err)
		}
		return storageFailure(err)
	}
	return nil
}

// CreateTask atomically creates one selected card and its provenance. The
// transaction removes the crash window between those two durable writes.
func (s *Service) CreateTask(user, sourceName string, task board.Task, item LinkInput) (board.Task, error) {
	sources, err := s.store.ForgeSources(user)
	if err != nil {
		return board.Task{}, storageFailure(err)
	}
	source, found := sourceByName(sources, sourceName)
	if !found {
		return board.Task{}, badRequest(configuredSourceUnavailableMessage, nil)
	}
	link, err := importLink(source, item)
	if err != nil {
		return board.Task{}, err
	}
	created, err := s.store.AddTaskWithImportLink(user, task, link)
	if err != nil {
		if strings.HasPrefix(err.Error(), "store: import ") {
			return board.Task{}, badRequest("invalid import link", err)
		}
		return board.Task{}, storageFailure(err)
	}
	return created, nil
}

func importLink(source store.ForgeSource, item LinkInput) (store.ImportLink, error) {
	if strings.TrimSpace(item.ExternalKey) == "" || strings.TrimSpace(item.Link) == "" || strings.TrimSpace(item.URL) == "" || strings.TrimSpace(item.Title) == "" {
		return store.ImportLink{}, badRequest("import link fields required", nil)
	}
	return store.ImportLink{Source: source.Name, Kind: source.Kind, ExternalKey: item.ExternalKey, Link: item.Link, URL: item.URL, Title: item.Title}, nil
}

func (s *Service) Provenance(user, link string) ([]store.ImportLink, error) {
	raw := link
	link = strings.TrimSpace(link)
	if link == "" || len(link) > 2048 || strings.ContainsAny(raw, "\r\n") {
		return nil, badRequest("invalid import link", nil)
	}
	items, err := s.store.ImportLinksByLink(user, link)
	if err != nil {
		return nil, storageFailure(err)
	}
	if len(items) == 0 {
		return nil, &Error{Code: http.StatusNotFound, Message: "import link not found"}
	}
	return items, nil
}

type Drift struct {
	State         string
	Link          string
	URL           string
	TitleChanged  *bool
	UpstreamTitle string
	BaselineTitle string
	BaselineAt    string
	CheckedAt     string
	Summary       string
	Revision      string
}

func (s *Service) authorizeDrift(user, source, externalKey string) (store.ImportLink, Ref, error) {
	items, err := s.store.ImportedAs(user, []string{externalKey})
	if err != nil {
		return store.ImportLink{}, Ref{}, storageFailure(err)
	}
	provenance, found := items[externalKey]
	if !found {
		return store.ImportLink{}, Ref{}, &Error{Code: http.StatusNotFound, Message: "import link not found"}
	}
	if !strings.EqualFold(source, provenance.Source) {
		return store.ImportLink{}, Ref{}, badRequest("source does not match imported item", nil)
	}
	ref, err := s.authorizeRef(user, source, provenance.URL)
	if err != nil {
		return store.ImportLink{}, Ref{}, err
	}
	if provenance.Kind != ref.Kind {
		return store.ImportLink{}, Ref{}, badRequest("imported item kind does not match selected source", nil)
	}
	if ref.Issue <= 0 {
		return store.ImportLink{}, Ref{}, badRequest("issue reference required", nil)
	}
	return provenance, ref, nil
}

func (s *Service) CheckDrift(ctx context.Context, user, source, externalKey string) (Drift, error) {
	ctx, cancel := context.WithTimeout(ctx, importFetchTimeout)
	defer cancel()
	provenance, ref, err := s.authorizeDrift(user, source, externalKey)
	if err != nil {
		return Drift{}, err
	}
	issue, err := s.fetchIssue(ctx, privateRef(ref))
	if err != nil {
		return Drift{}, err
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339Nano)
	current := store.NewImportBaseline(issue.Title, issue.Body, checkedAt)
	baseline, present, err := s.resolveBaseline(user, externalKey, current)
	if err != nil {
		return Drift{}, storageFailure(err)
	}
	result := Drift{Link: provenance.Link, URL: provenance.URL, UpstreamTitle: issue.Title, CheckedAt: checkedAt}
	if !present {
		result.State, result.BaselineTitle, result.BaselineAt = "baseline_recorded", current.Title, current.At
		return result, nil
	}
	changed := current.Title != baseline.Title
	result.TitleChanged, result.BaselineTitle, result.BaselineAt = &changed, baseline.Title, baseline.At
	if !changed && current.Hash == baseline.Hash {
		result.State = "unchanged"
		return result, nil
	}
	result.State = "drifted"
	result.Revision = importDriftRevision(current)
	result.Summary = s.driftSummary(ctx, user, baseline, current)
	return result, nil
}

func (s *Service) resolveBaseline(user, key string, current store.ImportBaseline) (store.ImportBaseline, bool, error) {
	baseline, created, err := s.store.CreateImportBaseline(user, key, current)
	return baseline, !created, err
}

func (s *Service) driftSummary(ctx context.Context, user string, baseline, current store.ImportBaseline) string {
	prompt := fmt.Sprintf("Summarize the material change in plain text. Do not invent details.\n\nBaseline title:\n%s\nBaseline excerpt:\n%s\n\nCurrent title:\n%s\nCurrent excerpt:\n%s",
		truncateImportText(baseline.Title, maxImportCommentBytes), baseline.Excerpt,
		truncateImportText(current.Title, maxImportCommentBytes), current.Excerpt)
	result, err := s.runner.RunText(ctx, user, importDriftSummaryPrompt, truncateImportText(prompt, maxImportPackBytes), aiDriftMaxTokens)
	if err != nil {
		return ""
	}
	return truncateImportText(strings.TrimSpace(result), maxImportCommentBytes)
}

func (s *Service) AcceptDrift(ctx context.Context, user, source, externalKey, revision string) (string, error) {
	if !validImportDriftRevision(revision) {
		return "", badRequest("invalid revision", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, importFetchTimeout)
	defer cancel()
	_, ref, err := s.authorizeDrift(user, source, externalKey)
	if err != nil {
		return "", err
	}
	lock := s.importDriftLocks.get(user)
	lock.Lock()
	defer lock.Unlock()
	baseline, present, err := s.store.ImportBaseline(user, externalKey)
	if err != nil {
		return "", storageFailure(err)
	}
	if !present {
		return "", &Error{Code: http.StatusConflict, Message: "check again"}
	}
	if importDriftRevision(baseline) == revision {
		return baseline.At, nil
	}
	issue, _, _, err := s.fetchIssueSnapshot(ctx, privateRef(ref))
	if err != nil {
		return "", err
	}
	current := store.NewImportBaseline(issue.Title, issue.Body, time.Now().UTC().Format(time.RFC3339Nano))
	if importDriftRevision(current) != revision {
		return "", fmt.Errorf("%w", ErrUpstreamChanged)
	}
	updated, err := s.store.CompareAndSwapImportBaseline(user, externalKey, baseline, current)
	if err != nil {
		return "", storageFailure(err)
	}
	if !updated {
		latest, present, err := s.store.ImportBaseline(user, externalKey)
		if err != nil {
			return "", storageFailure(err)
		}
		if present && importDriftRevision(latest) == revision {
			return latest.At, nil
		}
		return "", fmt.Errorf("%w", ErrUpstreamChanged)
	}
	return current.At, nil
}

func badRequest(message string, cause error) error {
	return &Error{Code: http.StatusBadRequest, Message: message, Cause: cause}
}
func storageFailure(cause error) error {
	return &Error{Code: http.StatusInternalServerError, Message: storageErrorMessage, Cause: cause}
}
