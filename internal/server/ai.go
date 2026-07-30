package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
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
	if req.URL.Host != via[0].URL.Host {
		return errors.New("refusing cross-host redirect")
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// newAIClient builds the HTTP client used for user-configured AI endpoints.
// Because the destination is user-controlled, the dialer rejects loopback,
// link-local, private, and ULA addresses — checked on the resolved IP, so
// DNS rebinding cannot bypass it — unless KB_AI_ALLOW_PRIVATE=1 opts in for
// local model servers such as Ollama. Redirects may never change host.
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

// storySystemPrompt forces a strict-JSON reply in the draft shape.
const storySystemPrompt = `You write kanban cards. Respond with a single JSON object only — no prose, no code fences — using exactly these keys:
{"title":"short imperative title","emoji":"🚀","desc":"markdown description","prio":3,"due":"YYYY-MM-DD or empty string","effort":"S, M, L or empty string","tags":["tag"],"checks":[{"text":"step","done":false}]}
prio is an integer from 1 (highest) to 4 (lowest); 3 is the default. emoji is exactly one emoji character that suits the work, or an empty string when nothing fits.`

// storiesSystemPrompt asks for the same card shape, many at a time, wrapped
// in a {"stories": [...]} object because json_object mode forbids a bare
// top-level array.
const storiesSystemPrompt = `You split an architecture decision record into kanban cards. Respond with a single JSON object only — no prose, no code fences — of the form:
{"stories":[{"title":"short imperative title","emoji":"🚀","desc":"markdown description","prio":3,"due":"YYYY-MM-DD or empty string","effort":"S, M, L or empty string","tags":["tag"],"checks":[{"text":"step","done":false}]}]}
prio is an integer from 1 (highest) to 4 (lowest); 3 is the default. emoji is exactly one emoji character that suits the work, or an empty string when nothing fits. Each story must be independently deliverable. Tags are single words with no spaces.`

const importSystemPrompt = `You transform forge issues into kanban-card proposals. Respond with a single JSON object only — no prose, no code fences — of the form:
{"stories":[{"title":"short imperative title","emoji":"🚀","desc":"markdown description","prio":3,"due":"YYYY-MM-DD or empty string","effort":"S, M, L or empty string","tags":["tag"],"checks":[{"text":"step","done":false}],"source":1}]}
emoji is exactly one emoji character that suits the work, or an empty string when nothing fits. source must be the positive integer Source number from the supplied forge issue. Do not copy source text verbatim; propose independently actionable work.`

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

// storyDraft is the coerced card draft returned by POST /api/ai/story.
type storyDraft struct {
	Title  string       `json:"title"`
	Emoji  string       `json:"emoji"`
	Desc   string       `json:"desc"`
	Prio   int          `json:"prio"`
	Due    string       `json:"due"`
	Effort string       `json:"effort"`
	Tags   []string     `json:"tags"`
	Checks []draftCheck `json:"checks"`
	Source int          `json:"-"`
}

type draftCheck struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

// aiEndpoint validates the configured base URL and joins it with
// /chat/completions, accepting bases with or without a trailing /v1 (and
// trailing slashes).
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
	u.Path = p + "/chat/completions"
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
		return aiConfig{}, &aiError{http.StatusInternalServerError, "storage error"}
	}
	key, err := s.store.AIKey(user)
	if err != nil {
		log.Printf("ai: key for %s: %v", user, err)
		return aiConfig{}, &aiError{http.StatusInternalServerError, "storage error"}
	}
	return aiConfig{baseURL: set.BaseURL, model: set.Model, key: key}, nil
}

// chatCompletion runs one chat completion against the user's configured
// endpoint and returns the assistant content. Errors are *aiError with a
// key-free message: 400 for configuration problems, 502 for upstream ones.
func (s *server) chatCompletion(user string, msgs []chatMessage, maxTokens int, jsonMode bool) (string, error) {
	cfg, err := s.storedAIConfig(user)
	if err != nil {
		return "", err
	}
	return s.chat(user, cfg, msgs, maxTokens, jsonMode)
}

