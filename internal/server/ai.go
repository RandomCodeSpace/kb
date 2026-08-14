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

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

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

// Every completion states its own output budget. Omitting it leaves the cap to
// the upstream default, which silently truncates a JSON reply mid-object and
// surfaces as "assistant reply is not valid JSON" — so the budgets are sized
// per call site: one card, up to maxStoryCount cards, up to maxImportIssues
// card proposals, one sentence of drift prose, one tool call.
//
// No budget may exceed aiMaxTokensCeiling. A model is only known by the name
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
	aiDefaultMaxTokens = 1024
	aiStoryMaxTokens   = 4096
	aiStoriesMaxTokens = 8192
	aiImportMaxTokens  = 8192
	aiDriftMaxTokens   = 1024
	aiProbeMaxTokens   = 256
	aiMaxTokensCeiling = 8192
)

// aiMaxResponseBytes caps one upstream reply. The endpoint is user-configured
// and the SDK buffers the whole body with io.ReadAll before decoding it, so
// without a ceiling a host that answers with an endless stream — or a gzip
// bomb, since the SDK asks for gzip — exhausts the shared process. This is the
// same 1 MiB the hand-rolled client enforced; the largest reply this server
// asks for is orders of magnitude smaller.
const aiMaxResponseBytes = 1 << 20

// errAIResponseTooLarge stops the read past the cap. It reaches chat as a
// transport-level failure, which is mapped to the same opaque 502 as any other
// upstream problem.
var errAIResponseTooLarge = errors.New("upstream response exceeds the size limit")

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

// chatMessage is one prompt message in the shape every caller builds. The wire
// encoding belongs to the SDK; this stays a plain struct so the prompts are
// readable and the coercion layer is unaffected by client changes.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCall is one completion request: what to ask, how much may come back, and
// whether the reply is constrained to JSON or to a tool call.
type chatCall struct {
	msgs      []chatMessage
	maxTokens int64
	jsonMode  bool
	tools     []openai.ChatCompletionToolUnionParam
}

// aiEndpoint validates the configured base URL and returns the API root the
// SDK appends chat/completions to, accepting bases with or without a trailing
// /v1 (and trailing slashes). The trailing slash matters: the SDK resolves
// method paths against this URL, and a base without one would lose its last
// segment.
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

