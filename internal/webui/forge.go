package webui

// forge.go is the web parity surface for the TUI's issue import overlay
// (internal/tui/issueimport) and its drift pane (internal/tui/carddetail):
// the same configured-source preview, per-card create, provenance lookup and
// upstream drift steps, in the JSON shape of docs/web-api-forge.md.
//
// Credentials never appear on this surface. A forge personal access token is
// configured once against a named source and sealed in the store; requests
// here name a source, never a token, so nothing to log or echo ever reaches
// these handlers.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func init() { featureRoutes = append(featureRoutes, (*server).forgeRoutes) }

// The overlay's count stepper: one to twenty issues, eight by default.
const (
	defaultForgeMax = 8
	maxForgeMax     = 20
)

// forgeAPI binds one forge service to the server. The service is built once
// per handler rather than per request because AcceptDrift serializes its
// compare-and-swap on a per-user lock the service itself owns.
type forgeAPI struct {
	s   *server
	svc *forge.Service
}

// newForgeService is the construction seam. Tests point the forge and AI
// clients at httptest servers; production gets the guarded clients, which
// refuse private addresses and cross-host redirects.
var newForgeService = defaultForgeService

func defaultForgeService(s *server) *forge.Service {
	skills := ""
	if s.dataDir != "" {
		skills = filepath.Join(s.dataDir, "skills")
	}
	return forge.New(s.st, ai.NewRunner(s.st, skills, nil, nil), nil)
}

func (s *server) forgeRoutes() []route {
	f := &forgeAPI{s: s, svc: newForgeService(s)}
	return []route{
		{"POST", "/api/forge/preview", f.preview},
		{"POST", "/api/forge/import", f.importCard},
		{"GET", "/api/forge/provenance", f.provenance},
		{"POST", "/api/forge/drift/check", f.driftCheck},
		{"POST", "/api/forge/drift/accept", f.driftAccept},
	}
}

// --- wire types ---

type forgeBaselineJSON struct {
	Title   string `json:"title"`
	Hash    string `json:"hash"`
	Excerpt string `json:"excerpt"`
	At      string `json:"at"`
}

// forgeDuplicateJSON is the overlay's dedupe marker. Via "link" means the
// card is already imported (the overlay starts those unticked); "similar" is
// a fuzzy title hit from SearchSimilar.
type forgeDuplicateJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Via   string `json:"via"`
}

// forgeDraftJSON is one proposal: the task wire field names plus the
// provenance the import step must hand back verbatim.
type forgeDraftJSON struct {
	Title       string              `json:"title"`
	Emoji       string              `json:"emoji,omitempty"`
	Desc        string              `json:"desc,omitempty"`
	Prio        int                 `json:"prio"`
	Due         string              `json:"due,omitempty"`
	Effort      string              `json:"effort,omitempty"`
	Tags        []string            `json:"tags,omitempty"`
	Checks      []checkJSON         `json:"checks,omitempty"`
	Source      string              `json:"source"`
	Link        string              `json:"link"`
	ExternalKey string              `json:"externalKey"`
	URL         string              `json:"url"`
	Baseline    forgeBaselineJSON   `json:"baseline"`
	Duplicate   *forgeDuplicateJSON `json:"duplicate,omitempty"`
}

type forgePreviewJSON struct {
	Kind      string           `json:"kind"`
	TotalHint int              `json:"totalHint"`
	Fetched   int              `json:"fetched"`
	Truncated bool             `json:"truncated"`
	Note      string           `json:"note,omitempty"`
	Drafts    []forgeDraftJSON `json:"drafts"`
}

type forgeLinkJSON struct {
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	ExternalKey string `json:"externalKey"`
	Link        string `json:"link"`
	URL         string `json:"url"`
	Title       string `json:"title"`
}

type forgeDriftJSON struct {
	State         string `json:"state"`
	Link          string `json:"link"`
	URL           string `json:"url"`
	TitleChanged  *bool  `json:"titleChanged,omitempty"`
	UpstreamTitle string `json:"upstreamTitle,omitempty"`
	BaselineTitle string `json:"baselineTitle,omitempty"`
	BaselineAt    string `json:"baselineAt,omitempty"`
	CheckedAt     string `json:"checkedAt"`
	Summary       string `json:"summary,omitempty"`
	Revision      string `json:"revision,omitempty"`
}

// forgeImportInput is one reviewed card plus the provenance the preview
// stamped on it. The link block is passed back unchanged: the service
// re-derives it from the configured source and refuses a mismatch.
type forgeImportInput struct {
	Source string            `json:"source"`
	Task   addTaskInput      `json:"task"`
	Link   forgeLinkInputRaw `json:"link"`
}

