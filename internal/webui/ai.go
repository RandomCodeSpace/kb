package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// The AI endpoints are review-only: a run proposes cards, the browser shows
// them, and nothing reaches the board until the user posts an accepted draft
// back to /api/tasks. Contract: docs/web-api-ai.md.
func init() { featureRoutes = append(featureRoutes, (*server).aiRoutes) }

// Per-flow output budgets, matching the TUI call sites they mirror
// (cardeditor draftMaxTokens, adrsplit splitMaxTokens) and the ADR size the
// split overlay accepts.
const (
	aiDraftMaxTokens = 4096
	aiSplitMaxTokens = 8192
	// maxSplitStories mirrors the ai package's story cap (ai.NormalizeStoryCount).
	maxSplitStories = 20
	maxADRBytes     = 64 << 10
	aiUnconfigured  = "AI is not configured — set the base URL and model in settings"
)

// aiRunner is the slice of *ai.Runner the web API uses. Every run is
// ScopeReadOnly, so no endpoint here can fetch a link or write a card.
type aiRunner interface {
	RunSkill(ctx context.Context, user string, scope ai.Scope, skill, input string, maxCards int, maxTokens int64) (ai.RunResult, error)
	LoadSkills() ([]ai.Skill, error)
}

// newAIRunner constructs the runner one request uses. It is a variable so a
// test can stub the upstream without an HTTP endpoint; the default keeps the
// ai package's own guarded client, timeouts and address policy.
var newAIRunner = func(st *store.Store, dataDir string) aiRunner {
	skillsDir := ""
	if dataDir != "" {
		skillsDir = filepath.Join(dataDir, "skills")
	}
	return ai.NewRunner(st, skillsDir, nil, nil)
}

func (s *server) aiRoutes() []route {
	return []route{
		{"GET", "/api/ai/status", s.aiStatus},
		{"GET", "/api/ai/skills", s.aiSkills},
		{"POST", "/api/ai/draft", s.aiDraft},
		{"POST", "/api/ai/split", s.aiSplit},
	}
}

// --- wire types ---

// aiDraftJSON is one proposed card. It carries the task wire type's field
// names minus everything the store assigns (id, seq, status, position,
// timestamps): a draft is not a task until the UI posts it to /api/tasks.
type aiDraftJSON struct {
	Title  string      `json:"title"`
	Emoji  string      `json:"emoji,omitempty"`
	Desc   string      `json:"desc,omitempty"`
	Prio   int         `json:"prio"`
	Due    string      `json:"due,omitempty"`
	Effort string      `json:"effort,omitempty"`
	Tags   []string    `json:"tags,omitempty"`
	Checks []checkJSON `json:"checks,omitempty"`
	Source int         `json:"source,omitempty"`
}

func toDraftJSON(d ai.Draft) aiDraftJSON {
	out := aiDraftJSON{
		Title:  d.Title,
		Emoji:  d.Emoji,
		Desc:   d.Desc,
		Prio:   d.Prio,
		Due:    d.Due,
		Effort: d.Effort,
		Tags:   d.Tags,
		Source: d.Source,
	}
	for _, c := range d.Checks {
		out.Checks = append(out.Checks, checkJSON{Text: c.Text, Done: c.Done})
	}
	return out
}

type aiStatusJSON struct {
	Configured bool   `json:"configured"`
	BaseURL    string `json:"baseURL"`
	Model      string `json:"model"`
	HasKey     bool   `json:"hasKey"`
}

type aiSkillJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// aiCardInput is the current card a rewrite starts from. The field set and
// the JSON it marshals to are the ones cardeditor appends to a draft prompt,
// so the skill sees the same block on both surfaces.
type aiCardInput struct {
	Title  string      `json:"title"`
	Desc   string      `json:"desc"`
	Prio   int         `json:"prio"`
	Due    string      `json:"due"`
	Effort string      `json:"effort"`
	Tags   []string    `json:"tags"`
	Checks []checkJSON `json:"checks"`
}

type aiDraftInput struct {
	Prompt string       `json:"prompt"`
	Card   *aiCardInput `json:"card"`
}

type aiSplitInput struct {
	Text string `json:"text"`
	Max  int    `json:"max"`
}

type aiDraftResponse struct {
	Card       aiDraftJSON `json:"card"`
	Commentary string      `json:"commentary,omitempty"`
	Partial    bool        `json:"partial,omitempty"`
}

type aiSplitResponse struct {
	Cards      []aiDraftJSON `json:"cards"`
	Commentary string        `json:"commentary,omitempty"`
	Partial    bool          `json:"partial,omitempty"`
}

