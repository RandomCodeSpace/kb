package webui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	kbai "github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func init() { featureRoutes = append(featureRoutes, (*server).settingsRoutes) }

const (
	// settingsTestTimeout caps one connection test, matching the TUI settings
	// pane so a stalled endpoint gives up at the same point on both surfaces.
	settingsTestTimeout = 20 * time.Second
	// settingsMessageLimit is the TUI's cap on a reported failure; a message
	// longer than this is a stack trace or an echoed body, not an explanation.
	settingsMessageLimit = 160

	aiSettingsPattern   = "/api/settings/ai"
	forgeSourcesPattern = "/api/settings/forge"
	forgeSourcePattern  = "/api/settings/forge/{name}"
	settingsTestSuffix  = "/test"

	// forgeConnectionFailed mirrors the forge package's opaque probe failure.
	// That package does not export it, so the one distinction the web API can
	// still draw — the caller's values (400) against the endpoint (502) —
	// rests on this string.
	forgeConnectionFailed = "connection failed"
)

func (s *server) settingsRoutes() []route {
	return []route{
		{"GET", aiSettingsPattern, s.getAISettings},
		{"PUT", aiSettingsPattern, s.putAISettings},
		{"POST", aiSettingsPattern + settingsTestSuffix, s.testAISettings},
		{"GET", forgeSourcesPattern, s.listForgeSources},
		{"PUT", forgeSourcePattern, s.putForgeSource},
		{"DELETE", forgeSourcePattern, s.deleteForgeSource},
		{"POST", forgeSourcePattern + settingsTestSuffix, s.testForgeSource},
	}
}

// aiProbe and forgeProbe are the connection tests the two test endpoints run.
// They are variables so tests can drive the endpoints without a live upstream.
var (
	aiProbe    = probeAI
	forgeProbe = probeForge
)

// probeAI runs the check behind the TUI's "Test connection" button: blank
// supplied fields fall back to the stored ones, and a stored key never travels
// to a newly supplied origin.
func probeAI(ctx context.Context, st *store.Store, user string, cfg kbai.Config) error {
	return kbai.NewRunner(st, "", nil, nil).Probe(ctx, user, cfg)
}

// probeForge runs the per-integration check behind the TUI's "Test" button.
func probeForge(ctx context.Context, st *store.Store, user string, cfg forge.ForgeProbeConfig) error {
	return forge.NewForgeProber(st).Probe(ctx, user, cfg)
}

// --- wire types ---

// aiSettingsJSON is the readable half of the AI configuration. The key itself
// never reaches the wire, only whether one is stored — the TUI shows no more
// either: its key field loads blank and echoes masked.
type aiSettingsJSON struct {
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
	HasKey  bool   `json:"hasKey"`
}

// aiSettingsResponse is the GET and PUT body. keyCleared is reported only when
// a write dropped the stored key, so a read never carries the field.
type aiSettingsResponse struct {
	AI         aiSettingsJSON `json:"ai"`
	KeyCleared bool           `json:"keyCleared,omitempty"`
}

// putAISettingsInput uses pointers so an omitted field is left alone and an
// explicit empty string clears it.
type putAISettingsInput struct {
	BaseURL *string `json:"baseURL"`
	Model   *string `json:"model"`
	APIKey  *string `json:"apiKey"`
}

// testAISettingsInput carries unsaved form values. Blank is not a clear here:
// the probe resolves every blank field from the stored configuration.
type testAISettingsInput struct {
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey"`
}

type forgeSettingsJSON struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	BaseURL   string `json:"baseURL"`
	HasToken  bool   `json:"hasToken"`
	CreatedAt string `json:"createdAt"`
}

type forgeSourceResponse struct {
	Source       forgeSettingsJSON `json:"source"`
	TokenCleared bool              `json:"tokenCleared,omitempty"`
}

type putForgeSourceInput struct {
	Kind    string  `json:"kind"`
	BaseURL *string `json:"baseURL"`
	Token   *string `json:"token"`
}

