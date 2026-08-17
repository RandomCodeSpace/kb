package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	kbai "github.com/RandomCodeSpace/kb/internal/ai"
)

// AITimeout bounds one upstream chat-completion round trip. It is exported so
// the serve command can keep its HTTP write timeout strictly above it: Go arms
// the write deadline when the request headers are read, not when the handler
// starts writing, so an equal budget lets the proxy consume it all and the
// error response never reaches the client.
const AITimeout = 60 * time.Second

func rejectPrivateAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("non-IP dial address %q", address)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return errors.New("AI endpoint resolves to a private address (set KB_AI_ALLOW_PRIVATE=1 for local model servers)")
	}
	return nil
}

func normalizeGuardHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

func guardedTransport(allowHosts map[string]bool, allowAll bool) *http.Transport {
	normalizedHosts := make(map[string]bool, len(allowHosts))
	for host, allowed := range allowHosts {
		if allowed {
			normalizedHosts[normalizeGuardHost(host)] = true
		}
	}

	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid dial address %q", address)
			}
			allowPrivate := allowAll || normalizedHosts[normalizeGuardHost(host)]
			dialer := &net.Dialer{
				Control: func(network, address string, rawConn syscall.RawConn) error {
					if allowPrivate {
						return nil
					}
					return rejectPrivateAddress(network, address, rawConn)
				},
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

func sameHostRedirect(req *http.Request, via []*http.Request) error {
	origin := via[0].URL
	if origin.User != nil || req.URL.User != nil {
		return errors.New("refusing redirect with URL credentials")
	}
	if !isHTTPURL(origin) || !isHTTPURL(req.URL) {
		return errors.New("refusing redirect to non-HTTP(S) URL")
	}
	if normalizeGuardHost(req.URL.Hostname()) != normalizeGuardHost(origin.Hostname()) || req.URL.Port() != origin.Port() {
		return errors.New("refusing cross-host redirect")
	}
	if strings.EqualFold(origin.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http") {
		return errors.New("refusing HTTPS-to-HTTP redirect")
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

func isHTTPURL(u *url.URL) bool {
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

// newAIClient builds the HTTP client used for user-configured AI endpoints.
// Because the destination is user-controlled, the dialer rejects loopback,
// link-local, private, and ULA addresses — checked on the resolved IP, so
// DNS rebinding cannot bypass it — unless KB_AI_ALLOW_PRIVATE=1 opts in for
// local model servers such as Ollama. Redirects may never change host or
// downgrade an HTTPS connection to HTTP.
func newAIClient() *http.Client {
	allowPrivate := os.Getenv("KB_AI_ALLOW_PRIVATE") == "1"
	return &http.Client{
		Timeout:       AITimeout,
		Transport:     guardedTransport(nil, allowPrivate),
		CheckRedirect: sameHostRedirect,
	}
}

// aiError carries the HTTP status a failed AI call should surface with. Its
// message must never contain the API key.
type aiError struct {
	code int
	msg  string
}

func (e *aiError) Error() string { return e.msg }

// Bounds for POST /api/ai/stories: an ADR is prose, not a payload, and the
// story count is clamped so one request cannot fan out unboundedly.
const (
	maxADRBytes             = 64 << 10 // 64 KiB
	defaultStoryCount       = 8
	maxStoryCount           = 20
	maxImportIssueBodyBytes = 16 << 10
	maxImportCommentBytes   = 1 << 10
	maxImportPackBytes      = 48 << 10
)

// Every run states its own output budget. Omitting it leaves the cap to the
// upstream default, which silently cuts a reply off mid-sentence — so the
// budgets are sized per call site: one card, up to maxStoryCount cards, up to
// maxImportIssues card proposals, one sentence of drift prose.
//
// No budget may exceed aiMaxTokensCeiling, which skillBudget enforces on every
// run rather than trusting the constants below. A model is only known by the name
// the user typed, and asking for more than the model's own completion cap can
// be rejected outright by strict servers — a 400 no caller can act on, where
// the previous omission worked. The ceiling is 8192 rather than the safer
// 4096: reasoning models (qwen3+, deepseek-r1 family) spend their thinking
// tokens inside this budget, and measured against qwen3.5 a modest ADR split
// burns ~3,600 completion tokens of which under a tenth is the JSON reply, so
// 4096 makes the flagship flow fail on exactly the models it targets. Hitting
// the budget is still detected from finish_reason and reported as truncation
// the caller can act on; models capped below 8192 on strict servers fail the
// larger flows either way.
const (
	aiStoryMaxTokens   = 4096
	aiStoriesMaxTokens = 8192
	aiImportMaxTokens  = 8192
	aiDriftMaxTokens   = 1024
	aiMaxTokensCeiling = 8192
)

// storyDraft is the coerced card draft returned by POST /api/ai/story.
type storyDraft = kbai.Draft
type draftCheck = kbai.DraftCheck

// aiEndpoint validates the configured base URL and returns the API root the
// client appends chat/completions to, accepting bases with or without a
// trailing /v1 (and trailing slashes). The trailing slash matters: method
// paths are resolved against this URL, and a base without one would lose its
// last segment.
func aiEndpoint(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" {
		return "", errors.New("invalid AI base URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("AI base URL scheme must be http or https")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("AI base URL must not contain query or fragment")
	}
	// The base URL is stored unencrypted and echoed back by GET /api/settings,
	// so a credential in the userinfo would be a key leaked to the browser —
	// the one thing the server-side AI proxy exists to prevent. Keys go in the
	// key field, which is encrypted at rest and never returned.
	if u.User != nil {
		return "", errors.New("AI base URL must not contain a username or password — put the key in the API key field")
	}
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/v1") {
		p += "/v1"
	}
	u.Path = p + "/"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// aiConfig is one resolved AI endpoint configuration: where a chat completion
// goes and what it authenticates with. It is assembled per request and never
// written back, so POST /api/ai/test can build one from form values the user
// has not saved.
type aiConfig struct {
	baseURL string
	model   string
	key     string
}

// storedAIConfig loads the user's saved AI configuration.
func (s *server) storedAIConfig(user string) (aiConfig, error) {
	set, err := s.store.AISettings(user)
	if err != nil {
		log.Printf("ai: settings for %s: %v", user, err)
		return aiConfig{}, &aiError{http.StatusInternalServerError, storageErrorMessage}
	}
	key, err := s.store.AIKey(user)
	if err != nil {
		log.Printf("ai: key for %s: %v", user, err)
		return aiConfig{}, &aiError{http.StatusInternalServerError, storageErrorMessage}
	}
	return aiConfig{baseURL: set.BaseURL, model: set.Model, key: key}, nil
}

const truncatedReplyMessage = "the model's reply hit the output limit and was cut off — ask for less in one request"

// usesMaxCompletionTokens reports whether the model needs the budget under
// max_completion_tokens. max_tokens is deprecated and rejected outright by the
// o-series and gpt-5 reasoning models, while many OpenAI-compatible servers
// only ever learned max_tokens — so the field is chosen by model name, which
// is all the server knows about the endpoint. Any vendor prefix ("openai/o3")
// is stripped first.
func usesMaxCompletionTokens(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if strings.HasPrefix(name, "gpt-5") {
		return true
	}
	return reasoningSeriesRe.MatchString(name)
}

// reasoningSeriesRe matches the o-series model names (o1, o3-mini, o4-mini and
// their dated variants) without matching unrelated names that merely start
// with an o.
var reasoningSeriesRe = regexp.MustCompile(`^o\d+(-|$)`)

// aiTestRequest is the optional body of POST /api/ai/test: the values the
// user currently has in the settings form, which may not be saved yet. It is
// used for that one request and never written to the store — testing a key
// must not be a way to persist an unvalidated one.
type aiTestRequest struct {
	BaseURL string `json:"ai_base_url"`
	Model   string `json:"ai_model"`
	Key     string `json:"ai_key"`
}

// merge overlays the supplied values onto the stored ones. A blank field
// means "keep what is stored", which is what lets the form test a new model
// against the already-saved key without the user retyping it — the key is
// write-only in the settings response, so the form cannot send it back.
// Whether the stored key may travel to a supplied base URL at all is
// runAITest's decision, made before this is called.
func (c aiConfig) merge(req aiTestRequest) aiConfig {
	if v := strings.TrimSpace(req.BaseURL); v != "" {
		c.baseURL = v
	}
	if v := strings.TrimSpace(req.Model); v != "" {
		c.model = v
	}
	if v := strings.TrimSpace(req.Key); v != "" {
		c.key = v
	}
	return c
}

// toolCallRequiredMessage is what a reachable endpoint whose model cannot call
// tools reports. Tool calling is a prerequisite, not a nice-to-have: the AI
// features are built on it, so a model that only produces prose fails the test
// rather than passing it and failing later on real work.
const toolCallRequiredMessage = "model must support tool calling"

// handleAITest runs the tool-call probe and reports the result; failures come
// back as ok:false, not error statuses. With no body it tests the saved
// settings; with an aiTestRequest body it tests those values instead, so the
// form can be validated before it is saved. Either way the probe goes through
// the same SSRF-guarded client, timeout and opaque errors: every upstream
// outcome (connect failure, non-2xx status, bad body)
// collapses to one message so a supplied URL cannot turn the endpoint into a
// host/port reachability oracle; the detail goes to the server log only. That
// is also why an endpoint that rejects the tools field reports the opaque
// failure and not toolCallRequiredMessage — distinguishing "reachable but
// refused" from "unreachable" is exactly the oracle. Configuration errors
// (no/invalid base URL) and a reply without the tool call pass through
// unchanged, because both describe the caller's own settings.
func (s *server) handleAITest(w http.ResponseWriter, r *http.Request, user string) {
	type result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req aiTestRequest
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
			return
		}
	}
	if err := s.runAITest(user, req); err != nil {
		msg := err.Error()
		var ae *aiError
		if errors.As(err, &ae) && ae.code == http.StatusBadGateway {
			log.Printf("ai: test for %s failed: %s", user, ae.msg)
			msg = connectionFailedMessage
		}
		writeJSON(w, result{Error: msg})
		return
	}
	writeJSON(w, result{OK: true})
}

// runAITest resolves one connection test — the stored configuration overlaid
// with whatever the caller supplied — and probes it with a tool call. Nothing
// is written back, so a key supplied only for the test lives no longer than
// this call.
//
// The stored key may only be used against the stored origin. Store.
// SetAISettings enforces exactly that on save (a base-URL host change without
// a key in the same call clears the stored key), and a test that skipped the
// rule would be the same leak by another door: a blank key field is the
// normal state of the form, so anyone who can reach this endpoint could
// otherwise point it at a host they control and be handed the decrypted key —
// without writing anything, so without leaving a trace. The SSRF guard does
// not help here; it only blocks private targets.
func (s *server) runAITest(user string, req aiTestRequest) error {
	err := s.aiRunner().Probe(context.Background(), user, kbai.Config{BaseURL: req.BaseURL, Model: req.Model, Key: req.Key})
	return serverAIError(err)
}

// storyDraftSkillName is the skill POST /api/ai/story runs. The endpoint
// predates the skill runner and keeps its own request and response shape: one
// request yields one card, written flat.
const storyDraftSkillName = "story-draft"

// noCardProposedMessage is the run that finished without ever calling
// propose_card. The endpoint owes the caller a card, so an empty run is an
// upstream failure — and writeAIError keeps 502 opaque, so the reason is
// logged rather than returned.
const noCardProposedMessage = "assistant proposed no card"

// handleAIStory drafts one card from a request by running the story-draft
// skill. The card comes from propose_card, never from parsing the reply, and
// it is written flat: the frozen UI decodes the response as a draft object, not
// as a run result. The prompt and the current card are never stored.
func (s *server) handleAIStory(w http.ResponseWriter, r *http.Request, user string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		Mode   string          `json:"mode"`
		Prompt string          `json:"prompt"`
		Task   json.RawMessage `json:"task"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
		return
	}
	if req.Mode != "create" && req.Mode != "update" {
		http.Error(w, `mode must be "create" or "update"`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "prompt required", http.StatusBadRequest)
		return
	}
	userMsg := "Create a new kanban card for this request:\n" + req.Prompt
	if req.Mode == "update" {
		userMsg = "Update the kanban card according to this request:\n" + req.Prompt
		if len(req.Task) > 0 && string(req.Task) != "null" {
			userMsg += "\n\nCurrent card JSON:\n" + string(req.Task)
		}
	}
	// One card, so the collector caps the run at one. The run is read-only:
	// the card being updated is board text, and a draft the user has yet to
	// accept is no reason to hand the loop a board write or an outbound fetch.
	run, err := s.runSkillForRequest(w, r, user, skillScopeReadOnly, storyDraftSkillName, userMsg, 1, aiStoryMaxTokens)
	if err != nil {
		writeAIError(w, user, "story", err)
		return
	}
	if len(run.Cards) == 0 {
		writeAIError(w, user, "story", &aiError{http.StatusBadGateway, noCardProposedMessage})
		return
	}
	writeJSON(w, run.Cards[0])
}

// writeAIError renders a failed AI call. Every upstream outcome
// collapses to one opaque message, matching handleAITest: err.Error() names
// the connect failure or the upstream status, which turns the endpoint into
// a host/port reachability oracle for any page that reaches it. The detail
// goes to the server log only. Configuration errors (400: no or invalid base
// URL) describe the caller's own settings and pass through.
func writeAIError(w http.ResponseWriter, user, op string, err error) {
	code, msg := http.StatusBadGateway, connectionFailedMessage
	var ae *aiError
	if errors.As(err, &ae) {
		code = ae.code
		if code != http.StatusBadGateway {
			msg = ae.msg
		}
	}
	if code == http.StatusBadGateway {
		log.Printf("ai: %s for %s failed: %v", op, user, err)
	}
	http.Error(w, msg, code)
}

type aiStoriesRequest struct {
	ADR    string `json:"adr"`
	URL    string `json:"url"`
	Source string `json:"source"`
	Max    int    `json:"max"`
}

type aiStoriesInput struct {
	adr      string
	link     string
	issueURL string
}

func decodeAIStoriesRequest(w http.ResponseWriter, r *http.Request) (aiStoriesRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req aiStoriesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
		return aiStoriesRequest{}, false
	}
	hasADR := strings.TrimSpace(req.ADR) != ""
	hasURL := strings.TrimSpace(req.URL) != ""
	if hasADR == hasURL {
		http.Error(w, "provide adr or url", http.StatusBadRequest)
		return aiStoriesRequest{}, false
	}
	if hasADR && len(req.ADR) > maxADRBytes {
		http.Error(w, "adr too large (max 64 KiB)", http.StatusRequestEntityTooLarge)
		return aiStoriesRequest{}, false
	}
	return req, true
}

func (s *server) resolveAIStoriesInput(w http.ResponseWriter, r *http.Request, user string, req aiStoriesRequest) (aiStoriesInput, bool) {
	if strings.TrimSpace(req.ADR) != "" {
		return aiStoriesInput{adr: req.ADR}, true
	}

	sources, err := s.store.ForgeSources(user)
	if err != nil {
		log.Printf("forge: list sources for stories for %s failed", user)
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
		return aiStoriesInput{}, false
	}
	ref, err := parseForgeRef(sources, req.Source, req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return aiStoriesInput{}, false
	}
	selected, found := forgeSourceByName(sources, req.Source)
	if !found {
		http.Error(w, configuredSourceUnavailableMessage, http.StatusBadRequest)
		return aiStoriesInput{}, false
	}
	if ref.Source.Name != selected.Name {
		http.Error(w, "reference does not match selected source", http.StatusBadRequest)
		return aiStoriesInput{}, false
	}
	kind, baseURL, pat, err := s.store.ForgePAT(user, selected.Name)
	if err != nil || kind != selected.Kind || baseURL != selected.BaseURL {
		http.Error(w, configuredSourceUnavailableMessage, http.StatusBadRequest)
		return aiStoriesInput{}, false
	}
	ref.pat = pat
	ctx, cancel := context.WithTimeout(r.Context(), importFetchTimeout)
	defer cancel()
	issue, err := s.fetchIssue(ctx, ref)
	if err != nil {
		writeAIError(w, user, "stories", err)
		return aiStoriesInput{}, false
	}
	link, _ := importIssueProvenance(ref, issue)
	return aiStoriesInput{adr: forgeIssueADR(issue), link: link, issueURL: issue.URL}, true
}

// adrSplitSkillName is the skill /api/ai/stories runs. The endpoint predates
// the skill runner and keeps its own request and response shape; only the way
// the drafts are produced is shared.
const adrSplitSkillName = "adr-split"

// handleAIStories splits one ADR into several card drafts by running the
// adr-split skill. The drafts come from propose_card, not from parsing the
// reply, so the story count is never stated in the prompt: a count in the
// prompt anchors the model to produce exactly that many. The skill's closing
// commentary is dropped, because this endpoint's response shape is fixed.
// The ADR text itself is never stored.
func (s *server) handleAIStories(w http.ResponseWriter, r *http.Request, user string) {
	req, ok := decodeAIStoriesRequest(w, r)
	if !ok {
		return
	}
	input, ok := s.resolveAIStoriesInput(w, r, user, req)
	if !ok {
		return
	}
	// The cap is clamped inside the run, where the collector enforces it. The
	// run is read-only: the document may be a forge issue with third-party
	// comments, so the loop that reads it gets no board write and no outbound
	// fetch.
	run, err := s.runSkillForRequest(w, r, user, skillScopeReadOnly, adrSplitSkillName, input.adr, req.Max, aiStoriesMaxTokens)
	if err != nil {
		writeAIError(w, user, "stories", err)
		return
	}
	stories := run.Cards
	if input.link != "" {
		for i := range stories {
			stories[i].Tags = append(stripModelLinkTags(stories[i].Tags), linkTagPrefix+input.link)
		}
	}
	writeJSON(w, struct {
		Stories []storyDraft `json:"stories"`
		Link    string       `json:"link,omitempty"`
		URL     string       `json:"url,omitempty"`
	}{Stories: stories, Link: input.link, URL: input.issueURL})
}

// forgeIssueADR turns one fetched issue and its bounded human discussion into
// the ADR text sent to the existing splitter. Discussion yields first when the
// input reaches maxADRBytes; if title and body alone exceed it, truncation is
// still rune-safe and leaves the prompt within the same existing limit.
func forgeIssueADR(issue forgeIssue) string {
	adr := fmt.Sprintf("# %s\n\n%s", issue.Title, issue.Body)
	discussion := "\n\n## Discussion"
	if len(adr)+len(discussion) > maxADRBytes {
		return truncateImportText(adr, maxADRBytes)
	}
	adr += discussion
	for _, comment := range issue.Comments {
		item := "\n- " + comment
		remaining := maxADRBytes - len(adr)
		if len(item) > remaining {
			return adr + truncateImportText(item, remaining)
		}
		adr += item
	}
	return adr
}

// packImportIssues limits raw forge text before one model call while keeping
// source numbers stable for server-owned provenance after coercion.
func packImportIssues(issues []forgeIssue) (string, int) {
	var packed strings.Builder
	count := 0
	for i, issue := range issues {
		if packed.Len() >= maxImportPackBytes {
			break
		}
		comments := make([]string, 0, min(len(issue.Comments), 10))
		for _, comment := range issue.Comments {
			if len(comments) == 10 {
				break
			}
			comments = append(comments, truncateImportText(comment, maxImportCommentBytes))
		}
		section := fmt.Sprintf("Source %d\nTitle: %s\nRef: %s\nLabels: %s\nBody:\n%s\nComments:\n%s\n\n",
			i+1, issue.Title, issue.Ref, strings.Join(issue.Labels, ", "),
			truncateImportText(issue.Body, maxImportIssueBodyBytes), strings.Join(comments, "\n"))
		if len(section) > maxImportPackBytes-packed.Len() {
			break
		}
		packed.WriteString(section)
		count++
	}
	return packed.String(), count
}

func truncateImportText(text string, max int) string {
	if max <= 0 {
		return ""
	}
	for len(text) > max {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return text
}

// logSafe makes an untrusted value safe to interpolate into a log line. The
// CR/LF removal is spelled out as explicit replacements rather than folded into
// stripControl's strings.Map because static analysis recognizes the former as a
// line-break barrier and cannot see through the latter's closure.
func logSafe(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return stripControl(s)
}

// stripControl removes every control character, including the CR/LF that
// would break a value out of its markdown line.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// stripControlKeepLines is stripControl for multi-line values: newlines are
// kept (the serializer indents each one), tabs and everything else are not.
func stripControlKeepLines(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}