// chatCompletion runs one chat completion against the user's configured
// endpoint and returns the assistant content. Errors are *aiError with a
// key-free message: 400 for configuration problems, 502 for upstream ones.
func (s *server) chatCompletion(user string, msgs []chatMessage, maxTokens int64, jsonMode bool) (string, error) {
	cfg, err := s.storedAIConfig(user)
	if err != nil {
		return "", err
	}
	msg, err := s.chat(user, cfg, chatCall{msgs: msgs, maxTokens: maxTokens, jsonMode: jsonMode})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

// limitedBody is a response body that fails once it has yielded more than the
// cap. The allowance is one byte over aiMaxResponseBytes so a reply of exactly
// the cap still reads to EOF; anything longer stops at cap+1 with
// errAIResponseTooLarge instead of being buffered.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errAIResponseTooLarge
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

// limitResponseBody caps what one upstream reply may hand to the SDK's
// io.ReadAll. It runs as SDK middleware rather than in the transport because
// the transport's gzip is transparent: by the time the response gets here the
// body is the decompressed stream, which is what the allocation is made of.
func limitResponseBody(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if err != nil || res == nil || res.Body == nil {
		return res, err
	}
	res.Body = &limitedBody{rc: res.Body, left: aiMaxResponseBytes + 1}
	return res, nil
}

// aiOwnedHeaders are headers this client sets for the request to work at all.
// A same-named value inherited from the environment is already overwritten
// (Authorization, by the per-request key), so deleting them in the ambient
// scrub would break the call rather than contain a leak.
var aiOwnedHeaders = map[string]bool{"authorization": true, "content-type": true, "accept": true}

// ambientHeaderNames lists the headers openai.NewClient derives from the
// server's own environment. DefaultClientOptions is prepended unconditionally
// and the option that would suppress it is internal to the SDK, so each header
// it can produce is deleted by name instead: OPENAI_ORG_ID and
// OPENAI_PROJECT_ID map to fixed names, and OPENAI_CUSTOM_HEADERS — arbitrary
// names, and where a gateway token is most likely to sit — is parsed here the
// way the SDK parses it so every name it declares is deleted too.
func ambientHeaderNames() []string {
	names := []string{"OpenAI-Organization", "OpenAI-Project"}
	for _, line := range strings.Split(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name != "" && !aiOwnedHeaders[strings.ToLower(name)] {
			names = append(names, name)
		}
	}
	return names
}

// aiSDKOptions builds the per-request client options for cfg. The SDK owns the
// wire format only: transport stays ours, so the SSRF-guarded dialer, the
// AITimeout and the same-host redirect policy hold for every AI request, and
// the response body is capped on the way back because the SDK reads it whole.
// Retries are disabled — one user action is one upstream request, and a retry
// would multiply both the timeout and the load a probe can direct at a host.
// The key is set explicitly on every call, and cleared explicitly when there
// is none, so a stray OPENAI_API_KEY in the server environment can never ride
// along to a user-configured endpoint; the other credentials and identifiers
// the SDK reads from the environment are deleted for the same reason.
func aiSDKOptions(base string, client *http.Client, key string) []option.RequestOption {
	opts := []option.RequestOption{
		option.WithBaseURL(base),
		option.WithHTTPClient(client),
		option.WithMaxRetries(0),
		option.WithMiddleware(limitResponseBody),
		option.WithAPIKey(key),
	}
	if key == "" {
		opts = append(opts, option.WithHeaderDel("Authorization"))
	}
	for _, name := range ambientHeaderNames() {
		opts = append(opts, option.WithHeaderDel(name))
	}
	return opts
}

// sdkMessages converts the prompt messages into the SDK's message unions.
// Only system and user prompts are built here; anything else is sent as a user
// message rather than dropped, because a dropped message changes the prompt.
func sdkMessages(msgs []chatMessage) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			out = append(out, openai.SystemMessage(m.Content))
			continue
		}
		out = append(out, openai.UserMessage(m.Content))
	}
	return out
}

// chat performs one chat completion against cfg and returns the assistant
// message. Every caller goes through here, so the SSRF-guarded client, the
// timeout and the key-free error mapping apply to a supplied configuration
// exactly as to a stored one; user is for logging only.
//
// Failure is opaque by construction: an upstream status, an upstream body or a
// transport reason would each turn this endpoint into a reachability oracle,
// so callers get one 502 message and the detail goes to the log without the
// response body, which is attacker-influenced text. A reply that reached the
// output budget is the one upstream outcome reported as itself (422): it says
// nothing a successful reply would not, and it is the caller's to act on.
func (s *server) chat(user string, cfg aiConfig, call chatCall) (openai.ChatCompletionMessage, error) {
	var empty openai.ChatCompletionMessage
	if strings.TrimSpace(cfg.baseURL) == "" {
		return empty, &aiError{http.StatusBadRequest, "AI base URL not configured"}
	}
	base, err := aiEndpoint(cfg.baseURL)
	if err != nil {
		return empty, &aiError{http.StatusBadRequest, err.Error()}
	}
	client := openai.NewClient(aiSDKOptions(base, s.aiClient, cfg.key)...)
	maxTokens := aiBudget(call.maxTokens)
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(cfg.model),
		Messages: sdkMessages(call.msgs),
		Tools:    call.tools,
	}
	if usesMaxCompletionTokens(cfg.model) {
		params.MaxCompletionTokens = openai.Int(maxTokens)
	} else {
		params.MaxTokens = openai.Int(maxTokens)
	}
	if call.jsonMode {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		}
	}
	completion, err := client.Chat.Completions.New(context.Background(), params)
	if err != nil {
		var apiErr *openai.Error
		if errors.As(err, &apiErr) {
			log.Printf("ai: request for %s returned upstream status %d", user, apiErr.StatusCode)
			return empty, &aiError{http.StatusBadGateway, fmt.Sprintf("upstream returned status %d", apiErr.StatusCode)}
		}
		log.Printf("ai: request for %s failed: %v", user, err)
		return empty, &aiError{http.StatusBadGateway, "upstream request failed"}
	}
	if len(completion.Choices) == 0 {
		return empty, &aiError{http.StatusBadGateway, "upstream returned no choices"}
	}
	choice := completion.Choices[0]
	// The budget is stated so the reply is not cut off by an unknown default,
	// but a stated budget can still be reached. finish_reason is the only
	// signal that says so: without it a reply cut mid-object comes back as
	// "assistant reply is not valid JSON", which sends the caller looking for
	// a bug in the model instead of asking for less in one request.
	if choice.FinishReason == finishReasonLength {
		log.Printf("ai: reply for %s was truncated at the %d-token budget", user, maxTokens)
		return empty, &aiError{http.StatusUnprocessableEntity, truncatedReplyMessage}
	}
	return choice.Message, nil
}

