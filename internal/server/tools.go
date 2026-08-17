package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/rig"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// Bounds for the agent tool layer. The model chooses the arguments, so every
// unbounded read is bounded here rather than in the prompt.
const (
	// fetchLinkMaxBytes caps one fetched document. The URL is model-chosen and
	// the body lands in the next prompt, so the read stops at the cap instead
	// of buffering whatever the host serves.
	fetchLinkMaxBytes = 64 << 10
	// findSimilarLimit matches GET /api/similar: three stubs are enough to
	// spot a duplicate and cheap enough to call once per candidate card.
	findSimilarLimit = 3
	// findSimilarMinRunes is the shortest query the similar endpoint answers;
	// below it the store is not queried at all.
	findSimilarMinRunes = 3
)

const cardLimitReachedMessage = "card limit reached; stop proposing"

// linkFetchTimeout bounds one fetch_link request, matching the forge fetch it
// is shaped after.
const linkFetchTimeout = 20 * time.Second

// newLinkClient builds the transport fetch_link uses. The destination is a URL
// the model composed, so loopback, link-local, private and unspecified
// addresses are refused on the resolved IP unless KB_LINK_ALLOW_PRIVATE opts
// in -- its own variable, never the forge one: letting a self-hosted forge
// resolve privately must not also hand the model's URLs the LAN and the cloud
// metadata service. Same syntax as the forge variable: a comma-separated
// hostname allowlist, or 1 / * for every host.
func newLinkClient() *http.Client {
	raw := os.Getenv("KB_LINK_ALLOW_PRIVATE")
	allowAll := raw == "1" || raw == "*"
	var allowHosts map[string]bool
	if !allowAll {
		allowHosts = parseAllowedHosts(raw)
	}
	return &http.Client{
		Timeout:       linkFetchTimeout,
		Transport:     guardedTransport(allowHosts, allowAll),
		CheckRedirect: sameHostRedirect,
	}
}

// cardCollector accumulates what propose_card accepted for one run. The rig
// loop executes tool calls sequentially within a single run, so the slice
// needs no lock; one collector must not be shared across runs.
type cardCollector struct {
	max   int
	cards []storyDraft
}

// atCap reports whether propose_card must refuse. A collector built without a
// positive max accepts nothing: an unset cap is a missing budget, not an
// unlimited one.
func (c *cardCollector) atCap() bool {
	return c.max <= 0 || len(c.cards) >= c.max
}

// proposeCardTool is the structured output channel: cards reach the caller
// through this tool, never through the final reply, so nothing parses the
// model's prose as JSON. Refusals are errors the model sees and can act on --
// silently dropping a proposal past the cap would leave the model believing it
// delivered work it did not.
func proposeCardTool(c *cardCollector) rig.Tool {
	return rig.Tool{
		Name: "propose_card",
		Description: "Propose one kanban card. Call this once per card; the cards are collected " +
			"server-side and returned to the user, so never repeat them in your reply.",
		InputSchema: schemaObject(map[string]any{
			"title":  schemaString("short imperative card title"),
			"emoji":  schemaString("single emoji that suits the work, or an empty string"),
			"desc":   schemaString("markdown description: context and rationale"),
			"prio":   schemaInt("priority 1 (highest) to 4 (lowest); 3 is the default"),
			"due":    schemaString("due date as YYYY-MM-DD, or an empty string"),
			"effort": schemaEnum("effort estimate", "S", "M", "L", ""),
			"tags":   schemaStrings("labels, single words with no spaces and no leading '#'"),
			"checks": schemaChecks("acceptance criteria as checklist items"),
			"source": schemaInt("1-based Source number of the forge issue this card came from; only when the input is numbered forge issues"),
		}, "title"),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			if c.atCap() {
				return "", errors.New(cardLimitReachedMessage)
			}
			var m map[string]any
			if err := json.Unmarshal(input, &m); err != nil {
				return "", errors.New("invalid input: expected a JSON object matching the tool schema")
			}
			draft := coerceDraftMap(m)
			if err := validateDraft(draft); err != nil {
				return "", fmt.Errorf("card rejected: %w", err)
			}
			c.cards = append(c.cards, draft)
			return marshalToolResult(struct {
				Accepted bool `json:"accepted"`
				Count    int  `json:"count"`
			}{Accepted: true, Count: len(c.cards)})
		},
	}
}