type forgeLinkInputRaw struct {
	ExternalKey string            `json:"externalKey"`
	Link        string            `json:"link"`
	URL         string            `json:"url"`
	Title       string            `json:"title"`
	Baseline    forgeBaselineJSON `json:"baseline"`
}

func toForgeBaselineJSON(b store.ImportBaseline) forgeBaselineJSON {
	return forgeBaselineJSON{Title: b.Title, Hash: b.Hash, Excerpt: b.Excerpt, At: b.At}
}

func toForgeDraftJSON(d forge.Draft) forgeDraftJSON {
	out := forgeDraftJSON{
		Title:       d.Title,
		Emoji:       d.Emoji,
		Desc:        d.Desc,
		Prio:        d.Prio,
		Due:         d.Due,
		Effort:      d.Effort,
		Tags:        d.Tags,
		Source:      d.SourceName,
		Link:        d.Link,
		ExternalKey: d.ExternalKey,
		URL:         d.URL,
		Baseline:    toForgeBaselineJSON(d.Baseline),
	}
	for _, c := range d.Checks {
		out.Checks = append(out.Checks, checkJSON{Text: c.Text, Done: c.Done})
	}
	if d.Duplicate != nil {
		out.Duplicate = &forgeDuplicateJSON{ID: d.Duplicate.ID, Title: d.Duplicate.Title, Via: d.Duplicate.Via}
	}
	return out
}

func toForgePreviewJSON(p forge.Preview) forgePreviewJSON {
	out := forgePreviewJSON{
		Kind:      p.Kind,
		TotalHint: p.TotalHint,
		Fetched:   p.Fetched,
		Truncated: p.Truncated,
		Note:      p.Note,
		Drafts:    make([]forgeDraftJSON, 0, len(p.Drafts)),
	}
	for _, d := range p.Drafts {
		out.Drafts = append(out.Drafts, toForgeDraftJSON(d))
	}
	return out
}

// --- error mapping ---

// writeForgeError maps the categorized failures the forge and AI packages
// raise onto the contract's status table. Both carry caller-safe messages;
// an uncategorized error is upstream noise and is reported as a bad gateway
// without echoing its text.
func writeForgeError(w http.ResponseWriter, err error) {
	var forgeErr *forge.Error
	var aiErr *ai.Error
	switch {
	case errors.As(err, &forgeErr):
		writeError(w, forgeStatus(forgeErr.Code), forgeErr.Message)
	case errors.As(err, &aiErr):
		writeError(w, forgeStatus(aiErr.Code), aiErr.Message)
	case errors.Is(err, forge.ErrUpstreamChanged):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "forge request timed out")
	default:
		writeError(w, http.StatusBadGateway, "forge request failed")
	}
}

// forgeStatus keeps a categorized code only when it is a status this API can
// legitimately answer with; anything else is reported as an upstream failure.
func forgeStatus(code int) int {
	if code >= 400 && code <= 599 {
		return code
	}
	return http.StatusBadGateway
}

// requireSource rejects an unknown source with 404 before any network call.
// The service would refuse it too, but as a 400 that cannot be told apart
// from a bad reference.
func (f *forgeAPI) requireSource(name string) error {
	sources, err := f.svc.Sources(f.s.user)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if strings.EqualFold(source.Name, strings.TrimSpace(name)) {
			return nil
		}
	}
	return &forge.Error{Code: http.StatusNotFound, Message: "unknown forge source"}
}

// --- handlers ---

// forgeMax clamps the requested count to the overlay's stepper range.
func forgeMax(n int) int {
	if n <= 0 {
		return defaultForgeMax
	}
	return min(n, maxForgeMax)
}

func (f *forgeAPI) preview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source string `json:"source"`
		Ref    string `json:"ref"`
		Max    int    `json:"max"`
	}
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	if strings.TrimSpace(in.Ref) == "" {
		writeError(w, http.StatusBadRequest, "ref must not be empty")
		return
	}
	if err := f.requireSource(in.Source); err != nil {
		writeForgeError(w, err)
		return
	}
	preview, err := f.svc.Preview(r.Context(), f.s.user,
		forge.PreviewRequest{Source: in.Source, Ref: strings.TrimSpace(in.Ref), Max: forgeMax(in.Max)})
	if err != nil {
		writeForgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toForgePreviewJSON(preview))
}