// finishReasonLength is what an upstream reports when the reply stopped
// because it reached the requested budget rather than a natural end.
const finishReasonLength = "length"

const truncatedReplyMessage = "the model's reply hit the output limit and was cut off — ask for less in one request"

// aiBudget resolves one call's output budget. No completion may leave the cap
// to the upstream default, which is what truncates a JSON reply mid-object,
// and none may ask for more than a small model will accept.
func aiBudget(maxTokens int64) int64 {
	if maxTokens < 1 {
		return aiDefaultMaxTokens
	}
	if maxTokens > aiMaxTokensCeiling {
		return aiMaxTokensCeiling
	}
	return maxTokens
}

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

// aiProbeToolName is the tool the connection test asks the model to call.
const aiProbeToolName = "ping"

// toolCallRequiredMessage is what a reachable endpoint whose model cannot call
// tools reports. Tool calling is a prerequisite, not a nice-to-have: the AI
// features are built on it, so a model that only produces prose fails the test
// rather than passing it and failing later on real work.
const toolCallRequiredMessage = "model must support tool calling"

const aiProbeSystemPrompt = `You are a connection probe. Call the ping tool. Do not answer with text.`

// aiProbeCall is the connection test: a trivial tool and an instruction to
// call it. Passing means the reply carried a call to that tool.
func aiProbeCall() chatCall {
	return chatCall{
		msgs: []chatMessage{
			{Role: "system", Content: aiProbeSystemPrompt},
			{Role: "user", Content: `Call the ping tool with message "ping".`},
		},
		maxTokens: aiProbeMaxTokens,
		tools: []openai.ChatCompletionToolUnionParam{
			openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
				Name:        aiProbeToolName,
				Description: openai.String("Acknowledge a connection probe."),
				Parameters: shared.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{"type": "string", "description": "Any short string."},
					},
					"required": []string{"message"},
				},
			}),
		},
	}
}

// hasProbeToolCall reports whether the reply actually called the probe tool.
// Content is ignored: a model that explains it would call ping has not called
// it, and the features this gates need the call itself.
func hasProbeToolCall(msg openai.ChatCompletionMessage) bool {
	for _, call := range msg.ToolCalls {
		if call.Function.Name == aiProbeToolName {
			return true
		}
	}
	return false
}

// handleAITest runs the tool-call probe and reports the result; failures come
// back as ok:false, not error statuses. With no body it tests the saved
// settings; with an aiTestRequest body it tests those values instead, so the
// form can be validated before it is saved. Either way the request goes
// through chat, which means the same SSRF-guarded client, timeout and opaque
// errors: every upstream outcome (connect failure, non-2xx status, bad body)
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
	cfg, err := s.storedAIConfig(user)
	if err != nil {
		return err
	}
	supplied := strings.TrimSpace(req.BaseURL)
	if strings.TrimSpace(req.Key) == "" && supplied != "" && cfg.key != "" &&
		!store.SameAIOrigin(cfg.baseURL, supplied) {
		return &aiError{http.StatusBadRequest, "enter the API key to test a different endpoint"}
	}
	msg, err := s.chat(user, cfg.merge(req), aiProbeCall())
	if err != nil {
		return err
	}
	if !hasProbeToolCall(msg) {
		return &aiError{http.StatusBadRequest, toolCallRequiredMessage}
	}
	return nil
}

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
	msgs := []chatMessage{
		{Role: "system", Content: storySystemPrompt},
		{Role: "user", Content: userMsg},
	}
	content, err := s.chatCompletion(user, msgs, aiStoryMaxTokens, true)
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

