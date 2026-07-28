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
)

// aiTimeout bounds one upstream chat-completion round trip.
const aiTimeout = 60 * time.Second

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
		Timeout:   aiTimeout,
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
		code := http.StatusBadGateway
		var ae *aiError
		if errors.As(err, &ae) {
			code = ae.code
		}
		http.Error(w, err.Error(), code)
		return
	}
	draft, err := coerceDraft(content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, draft)
}

var draftDueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// coerceDraft parses assistant content as JSON and clamps every field into
// the draft contract: prio 1-4, effort S/M/L or empty, due YYYY-MM-DD or
// empty, tags as non-empty strings, checks as {text,done}.
func coerceDraft(content string) (storyDraft, error) {
	raw := strings.TrimSpace(content)
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		// Tolerate stray prose or fences around a single JSON object.
		start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
		if start < 0 || end <= start {
			return storyDraft{}, errors.New("assistant reply is not JSON")
		}
		if err := json.Unmarshal([]byte(raw[start:end+1]), &m); err != nil {
			return storyDraft{}, errors.New("assistant reply is not valid JSON")
		}
	}
	d := storyDraft{Prio: 3, Tags: []string{}, Checks: []draftCheck{}}
	if v, ok := m["title"].(string); ok {
		d.Title = strings.TrimSpace(v)
	}
	if v, ok := m["desc"].(string); ok {
		d.Desc = v
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
		if due := strings.TrimSpace(v); draftDueRe.MatchString(due) {
			d.Due = due
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
			if tag, ok := v.(string); ok {
				if tag = strings.TrimSpace(tag); tag != "" {
					d.Tags = append(d.Tags, tag)
				}
			}
		}
	}
	if vs, ok := m["checks"].([]any); ok {
		for _, v := range vs {
			cm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			text, _ := cm["text"].(string)
			if text = strings.TrimSpace(text); text == "" {
				continue
			}
			done, _ := cm["done"].(bool)
			d.Checks = append(d.Checks, draftCheck{Text: text, Done: done})
		}
	}
	return d, nil
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