// chat performs one chat completion against cfg. Every caller goes through
// here, so the SSRF-guarded client, the timeout and the key-free error
// mapping apply to a supplied configuration exactly as to a stored one; user
// is for logging only.
func (s *server) chat(user string, cfg aiConfig, msgs []chatMessage, maxTokens int, jsonMode bool) (string, error) {
	if strings.TrimSpace(cfg.baseURL) == "" {
		return "", &aiError{http.StatusBadRequest, "AI base URL not configured"}
	}
	endpoint, err := aiEndpoint(cfg.baseURL)
	if err != nil {
		return "", &aiError{http.StatusBadRequest, err.Error()}
	}
	payload := chatRequest{Model: cfg.model, Messages: msgs, MaxTokens: maxTokens}
	if jsonMode {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", &aiError{http.StatusInternalServerError, "encode request"}
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return "", &aiError{http.StatusBadRequest, "invalid AI endpoint"}
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.key != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.key)
	}
	resp, err := s.aiClient.Do(req)
	if err != nil {
		log.Printf("ai: request for %s failed: %v", user, err)
		return "", &aiError{http.StatusBadGateway, "upstream request failed"}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", &aiError{http.StatusBadGateway, "upstream read failed"}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &aiError{http.StatusBadGateway, fmt.Sprintf("upstream returned status %d", resp.StatusCode)}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", &aiError{http.StatusBadGateway, "upstream returned invalid JSON"}
	}
	if len(out.Choices) == 0 {
		return "", &aiError{http.StatusBadGateway, "upstream returned no choices"}
	}
	return out.Choices[0].Message.Content, nil
}

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

// handleAITest runs a 1-token completion and reports reachability; failures
// come back as ok:false, not error statuses. With no body it tests the saved
// settings; with an aiTestRequest body it tests those values instead, so the
// form can be validated before it is saved. Either way the request goes
// through chat, which means the same SSRF-guarded client, timeout and opaque
// errors: every upstream outcome (connect failure, non-2xx status, bad body)
// collapses to one message so a supplied URL cannot turn the endpoint into a
// host/port reachability oracle; the detail goes to the server log only.
// Configuration errors (no/invalid base URL) pass through unchanged.
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
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	if err := s.runAITest(user, req); err != nil {
		msg := err.Error()
		var ae *aiError
		if errors.As(err, &ae) && ae.code == http.StatusBadGateway {
			log.Printf("ai: test for %s failed: %s", user, ae.msg)
			msg = "connection failed"
		}
		writeJSON(w, result{Error: msg})
		return
	}
	writeJSON(w, result{OK: true})
}

// runAITest resolves one connection test — the stored configuration overlaid
// with whatever the caller supplied — and pings it. Nothing is written back,
// so a key supplied only for the test lives no longer than this call.
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
	cfg, err := s.storedAIConfig(user)
	if err != nil {
		return err
	}
	supplied := strings.TrimSpace(req.BaseURL)
	if strings.TrimSpace(req.Key) == "" && supplied != "" && cfg.key != "" &&
		!store.SameAIOrigin(cfg.baseURL, supplied) {
		return &aiError{http.StatusBadRequest, "enter the API key to test a different endpoint"}
	}
	_, err = s.chat(user, cfg.merge(req), []chatMessage{{Role: "user", Content: "ping"}}, 1, false)
	return err
}