// findSimilarTool exposes the duplicate check the UI runs before a card is
// created, scoped to user's board.
func (s *server) findSimilarTool(user string) rig.Tool {
	return rig.Tool{
		Name: "find_similar",
		Description: "Search existing cards and import history for work that already covers a " +
			"proposal. Call this before proposing a card. Returns cheap stubs.",
		InputSchema: schemaObject(map[string]any{
			"query": schemaString("free text matched against card titles, descriptions, tags, and import history"),
		}, "query"),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", errors.New(`invalid input: expected {"query": string}`)
			}
			query := strings.TrimSpace(args.Query)
			if utf8.RuneCountInString(query) < findSimilarMinRunes {
				return "", fmt.Errorf("query must be at least %d characters", findSimilarMinRunes)
			}
			hits, err := s.store.SearchSimilar(user, query, "", nil, findSimilarLimit)
			if err != nil {
				log.Printf("tools: find_similar for %s: %s", logSafe(user), logSafe(err.Error()))
				return "", errors.New(storageErrorMessage)
			}
			out := similarResponse{Items: []similarItem{}}
			for _, hit := range hits {
				out.Items = append(out.Items, similarItem{
					ID: hit.ID, Title: hit.Title, Status: hit.Status, Via: hit.Via, Link: hit.Link,
				})
			}
			return marshalToolResult(out)
		},
	}
}

// fetchLinkTool reads one http(s) document the model asked for. The request
// goes through s.linkClient, whose SSRF guard is governed by its own
// KB_LINK_ALLOW_PRIVATE, so a model-chosen URL cannot reach a private address
// on the strength of a forge opt-in.
//
// Failures are deliberately generic: the model already knows the URL it sent,
// and an error naming the status or the transport reason would turn this tool
// into a host reachability oracle for whoever writes the document it reads.
//
// Depends on the linkClient field on server, owned by the runner change.
func (s *server) fetchLinkTool() rig.Tool {
	return rig.Tool{
		Name: "fetch_link",
		Description: "Fetch one http(s) document and return its text. Use it to read a " +
			"specification or issue the user referenced by URL.",
		InputSchema: schemaObject(map[string]any{
			"url": schemaString("absolute http or https URL"),
		}, "url"),
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var args struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", errors.New(`invalid input: expected {"url": string}`)
			}
			return s.fetchLink(ctx, strings.TrimSpace(args.URL))
		},
	}
}

func (s *server) fetchLink(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !isHTTPURL(u) || u.Host == "" {
		return "", errors.New("url must be an absolute http or https URL")
	}
	// Credentials in the URL would be sent to a host the model picked.
	if u.User != nil {
		return "", errors.New("url must not contain a username or password")
	}
	if s.linkClient == nil {
		return "", errors.New("link fetching is not available")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", errFetchLinkFailed
	}
	request.Header.Set("Accept", "text/*, application/json")
	response, err := s.linkClient.Do(request)
	if err != nil {
		log.Printf("tools: fetch_link request failed: %s", logSafe(err.Error()))
		return "", errFetchLinkFailed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		log.Printf("tools: fetch_link returned status %d", response.StatusCode)
		return "", errFetchLinkFailed
	}
	if !fetchLinkTextual(response.Header.Get(contentTypeHeader)) {
		return "", errors.New("link is not text; only text/* and application/json can be read")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, fetchLinkMaxBytes))
	if err != nil {
		log.Printf("tools: fetch_link read failed: %s", logSafe(err.Error()))
		return "", errFetchLinkFailed
	}
	// The document is untrusted text on its way into a prompt; control
	// characters are stripped the way every other model-visible string is.
	return stripControlKeepLines(string(body)), nil
}

var errFetchLinkFailed = errors.New("could not fetch the link")

// fetchLinkTextual reports whether a Content-Type names something that can be
// read as text. A missing or unparseable type is rejected: guessing is how a
// binary payload ends up in a prompt.
func fetchLinkTextual(header string) bool {
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return false
	}
	return strings.HasPrefix(mediaType, "text/") || mediaType == jsonMediaType
}