func normalizeStoryCount(max int) int {
	if max == 0 {
		max = defaultStoryCount
	}
	if max < 1 {
		max = 1
	}
	if max > maxStoryCount {
		max = maxStoryCount
	}
	return max
}

// handleAIStories splits one ADR into several card drafts. It shares the
// proxy client, timeout, SSRF guard and opaque error mapping with
// /api/ai/story — there is deliberately no second HTTP path — and the ADR
// text itself is never stored.
func (s *server) handleAIStories(w http.ResponseWriter, r *http.Request, user string) {
	req, ok := decodeAIStoriesRequest(w, r)
	if !ok {
		return
	}
	input, ok := s.resolveAIStoriesInput(w, r, user, req)
	if !ok {
		return
	}
	max := normalizeStoryCount(req.Max)
	msgs := []chatMessage{
		{Role: "system", Content: storiesSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Split this ADR into at most %d stories:\n\n%s", max, input.adr)},
	}
	content, err := s.chatCompletion(user, msgs, aiStoriesMaxTokens, true)
	if err != nil {
		writeAIError(w, user, "stories", err)
		return
	}
	stories, err := coerceDrafts(content, max)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
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

var draftDueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// thinkBlockRe matches a closed inline reasoning block. Some backends leave
// the model's chain-of-thought in the message content instead of a separate
// field, and reasoning about a JSON answer routinely contains braces, which
// would defeat the brace scan in decodeJSONObject. Reasoning drafts are also
// not the answer: a JSON object inside a think block must never be returned
// as the reply.
var thinkBlockRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// decodeJSONObject parses assistant content as a single JSON object,
// tolerating inline reasoning blocks, stray prose, and code fences around it.
func decodeJSONObject(content string) (map[string]any, error) {
	raw := strings.TrimSpace(thinkBlockRe.ReplaceAllString(content, ""))
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err == nil {
		return m, nil
	}
	// Surrounding prose can itself contain braces, so a first-{-to-last-}
	// slice is not enough: decode at each "{" until one yields an object.
	for i := strings.Index(raw, "{"); i >= 0 && i < len(raw); i++ {
		if raw[i] != '{' {
			continue
		}
		var candidate map[string]any
		if err := json.NewDecoder(strings.NewReader(raw[i:])).Decode(&candidate); err == nil {
			return candidate, nil
		}
	}
	if !strings.Contains(raw, "{") {
		return nil, errors.New("assistant reply is not JSON")
	}
	return nil, errors.New("assistant reply is not valid JSON")
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
	coerceDraftTextFields(m, &d)
	coerceDraftPrio(m, &d)
	coerceDraftDue(m, &d)
	coerceDraftEffort(m, &d)
	coerceDraftTags(m, &d)
	coerceDraftChecks(m, &d)
	return d
}

func coerceDraftTextFields(m map[string]any, d *storyDraft) {
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
}

func coerceDraftPrio(m map[string]any, d *storyDraft) {
	switch v := m["prio"].(type) {
	case float64:
		d.Prio = clampPrio(int(v))
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			d.Prio = clampPrio(n)
		}
	}
}

func coerceDraftDue(m map[string]any, d *storyDraft) {
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
}

func coerceDraftEffort(m map[string]any, d *storyDraft) {
	if v, ok := m["effort"].(string); ok {
		switch e := strings.ToUpper(strings.TrimSpace(v)); e {
		case "S", "M", "L":
			d.Effort = e
		}
	}
}

func coerceDraftTags(m map[string]any, d *storyDraft) {
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
}

func coerceDraftChecks(m map[string]any, d *storyDraft) {
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
