package server

import (
	"bytes"
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

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// AITimeout bounds one upstream chat-completion round trip. It is exported so
// the serve command can keep its HTTP write timeout strictly above it: Go arms
// the write deadline when the request headers are read, not when the handler
// starts writing, so an equal budget lets the proxy consume it all and the
// error response never reaches the client.
const AITimeout = 60 * time.Second

// newAIClient builds the HTTP client used for user-configured AI endpoints.
// Because the destination is user-controlled, the dialer rejects loopback,
// link-local, private, and ULA addresses — checked on the resolved IP, so
// DNS rebinding cannot bypass it — unless KB_AI_ALLOW_PRIVATE=1 opts in for
// local model servers such as Ollama. Redirects may never change host.
func newAIClient() *http.Client {
	allowPrivate := os.Getenv("KB_AI_ALLOW_PRIVATE") == "1"
	dialer := &net.Dialer{
		Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
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
		},
	}
	return &http.Client{
		Timeout:   AITimeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != via[0].URL.Host {
				return errors.New("refusing cross-host redirect")
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
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
{"title":"short imperative title","desc":"markdown description","prio":3,"due":"YYYY-MM-DD or empty string","effort":"S, M, L or empty string","tags":["tag"],"checks":[{"text":"step","done":false}]}
prio is an integer from 1 (highest) to 4 (lowest); 3 is the default.`

// storiesSystemPrompt asks for the same card shape, many at a time, wrapped
// in a {"stories": [...]} object because json_object mode forbids a bare
// top-level array.
const storiesSystemPrompt = `You split an architecture decision record into kanban cards. Respond with a single JSON object only — no prose, no code fences — of the form:
{"stories":[{"title":"short imperative title","desc":"markdown description","prio":3,"due":"YYYY-MM-DD or empty string","effort":"S, M, L or empty string","tags":["tag"],"checks":[{"text":"step","done":false}]}]}
prio is an integer from 1 (highest) to 4 (lowest); 3 is the default. Each story must be independently deliverable. Tags are single words with no spaces.`

// Bounds for POST /api/ai/stories: an ADR is prose, not a payload, and the
// story count is clamped so one request cannot fan out unboundedly.
const (
	maxADRBytes       = 64 << 10 // 64 KiB
	defaultStoryCount = 8
	maxStoryCount     = 20
)

// storyDraft is the coerced card draft returned by POST /api/ai/story.
type storyDraft struct {
	Title  string       `json:"title"`
	Desc   string       `json:"desc"`
	Prio   int          `json:"prio"`
	Due    string       `json:"due"`
	Effort string       `json:"effort"`
	Tags   []string     `json:"tags"`
	Checks []draftCheck `json:"checks"`
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
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/v1") {
		p += "/v1"
	}
	u.Path = p + "/chat/completions"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// chatCompletion runs one chat completion against the user's configured
// endpoint and returns the assistant content. Errors are *aiError with a
// key-free message: 400 for configuration problems, 502 for upstream ones.
func (s *server) chatCompletion(user string, msgs []chatMessage, maxTokens int, jsonMode bool) (string, error) {
	set, err := s.store.AISettings(user)
	if err != nil {
		log.Printf("ai: settings for %s: %v", user, err)
		return "", &aiError{http.StatusInternalServerError, "storage error"}
	}
	if strings.TrimSpace(set.BaseURL) == "" {
		return "", &aiError{http.StatusBadRequest, "AI base URL not configured"}
	}
	endpoint, err := aiEndpoint(set.BaseURL)
	if err != nil {
		return "", &aiError{http.StatusBadRequest, err.Error()}
	}
	key, err := s.store.AIKey(user)
	if err != nil {
		log.Printf("ai: key for %s: %v", user, err)
		return "", &aiError{http.StatusInternalServerError, "storage error"}
	}
	payload := chatRequest{Model: set.Model, Messages: msgs, MaxTokens: maxTokens}
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
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
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

// handleAITest runs a 1-token completion against the configured endpoint and
// reports reachability; failures come back as ok:false, not error statuses.
// Every upstream outcome (connect failure, non-2xx status, bad body)
// collapses to one opaque message so the endpoint cannot be used as a
// host/port reachability oracle; the detail goes to the server log only.
// Configuration errors (no/invalid base URL) pass through unchanged.
func (s *server) handleAITest(w http.ResponseWriter, _ *http.Request, user string) {
	type result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if _, err := s.chatCompletion(user, []chatMessage{{Role: "user", Content: "ping"}}, 1, false); err != nil {
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
		ADR string `json:"adr"`
		Max int    `json:"max"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(req.ADR) > maxADRBytes {
		http.Error(w, "adr too large (max 64 KiB)", http.StatusRequestEntityTooLarge)
		return
	}
	if strings.TrimSpace(req.ADR) == "" {
		http.Error(w, "adr required", http.StatusBadRequest)
		return
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
		{Role: "user", Content: fmt.Sprintf("Split this ADR into at most %d stories:\n\n%s", max, req.ADR)},
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
	writeJSON(w, map[string][]storyDraft{"stories": stories})
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
		if err := validateDraft(d); err != nil {
			log.Printf("ai: dropping unusable story: %v", err)
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// validateDraft runs a coerced draft through the same field rules every
// other write path uses, so a hostile reply cannot reach the board even if
// coerceDraftMap ever loosens. A draft that fails is dropped, not repaired.
func validateDraft(d storyDraft) error {
	return store.ValidateTaskFields(board.Task{
		Title:  d.Title,
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