func (s *server) handleAIStory(w http.ResponseWriter, r *http.Request, user string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		Mode   string          `json:"mode"`
		Prompt string          `json:"prompt"`
		Task   json.RawMessage `json:"task"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
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
	msgs := []chatMessage{
		{Role: "system", Content: storySystemPrompt},
		{Role: "user", Content: userMsg},
	}
	content, err := s.chatCompletion(user, msgs, 0, true)
	if err != nil {
		writeAIError(w, user, "story", err)
		return
	}
	draft, err := coerceDraft(content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, draft)
}

// writeAIError renders a chatCompletion failure. Every upstream outcome
// collapses to one opaque message, matching handleAITest: err.Error() names
// the connect failure or the upstream status, which turns the endpoint into
// a host/port reachability oracle for any page that reaches it. The detail
// goes to the server log only. Configuration errors (400: no or invalid base
// URL) describe the caller's own settings and pass through.
func writeAIError(w http.ResponseWriter, user, op string, err error) {
	code, msg := http.StatusBadGateway, "connection failed"
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

// handleAIStories splits one ADR into several card drafts. It shares the
// proxy client, timeout, SSRF guard and opaque error mapping with
// /api/ai/story — there is deliberately no second HTTP path — and the ADR
// text itself is never stored.
func (s *server) handleAIStories(w http.ResponseWriter, r *http.Request, user string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		ADR    string `json:"adr"`
		URL    string `json:"url"`
		Source string `json:"source"`
		Max    int    `json:"max"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	hasADR := strings.TrimSpace(req.ADR) != ""
	hasURL := strings.TrimSpace(req.URL) != ""
	if hasADR == hasURL {
		http.Error(w, "provide adr or url", http.StatusBadRequest)
		return
	}
	if hasADR && len(req.ADR) > maxADRBytes {
		http.Error(w, "adr too large (max 64 KiB)", http.StatusRequestEntityTooLarge)
		return
	}
	adr, link, issueURL := req.ADR, "", ""
	if hasURL {
		sources, err := s.store.ForgeSources(user)
		if err != nil {
			log.Printf("forge: list sources for stories for %s failed", user)
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		ref, err := parseForgeRef(sources, req.Source, req.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		selected, found := forgeSourceByName(sources, req.Source)
		if !found {
			http.Error(w, "configured source unavailable", http.StatusBadRequest)
			return
		}
		if ref.Source.Name != selected.Name {
			http.Error(w, "reference does not match selected source", http.StatusBadRequest)
			return
		}
		kind, baseURL, pat, err := s.store.ForgePAT(user, selected.Name)
		if err != nil || kind != selected.Kind || baseURL != selected.BaseURL {
			http.Error(w, "configured source unavailable", http.StatusBadRequest)
			return
		}
		ref.pat = pat
		ctx, cancel := context.WithTimeout(r.Context(), importFetchTimeout)
		defer cancel()
		issue, err := s.fetchIssue(ctx, ref)
		if err != nil {
			writeAIError(w, user, "stories", err)
			return
		}
		adr = forgeIssueADR(issue)
		link, _ = importIssueProvenance(ref, issue)
		issueURL = issue.URL
	}
	max := req.Max
	if max == 0 {
		max = defaultStoryCount
	}
	if max < 1 {
		max = 1
	}
	if max > maxStoryCount {
		max = maxStoryCount
	}
	msgs := []chatMessage{
		{Role: "system", Content: storiesSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Split this ADR into at most %d stories:\n\n%s", max, adr)},
	}
	content, err := s.chatCompletion(user, msgs, 0, true)
	if err != nil {
		writeAIError(w, user, "stories", err)
		return
	}
	stories, err := coerceDrafts(content, max)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if link != "" {
		for i := range stories {
			stories[i].Tags = append(stripModelLinkTags(stories[i].Tags), "link::"+link)
		}
	}
	writeJSON(w, struct {
		Stories []storyDraft `json:"stories"`
		Link    string       `json:"link,omitempty"`
		URL     string       `json:"url,omitempty"`
	}{Stories: stories, Link: link, URL: issueURL})
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

var draftDueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// decodeJSONObject parses assistant content as a single JSON object,
// tolerating stray prose or code fences around it.
func decodeJSONObject(content string) (map[string]any, error) {
	raw := strings.TrimSpace(content)
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
		if start < 0 || end <= start {
			return nil, errors.New("assistant reply is not JSON")
		}
		if err := json.Unmarshal([]byte(raw[start:end+1]), &m); err != nil {
			return nil, errors.New("assistant reply is not valid JSON")
		}
	}
	return m, nil
}

// coerceDraft parses assistant content as JSON and clamps every field into
// the draft contract: prio 1-4, effort S/M/L or empty, due YYYY-MM-DD or
// empty, tags as non-empty strings, checks as {text,done}.
func coerceDraft(content string) (storyDraft, error) {
	m, err := decodeJSONObject(content)
	if err != nil {
		return storyDraft{}, err
	}
	return coerceDraftMap(m), nil
}

// coerceDrafts reads a {"stories":[...]} reply into at most max drafts. The
// upstream reply is untrusted, so anything that is not an object, or that
// cannot be made wire-safe (validateDraft), is dropped rather than returned.
func coerceDrafts(content string, max int) ([]storyDraft, error) {
	m, err := decodeJSONObject(content)
	if err != nil {
		return nil, err
	}
	vs, ok := m["stories"].([]any)
	if !ok {
		return nil, errors.New("assistant reply has no stories array")
	}
	out := []storyDraft{}
	for _, v := range vs {
		if len(out) >= max {
			break
		}
		sm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		d := coerceDraftMap(sm)
		if source, ok := sm["source"].(float64); ok && source >= 1 && source == float64(int(source)) {
			d.Source = int(source)
		}
		if err := validateDraft(d); err != nil {
			// Deliberately without the error: validation quotes the offending
			// field value, and that value is card text derived from the
			// user's document. Board content never reaches the log.
			log.Print("ai: dropping a story the assistant returned unusable")
			continue
		}
		out = append(out, d)
	}
	return out, nil
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

// validateDraft runs a coerced draft through the same field rules every
// other write path uses, so a hostile reply cannot reach the board even if
// coerceDraftMap ever loosens. A draft that fails is dropped, not repaired.
func validateDraft(d storyDraft) error {
	return store.ValidateTaskFields(board.Task{
		Title:  d.Title,
		Emoji:  d.Emoji,
		Desc:   d.Desc,
		Due:    d.Due,
		Effort: d.Effort,
		Prio:   d.Prio,
		Tags:   d.Tags,
	})
}

// coerceDraftMap clamps one decoded object into the draft contract. Every
// string that lands on a single markdown line is stripped of control
// characters first: an upstream reply is untrusted, and a title of
// "x\n- [x] forged !1" would otherwise serialize as an extra board line.
func coerceDraftMap(m map[string]any) storyDraft {
	d := storyDraft{Prio: 3, Tags: []string{}, Checks: []draftCheck{}}
	if v, ok := m["title"].(string); ok {
		d.Title = strings.TrimSpace(stripControl(v))
	}
	if v, ok := m["emoji"].(string); ok {
		d.Emoji = board.LeadingEmoji(strings.TrimSpace(v))
	}
	if v, ok := m["desc"].(string); ok {
		// Descriptions are serialized as indented lines, so newlines survive
		// the wire; every other control character is still stripped.
		d.Desc = stripControlKeepLines(v)
	}
	switch v := m["prio"].(type) {
	case float64:
		d.Prio = clampPrio(int(v))
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			d.Prio = clampPrio(n)
		}
	}
	if v, ok := m["due"].(string); ok {
		due := strings.TrimSpace(v)
		// Shape and calendar validity both: "2026-13-45" matches the pattern
		// but is not a date, and every write path rejects it.
		if draftDueRe.MatchString(due) {
			if _, err := time.Parse("2006-01-02", due); err == nil {
				d.Due = due
			}
		}
	}
	if v, ok := m["effort"].(string); ok {
		switch e := strings.ToUpper(strings.TrimSpace(v)); e {
		case "S", "M", "L":
			d.Effort = e
		}
	}
	if vs, ok := m["tags"].([]any); ok {
		for _, v := range vs {
			tag, ok := v.(string)
			if !ok {
				continue
			}
			// A tag is one wire token: no whitespace (a space would start a
			// second token) and no leading '#' (already the tag sigil).
			tag = strings.TrimSpace(stripControl(tag))
			if tag == "" || board.ContainsSpace(tag) || tag[0] == '#' {
				continue
			}
			d.Tags = append(d.Tags, tag)
		}
	}
	if vs, ok := m["checks"].([]any); ok {
		for _, v := range vs {
			cm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			text, _ := cm["text"].(string)
			if text = strings.TrimSpace(stripControl(text)); text == "" {
				continue
			}
			done, _ := cm["done"].(bool)
			d.Checks = append(d.Checks, draftCheck{Text: text, Done: done})
		}
	}
	return d
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

func clampPrio(n int) int {
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}