type testForgeSourceInput struct {
	Kind    string `json:"kind"`
	BaseURL string `json:"baseURL"`
	Project string `json:"project"`
	Token   string `json:"token"`
	Saved   bool   `json:"saved"`
}

func toForgeSettingsJSON(source store.ForgeSource) forgeSettingsJSON {
	return forgeSettingsJSON{
		Name:      source.Name,
		Kind:      source.Kind,
		BaseURL:   source.BaseURL,
		HasToken:  source.HasToken,
		CreatedAt: source.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// values turns the wire patch into the store's pointer arguments. Base URL and
// model are trimmed the way the TUI trims its fields; a key that is only
// whitespace counts as a clear rather than as a credential.
func (in putAISettingsInput) values() (baseURL, model, apiKey *string) {
	return settingsTrimmed(in.BaseURL), settingsTrimmed(in.Model), settingsSecret(in.APIKey)
}

func settingsTrimmed(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func settingsSecret(value *string) *string {
	if value == nil {
		return nil
	}
	secret := *value
	if strings.TrimSpace(secret) == "" {
		secret = ""
	}
	return &secret
}

// --- AI handlers ---

func (s *server) getAISettings(w http.ResponseWriter, _ *http.Request) {
	s.writeAISettings(w, false)
}

// writeAISettings answers with the stored AI settings, shared by the read and
// by the echo a write returns.
func (s *server) writeAISettings(w http.ResponseWriter, keyCleared bool) {
	set, err := s.st.AISettings(s.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, settingsMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, aiSettingsResponse{
		AI:         aiSettingsJSON{BaseURL: set.BaseURL, Model: set.Model, HasKey: set.HasKey},
		KeyCleared: keyCleared,
	})
}

func (s *server) putAISettings(w http.ResponseWriter, r *http.Request) {
	var in putAISettingsInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	baseURL, model, apiKey := in.values()
	// The base URL is validated here rather than left to the store, so a
	// rejected endpoint reads the same as in the TUI, which runs
	// ai.ValidateBaseURL before it saves.
	if baseURL != nil && *baseURL != "" {
		if err := kbai.ValidateBaseURL(*baseURL); err != nil {
			writeError(w, http.StatusBadRequest, settingsMessage(err))
			return
		}
	}
	keyCleared, err := s.st.SetAISettings(s.user, baseURL, model, apiKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, settingsMessage(err))
		return
	}
	s.writeAISettings(w, keyCleared)
}

func (s *server) testAISettings(w http.ResponseWriter, r *http.Request) {
	var in testAISettingsInput
	// The body is optional: a bare POST tests the stored configuration.
	if err := decode(r, &in); err != nil && !errors.Is(err, io.EOF) {
		badBody(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), settingsTestTimeout)
	defer cancel()
	err := aiProbe(ctx, s.st, s.user, kbai.Config{
		BaseURL: strings.TrimSpace(in.BaseURL),
		Model:   strings.TrimSpace(in.Model),
		Key:     in.APIKey,
	})
	if err != nil {
		writeError(w, aiProbeStatus(err), settingsMessage(err, in.APIKey))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// aiProbeStatus keeps the category the AI package already assigned: settings
// the caller can fix stay 4xx, an unreachable or misbehaving endpoint is 502.
func aiProbeStatus(err error) int {
	var probeErr *kbai.Error
	if errors.As(err, &probeErr) && probeErr.Code >= 400 && probeErr.Code <= 599 {
		return probeErr.Code
	}
	return http.StatusBadGateway
}

// --- forge handlers ---

func (s *server) listForgeSources(w http.ResponseWriter, _ *http.Request) {
	sources, err := s.st.ForgeSources(s.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, settingsMessage(err))
		return
	}
	out := make([]forgeSettingsJSON, 0, len(sources))
	for _, source := range sources {
		out = append(out, toForgeSettingsJSON(source))
	}
	writeJSON(w, http.StatusOK, map[string][]forgeSettingsJSON{"sources": out})
}

func (s *server) putForgeSource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var in putForgeSourceInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	tokenCleared, err := s.st.SetForgeSource(s.user, name, strings.TrimSpace(in.Kind), settingsTrimmed(in.BaseURL), settingsSecret(in.Token))
	if err != nil {
		writeError(w, http.StatusBadRequest, settingsMessage(err, settingsValue(in.Token)))
		return
	}
	s.writeForgeSource(w, name, tokenCleared)
}

// writeForgeSource answers with the stored source a write just produced.
func (s *server) writeForgeSource(w http.ResponseWriter, name string, tokenCleared bool) {
	source, err := s.forgeSource(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, settingsMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, forgeSourceResponse{Source: toForgeSettingsJSON(source), TokenCleared: tokenCleared})
}

// forgeSource reads back one stored source. The store has no single-source
// read that skips decrypting the token, so the list is the safe way to echo a
// write.
func (s *server) forgeSource(name string) (store.ForgeSource, error) {
	sources, err := s.st.ForgeSources(s.user)
	if err != nil {
		return store.ForgeSource{}, err
	}
	for _, source := range sources {
		if strings.EqualFold(source.Name, strings.TrimSpace(name)) {
			return source, nil
		}
	}
	return store.ForgeSource{}, errors.New("saved integration unavailable")
}

func (s *server) deleteForgeSource(w http.ResponseWriter, r *http.Request) {
	// Deleting a source that is not there is a successful no-op in the store,
	// so the only failure left is an unusable name.
	if err := s.st.DeleteForgeSource(s.user, r.PathValue("name")); err != nil {
		writeError(w, http.StatusBadRequest, settingsMessage(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) testForgeSource(w http.ResponseWriter, r *http.Request) {
	var in testForgeSourceInput
	// The body is optional: a bare POST tests the saved integration as stored.
	if err := decode(r, &in); err != nil && !errors.Is(err, io.EOF) {
		badBody(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), settingsTestTimeout)
	defer cancel()
	err := forgeProbe(ctx, s.st, s.user, forge.ForgeProbeConfig{
		Name:    r.PathValue("name"),
		Kind:    strings.TrimSpace(in.Kind),
		BaseURL: in.BaseURL,
		Project: in.Project,
		Token:   in.Token,
		Saved:   in.Saved,
	})
	if err != nil {
		writeError(w, forgeProbeStatus(err), settingsMessage(err, in.Token))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forgeProbeStatus separates the two kinds of probe failure the forge package
// reports: everything but its opaque connection failure names a value the
// caller supplied.
func forgeProbeStatus(err error) int {
	if strings.Contains(err.Error(), forgeConnectionFailed) {
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

// --- error text ---

// settingsMessage is the web copy of the TUI's safeSettingsError: it removes
// the credentials the caller just sent, folds the message onto one line
// without control characters, and caps its length. An upstream can echo a
// request back inside its error body, and this response is the one place a
// settings write and its secret meet.
func settingsMessage(err error, secrets ...string) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	for _, secret := range secrets {
		for _, candidate := range []string{secret, strings.TrimSpace(secret)} {
			if candidate != "" {
				message = strings.ReplaceAll(message, candidate, "[redacted]")
			}
		}
	}
	message = sanitizeSettingsMessage(strings.Join(strings.Fields(message), " "))
	if message == "" {
		message = "operation failed"
	}
	// A cut that leaves no mark reads as the whole message, which is how a
	// truncated cause becomes a wrong one. The ellipsis stays inside the cap.
	if utf8.RuneCountInString(message) > settingsMessageLimit {
		message = strings.TrimRight(string([]rune(message)[:settingsMessageLimit-3]), " ") + "..."
	}
	return message
}

// sanitizeSettingsMessage drops escape sequences and control characters, the
// same treatment the TUI gives a message before it draws one: an upstream
// error body is not trusted markup on either surface.
func sanitizeSettingsMessage(value string) string {
	return strings.Map(func(r rune) rune {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, ansi.Strip(value))
}

func settingsValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