// listTasksTool mirrors the MCP tool of the same name, scoped to user's board.
func (s *server) listTasksTool(user string) rig.Tool {
	return rig.Tool{
		Name: "list_tasks",
		Description: "List kanban tasks on the board, ordered by column (todo, doing, done, " +
			"cancelled) then position. Optional filters: one column (status), free text over " +
			"title/description/tags (search), and exact labels that must all be present (tags).",
		InputSchema: schemaObject(map[string]any{
			"status": schemaEnum("column filter; omit for all columns", "todo", "doing", "done", "cancelled"),
			"search": schemaString("free text matched against title, description, and tags"),
			"tags":   schemaStrings("exact label filters; a task must carry every listed tag"),
		}),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Status string   `json:"status"`
				Search string   `json:"search"`
				Tags   []string `json:"tags"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", errors.New("invalid input: expected a JSON object matching the tool schema")
			}
			filter := store.TaskFilter{Search: strings.TrimSpace(args.Search), Tags: args.Tags}
			if status := strings.TrimSpace(args.Status); status != "" {
				st, err := toolStatus(status)
				if err != nil {
					return "", err
				}
				filter.Status = st
			}
			tasks, err := s.store.FilterTasks(user, filter)
			if err != nil {
				log.Printf("tools: list_tasks for %s: %s", logSafe(user), logSafe(err.Error()))
				return "", errors.New(storageErrorMessage)
			}
			out := struct {
				Tasks []toolTask `json:"tasks"`
			}{Tasks: make([]toolTask, 0, len(tasks))}
			for _, t := range tasks {
				out.Tasks = append(out.Tasks, toToolTask(t))
			}
			return marshalToolResult(out)
		},
	}
}

// getTaskTool fetches one task in full by any reference the store resolves.
func (s *server) getTaskTool(user string) rig.Tool {
	return rig.Tool{
		Name:        "get_task",
		Description: "Fetch one task in full by its stable number (12 or #12), UUID, or unique UUID prefix.",
		InputSchema: schemaObject(map[string]any{
			"ref": schemaString("task number (12 or #12), UUID, or unique UUID prefix"),
		}, "ref"),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Ref string `json:"ref"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", errors.New(`invalid input: expected {"ref": string}`)
			}
			ref := strings.TrimSpace(args.Ref)
			if ref == "" {
				return "", errors.New("ref must not be empty")
			}
			t, err := s.store.Task(user, ref)
			if err != nil {
				return "", taskRefError(err, ref)
			}
			return marshalToolResult(toToolTask(t))
		},
	}
}

// toolUpdateArgs distinguishes a field the model supplied from one it left
// out: a nil pointer is an absent key, and a pointer to the zero value is an
// explicit clear. Status, move, and delete are deliberately absent -- this
// phase edits cards, it does not retire them.
type toolUpdateArgs struct {
	Ref     string       `json:"ref"`
	Title   *string      `json:"title"`
	Desc    *string      `json:"desc"`
	Emoji   *string      `json:"emoji"`
	Prio    *int         `json:"prio"`
	Due     *string      `json:"due"`
	Effort  *string      `json:"effort"`
	Tags    *[]string    `json:"tags"`
	Checks  *[]toolCheck `json:"checks"`
	Blocked *bool        `json:"blocked"`
}

// updateTaskTool edits an existing card. Only supplied fields change; tags and
// checks replace the whole list when given.
func (s *server) updateTaskTool(user string) rig.Tool {
	return rig.Tool{
		Name: "update_task",
		Description: "Update fields of one existing task. Only the fields you supply change; " +
			"tags and checks replace the whole list when given. An empty string clears a field.",
		InputSchema: schemaObject(map[string]any{
			"ref":     schemaString("task number (12 or #12), UUID, or unique UUID prefix"),
			"title":   schemaString("new title"),
			"desc":    schemaString("new markdown description (empty string clears it)"),
			"emoji":   schemaString("new emoji (empty string clears it)"),
			"prio":    schemaInt("new priority 1 (highest) to 4 (lowest)"),
			"due":     schemaString("new due date as YYYY-MM-DD (empty string clears it)"),
			"effort":  schemaEnum("new effort estimate", "S", "M", "L", ""),
			"tags":    schemaStrings("replacement label list"),
			"checks":  schemaChecks("replacement checklist"),
			"blocked": schemaBool("true to flag the task blocked, false to clear the flag"),
		}, "ref"),
		Run: func(_ context.Context, input json.RawMessage) (string, error) {
			var args toolUpdateArgs
			if err := json.Unmarshal(input, &args); err != nil {
				return "", errors.New("invalid input: expected a JSON object matching the tool schema")
			}
			ref := strings.TrimSpace(args.Ref)
			if ref == "" {
				return "", errors.New("ref must not be empty")
			}
			patch, err := args.patch()
			if err != nil {
				return "", err
			}
			t, err := s.store.UpdateTask(user, ref, patch)
			if err != nil {
				return "", taskRefError(err, ref)
			}
			return marshalToolResult(toToolTask(t))
		},
	}
}