// --- handlers ---

func (s *server) aiStatus(w http.ResponseWriter, _ *http.Request) {
	set, err := s.st.AISettings(s.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, aiStatusJSON{
		Configured: strings.TrimSpace(set.BaseURL) != "",
		BaseURL:    set.BaseURL,
		Model:      set.Model,
		HasKey:     set.HasKey,
	})
}

func (s *server) aiSkills(w http.ResponseWriter, _ *http.Request) {
	skills, err := newAIRunner(s.st, s.dataDir).LoadSkills()
	if err != nil {
		writeAIError(w, err)
		return
	}
	out := make([]aiSkillJSON, 0, len(skills))
	for _, skill := range skills {
		out = append(out, aiSkillJSON{Name: skill.Name, Description: skill.Description})
	}
	writeJSON(w, http.StatusOK, map[string][]aiSkillJSON{"skills": out})
}

// aiDraft drafts one card from a request, or rewrites the card the body
// carries — the card editor's ai-draft field on the web.
func (s *server) aiDraft(w http.ResponseWriter, r *http.Request) {
	var in aiDraftInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt must not be empty")
		return
	}
	run, ok := s.runSkill(w, r, "story-draft", draftPrompt(prompt, in.Card), 1, aiDraftMaxTokens)
	if !ok {
		return
	}
	if len(run.Cards) == 0 {
		writeError(w, http.StatusBadGateway, "the model returned no usable card")
		return
	}
	writeJSON(w, http.StatusOK, aiDraftResponse{
		Card:       toDraftJSON(run.Cards[0]),
		Commentary: run.Commentary,
		Partial:    run.Partial,
	})
}

// draftPrompt packs the request the way cardeditor does: a bare request
// creates a card, a request plus the current card JSON rewrites it.
func draftPrompt(prompt string, card *aiCardInput) string {
	if card == nil {
		return "Create a new kanban card for this request:\n" + prompt
	}
	if card.Tags == nil {
		card.Tags = []string{}
	}
	if card.Checks == nil {
		card.Checks = []checkJSON{}
	}
	// The field set is fixed and marshals cleanly, so the error is dead.
	current, _ := json.Marshal(card)
	return "Update the kanban card according to this request:\n" + prompt +
		"\n\nCurrent card JSON:\n" + string(current)
}

// aiSplit turns one ADR or design document into proposed stories, the
// adrsplit overlay's run. Nothing is persisted.
func (s *server) aiSplit(w http.ResponseWriter, r *http.Request) {
	var in aiSplitInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeError(w, http.StatusBadRequest, "text must not be empty")
		return
	}
	if len(in.Text) > maxADRBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "document exceeds 64 KiB")
		return
	}
	// Bound the count here with plain comparisons so the request value never
	// reaches the runner's allocation unchecked; the runner clamps again.
	stories := ai.NormalizeStoryCount(0)
	if in.Max >= 1 && in.Max <= maxSplitStories {
		stories = in.Max
	}
	run, ok := s.runSkill(w, r, "adr-split", in.Text, stories, aiSplitMaxTokens)
	if !ok {
		return
	}
	cards := make([]aiDraftJSON, 0, len(run.Cards))
	for _, card := range run.Cards {
		cards = append(cards, toDraftJSON(card))
	}
	writeJSON(w, http.StatusOK, aiSplitResponse{
		Cards:      cards,
		Commentary: run.Commentary,
		Partial:    run.Partial,
	})
}

// runSkill refuses an unconfigured endpoint before any outbound work, then
// runs one read-only skill. It reports whether the caller should still write
// a response; every failure path has already written one.
func (s *server) runSkill(w http.ResponseWriter, r *http.Request, skill, input string, maxCards int, maxTokens int64) (ai.RunResult, bool) {
	set, err := s.st.AISettings(s.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return ai.RunResult{}, false
	}
	if strings.TrimSpace(set.BaseURL) == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": aiUnconfigured, "aiUnconfigured": true})
		return ai.RunResult{}, false
	}
	run, err := newAIRunner(s.st, s.dataDir).
		RunSkill(r.Context(), s.user, ai.ScopeReadOnly, skill, input, maxCards, maxTokens)
	if err != nil {
		writeAIError(w, err)
		return ai.RunResult{}, false
	}
	return run, true
}

// writeAIError reports an ai package failure with its own text. The package
// already keeps upstream detail out of these messages, so they pass through
// unchanged; only a timed-out request is separated, because retrying is the
// answer to that one and not to the rest.
func writeAIError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	writeError(w, status, err.Error())
}