func (f *forgeAPI) importCard(w http.ResponseWriter, r *http.Request) {
	var in forgeImportInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	if err := f.requireSource(in.Source); err != nil {
		writeForgeError(w, err)
		return
	}
	task, err := f.s.forgeTask(in.Task)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := f.svc.CreateTask(f.s.user, in.Source, task, forge.LinkInput{
		ExternalKey: in.Link.ExternalKey,
		Link:        in.Link.Link,
		URL:         in.Link.URL,
		Title:       in.Link.Title,
		Baseline: store.ImportBaseline{
			Title:   in.Link.Baseline.Title,
			Hash:    in.Link.Baseline.Hash,
			Excerpt: in.Link.Baseline.Excerpt,
			At:      in.Link.Baseline.At,
		},
	})
	if err != nil {
		writeForgeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTaskJSON(created))
}

// forgeTask turns a reviewed draft into the card the service writes. Like the
// overlay, it stamps the one project every task carries at write time, so a
// `kb project use` between preview and import is picked up.
func (s *server) forgeTask(in addTaskInput) (board.Task, error) {
	if strings.TrimSpace(in.Title) == "" {
		return board.Task{}, errors.New("title must not be empty")
	}
	if in.Prio != 0 && !board.ValidPrio(in.Prio) {
		return board.Task{}, errors.New("invalid prio: must be 1 high, 2 medium, or 3 low")
	}
	t := board.Task{
		Emoji:   in.Emoji,
		Title:   in.Title,
		Desc:    in.Desc,
		Status:  board.StatusTodo,
		Blocked: in.Blocked,
		Prio:    in.Prio,
		Due:     in.Due,
		Effort:  in.Effort,
		Checks:  toBoardChecks(in.Checks),
	}
	if in.Status != "" {
		status, err := parseStatus(in.Status)
		if err != nil {
			return board.Task{}, err
		}
		t.Status = status
	}
	tags, err := cliapp.ProjectTags(in.Tags, in.Project, s.dataDir, "")
	if err != nil {
		return board.Task{}, err
	}
	t.Tags = tags
	return t, nil
}

func (f *forgeAPI) provenance(w http.ResponseWriter, r *http.Request) {
	items, err := f.svc.Provenance(f.s.user, r.URL.Query().Get("link"))
	if err != nil {
		writeForgeError(w, err)
		return
	}
	out := make([]forgeLinkJSON, 0, len(items))
	for _, item := range items {
		out = append(out, forgeLinkJSON{
			Source:      item.Source,
			Kind:        item.Kind,
			ExternalKey: item.ExternalKey,
			Link:        item.Link,
			URL:         item.URL,
			Title:       item.Title,
		})
	}
	writeJSON(w, http.StatusOK, map[string][]forgeLinkJSON{"links": out})
}

// forgeDriftInput is the selection both drift steps address: a configured
// source plus one imported item's external key.
type forgeDriftInput struct {
	Source      string `json:"source"`
	ExternalKey string `json:"externalKey"`
	Revision    string `json:"revision"`
}

// forgeDriftSelection decodes and validates the shared drift selection.
func (f *forgeAPI) forgeDriftSelection(w http.ResponseWriter, r *http.Request) (forgeDriftInput, bool) {
	var in forgeDriftInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return in, false
	}
	if strings.TrimSpace(in.ExternalKey) == "" {
		writeError(w, http.StatusBadRequest, "externalKey must not be empty")
		return in, false
	}
	if err := f.requireSource(in.Source); err != nil {
		writeForgeError(w, err)
		return in, false
	}
	return in, true
}

func (f *forgeAPI) driftCheck(w http.ResponseWriter, r *http.Request) {
	in, ok := f.forgeDriftSelection(w, r)
	if !ok {
		return
	}
	drift, err := f.svc.CheckDrift(r.Context(), f.s.user, in.Source, in.ExternalKey)
	if err != nil {
		writeForgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, forgeDriftJSON{
		State:         drift.State,
		Link:          drift.Link,
		URL:           drift.URL,
		TitleChanged:  drift.TitleChanged,
		UpstreamTitle: drift.UpstreamTitle,
		BaselineTitle: drift.BaselineTitle,
		BaselineAt:    drift.BaselineAt,
		CheckedAt:     drift.CheckedAt,
		Summary:       drift.Summary,
		Revision:      drift.Revision,
	})
}

func (f *forgeAPI) driftAccept(w http.ResponseWriter, r *http.Request) {
	in, ok := f.forgeDriftSelection(w, r)
	if !ok {
		return
	}
	at, err := f.svc.AcceptDrift(r.Context(), f.s.user, in.Source, in.ExternalKey, in.Revision)
	if err != nil {
		writeForgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"baselineAt": at})
}