// patch builds the store patch, sanitizing every model-supplied string the way
// the draft coercion does: a title carrying a newline would otherwise
// serialize as an extra board line.
func (a toolUpdateArgs) patch() (store.TaskPatch, error) {
	patch := store.TaskPatch{Blocked: a.Blocked}
	if a.Title != nil {
		title := strings.TrimSpace(stripControl(*a.Title))
		patch.Title = &title
	}
	if a.Desc != nil {
		desc := stripControlKeepLines(*a.Desc)
		patch.Desc = &desc
	}
	if a.Emoji != nil {
		emoji := board.LeadingEmoji(strings.TrimSpace(*a.Emoji))
		patch.Emoji = &emoji
	}
	if a.Prio != nil {
		if *a.Prio < 1 || *a.Prio > 4 {
			return store.TaskPatch{}, fmt.Errorf("invalid prio %d: must be 1 (highest) to 4 (lowest)", *a.Prio)
		}
		prio := *a.Prio
		patch.Prio = &prio
	}
	if a.Due != nil {
		due := strings.TrimSpace(*a.Due)
		patch.Due = &due
	}
	if a.Effort != nil {
		effort := strings.ToUpper(strings.TrimSpace(*a.Effort))
		patch.Effort = &effort
	}
	if a.Tags != nil {
		tags := make([]string, 0, len(*a.Tags))
		for _, tag := range *a.Tags {
			tags = append(tags, strings.TrimSpace(stripControl(tag)))
		}
		patch.Tags = &tags
	}
	if a.Checks != nil {
		checks := make([]board.Check, 0, len(*a.Checks))
		for i, c := range *a.Checks {
			text := strings.TrimSpace(stripControl(c.Text))
			if text == "" {
				return store.TaskPatch{}, fmt.Errorf("checks[%d].text must not be empty", i)
			}
			checks = append(checks, board.Check{Text: text, Done: c.Done})
		}
		patch.Checks = &checks
	}
	return patch, nil
}

// --- shared wire shapes ---

// toolCheck is a checklist item as the tools read and write it.
type toolCheck struct {
	Text string `json:"text"`
	Done bool   `json:"done,omitempty"`
}

// toolTask is the task shape the board tools return. It mirrors the MCP
// server's taskJSON, which is unexported, so the fields are re-declared here
// rather than shared.
type toolTask struct {
	ID      string      `json:"id"`
	Seq     int         `json:"seq,omitempty"`
	Emoji   string      `json:"emoji,omitempty"`
	Title   string      `json:"title"`
	Desc    string      `json:"desc,omitempty"`
	Status  string      `json:"status"`
	Blocked bool        `json:"blocked,omitempty"`
	Prio    int         `json:"prio"`
	Due     string      `json:"due,omitempty"`
	Effort  string      `json:"effort,omitempty"`
	Tags    []string    `json:"tags,omitempty"`
	Checks  []toolCheck `json:"checks,omitempty"`
}

func toToolTask(t board.Task) toolTask {
	out := toolTask{
		ID:      t.ID,
		Seq:     t.Seq,
		Emoji:   t.Emoji,
		Title:   t.Title,
		Desc:    t.Desc,
		Status:  string(t.Status),
		Blocked: t.Blocked,
		Prio:    t.Prio,
		Due:     t.Due,
		Effort:  t.Effort,
		Tags:    t.Tags,
	}
	for _, c := range t.Checks {
		out.Checks = append(out.Checks, toolCheck{Text: c.Text, Done: c.Done})
	}
	return out
}

// --- helpers ---

// marshalToolResult renders a tool result as the compact JSON the loop feeds
// back to the model. The values are server-owned structs, so a failure is a
// bug rather than something the model can correct; it gets an opaque message.
func marshalToolResult(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("tools: encode result: %v", err)
		return "", errors.New("could not encode the result")
	}
	return string(data), nil
}

// toolStatus validates a model-supplied column name.
func toolStatus(s string) (board.Status, error) {
	st := board.Status(strings.ToLower(s))
	if !st.Valid() {
		return "", fmt.Errorf("invalid status %q: must be todo, doing, done, or cancelled", s)
	}
	return st, nil
}

// taskRefError turns the store's id-resolution sentinels into errors the model
// can act on. The reference is echoed because the model supplied it; anything
// else (a validation refusal) passes through, since that detail is what lets
// the model retry with a legal field.
func taskRefError(err error, ref string) error {
	switch {
	case errors.Is(err, store.ErrAmbiguous):
		return fmt.Errorf("task reference %q is ambiguous; retry with a longer id prefix", ref)
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("no task matches %q; call list_tasks to see current tasks", ref)
	}
	return err
}

// --- input schema builders ---

func schemaObject(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		names := make([]any, 0, len(required))
		for _, name := range required {
			names = append(names, name)
		}
		schema["required"] = names
	}
	return schema
}

func schemaString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func schemaInt(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func schemaBool(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func schemaEnum(desc string, values ...string) map[string]any {
	allowed := make([]any, 0, len(values))
	for _, v := range values {
		allowed = append(allowed, v)
	}
	return map[string]any{"type": "string", "description": desc, "enum": allowed}
}

func schemaStrings(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       map[string]any{"type": "string"},
	}
}

func schemaChecks(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items": schemaObject(map[string]any{
			"text": schemaString("checklist item text"),
			"done": schemaBool("true when the item is already done"),
		}, "text"),
	}
}
