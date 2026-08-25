package board

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// doc is the legacy cross-client Markdown fixture.
const doc = `# Ops Board

## To Do

- [ ] 🚚 Ship crates !1 @2026-07-21 ~L #infra #env::prod
  Coordinate with the warehouse team.
  Confirm the loading dock slot.
  - [ ] pack boxes
  - [x] book truck

## Doing

- [ ] Migrate database

## Done

- [x] Provision servers
`

// taskProj is the id/timestamp/position-free projection used for round-trip
// equality. The JSON tags name the frozen fixtures in testdata/*.json.
type taskProj struct {
	Emoji   string   `json:"emoji"`
	Title   string   `json:"title"`
	Desc    string   `json:"desc"`
	Status  Status   `json:"status"`
	Blocked bool     `json:"blocked"`
	Prio    int      `json:"prio"`
	Due     string   `json:"due"`
	Effort  string   `json:"effort"`
	Tags    []string `json:"tags"`
	Checks  []Check  `json:"checks"`
}

type boardProj struct {
	Title string     `json:"title"`
	Tasks []taskProj `json:"tasks"`
}

func projection(b Board) boardProj {
	p := boardProj{Title: b.Title}
	for _, t := range b.Tasks {
		p.Tasks = append(p.Tasks, taskProj{
			Emoji:   t.Emoji,
			Title:   t.Title,
			Desc:    t.Desc,
			Status:  t.Status,
			Blocked: t.Blocked,
			Prio:    t.Prio,
			Due:     t.Due,
			Effort:  t.Effort,
			Tags:    t.Tags,
			Checks:  t.Checks,
		})
	}
	return p
}

// AI replies can include extra text or multiple symbols, so callers need the
// exact leading token the unchanged markdown grammar would preserve.
func TestLeadingEmojiNormalization(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare emoji", in: "🚀", want: "🚀"},
		{name: "emoji with variation selector", in: "⚙️", want: "⚙️"},
		{name: "two emoji", in: "🚀✨", want: "🚀"},
		{name: "shortcode", in: ":rocket:", want: ""},
		{name: "leading text", in: "ship 🚀", want: ""},
		{name: "empty", in: "", want: ""},
		{name: "multibyte non emoji", in: "界", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LeadingEmoji(tt.in); got != tt.want {
				t.Errorf("LeadingEmoji(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStatusValid(t *testing.T) {
	for _, s := range Statuses {
		if !s.Valid() {
			t.Errorf("Statuses entry %q must be valid", s)
		}
	}
	for _, s := range []Status{"", "archived", "To Do"} {
		if s.Valid() {
			t.Errorf("Status(%q).Valid() = true, want false", s)
		}
	}
}

func TestParseFullDocument(t *testing.T) {
	b := Parse(doc)

	if b.Title != "Ops Board" {
		t.Errorf("title = %q, want %q", b.Title, "Ops Board")
	}
	if len(b.Tasks) != 3 {
		t.Fatalf("len(tasks) = %d, want 3", len(b.Tasks))
	}

	rich := b.Tasks[0]
	if rich.Emoji != "🚚" {
		t.Errorf("emoji = %q, want 🚚", rich.Emoji)
	}
	if rich.Title != "Ship crates" {
		t.Errorf("title = %q, want %q", rich.Title, "Ship crates")
	}
	if rich.Status != StatusTodo {
		t.Errorf("status = %q, want todo", rich.Status)
	}
	if rich.Prio != 1 {
		t.Errorf("prio = %d, want 1", rich.Prio)
	}
	if rich.Due != "2026-07-21" {
		t.Errorf("due = %q, want 2026-07-21", rich.Due)
	}
	if rich.Effort != "L" {
		t.Errorf("effort = %q, want L", rich.Effort)
	}
	if !reflect.DeepEqual(rich.Tags, []string{"infra", "env::prod"}) {
		t.Errorf("tags = %#v, want [infra env::prod]", rich.Tags)
	}
	wantDesc := "Coordinate with the warehouse team.\nConfirm the loading dock slot."
	if rich.Desc != wantDesc {
		t.Errorf("desc = %q, want %q", rich.Desc, wantDesc)
	}
	wantChecks := []Check{{Text: "pack boxes", Done: false}, {Text: "book truck", Done: true}}
	if !reflect.DeepEqual(rich.Checks, wantChecks) {
		t.Errorf("checks = %#v, want %#v", rich.Checks, wantChecks)
	}
	if rich.ID != "" {
		t.Errorf("id = %q, want empty (wire format carries no ids)", rich.ID)
	}
	if rich.CreatedAt.IsZero() || rich.MovedAt.IsZero() {
		t.Error("createdAt/movedAt must be set")
	}

	doing := b.Tasks[1]
	if doing.Title != "Migrate database" {
		t.Errorf("doing title = %q", doing.Title)
	}
	if doing.Status != StatusDoing {
		t.Errorf("doing status = %q, want doing", doing.Status)
	}
	if doing.Emoji != "" || doing.Prio != 3 || doing.Due != "" || doing.Effort != "" {
		t.Errorf("doing defaults wrong: emoji=%q prio=%d due=%q effort=%q",
			doing.Emoji, doing.Prio, doing.Due, doing.Effort)
	}
	if len(doing.Tags) != 0 || doing.Desc != "" || len(doing.Checks) != 0 {
		t.Errorf("doing extras wrong: tags=%#v desc=%q checks=%#v",
			doing.Tags, doing.Desc, doing.Checks)
	}

	done := b.Tasks[2]
	if done.Title != "Provision servers" {
		t.Errorf("done title = %q", done.Title)
	}
	if done.Status != StatusDone {
		t.Errorf("done status = %q, want done", done.Status)
	}

	for i, task := range b.Tasks {
		if task.Position != 0 {
			t.Errorf("task %d position = %d, want 0 (first in its column)", i, task.Position)
		}
	}
}

func TestRoundTripProjection(t *testing.T) {
	first := Parse(doc)
	second := Parse(Serialize(first))
	if !reflect.DeepEqual(projection(second), projection(first)) {
		t.Errorf("round trip changed content:\nfirst:  %#v\nsecond: %#v",
			projection(first), projection(second))
	}
}

func TestInvalidTokensRemainInTitle(t *testing.T) {
	b := Parse(strings.Join([]string{
		"# B", "", "## To Do", "", "- [ ] Fix thing !9 @not-a-date ~X #",
	}, "\n"))
	if len(b.Tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(b.Tasks))
	}
	task := b.Tasks[0]
	if task.Title != "Fix thing !9 @not-a-date ~X #" {
		t.Errorf("title = %q, want invalid tokens preserved", task.Title)
	}
	if task.Prio != 3 || task.Due != "" || task.Effort != "" || len(task.Tags) != 0 {
		t.Errorf("invalid tokens leaked into fields: prio=%d due=%q effort=%q tags=%#v",
			task.Prio, task.Due, task.Effort, task.Tags)
	}
}

func TestCheckedItemsAreDoneInAnyColumn(t *testing.T) {
	b := Parse(strings.Join([]string{
		"# B", "",
		"## To Do", "", "- [x] Checked in todo", "",
		"## Doing", "", "- [x] Checked in doing",
	}, "\n"))
	if len(b.Tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(b.Tasks))
	}
	if b.Tasks[0].Title != "Checked in todo" || b.Tasks[0].Status != StatusDone {
		t.Errorf("task 0 = %q/%q, want Checked in todo/done", b.Tasks[0].Title, b.Tasks[0].Status)
	}
	if b.Tasks[1].Title != "Checked in doing" || b.Tasks[1].Status != StatusDone {
		t.Errorf("task 1 = %q/%q, want Checked in doing/done", b.Tasks[1].Title, b.Tasks[1].Status)
	}
}

func TestUnknownFirstHeaderMapsToTodoByPosition(t *testing.T) {
	b := Parse(strings.Join([]string{
		"# B", "", "## Backlog", "", "- [ ] Something someday",
	}, "\n"))
	if len(b.Tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(b.Tasks))
	}
	if b.Tasks[0].Title != "Something someday" {
		t.Errorf("title = %q", b.Tasks[0].Title)
	}
	if b.Tasks[0].Status != StatusTodo {
		t.Errorf("status = %q, want todo", b.Tasks[0].Status)
	}
}

func TestUnknownHeadersMapByPosition(t *testing.T) {
	b := Parse(strings.Join([]string{
		"# B", "",
		"## Backlog", "", "- [ ] a", "",
		"## In Progress", "", "- [ ] b", "",
		"## Shipped", "", "- [ ] c", "",
		"## Overflow", "", "- [ ] d", "",
		"## Beyond", "", "- [ ] e",
	}, "\n"))
	if len(b.Tasks) != 5 {
		t.Fatalf("len(tasks) = %d, want 5", len(b.Tasks))
	}
	want := []Status{StatusTodo, StatusDoing, StatusDone, StatusCancelled, StatusCancelled}
	for i, w := range want {
		if b.Tasks[i].Status != w {
			t.Errorf("task %d status = %q, want %q", i, b.Tasks[i].Status, w)
		}
	}
}

func TestEscapingRoundTrip(t *testing.T) {
	in := Board{Title: "B", Tasks: []Task{{
		Title:  `Fix #123 login !2 ~S @2026-01-01 \raw`,
		Desc:   "first\n- [ ] buy milk\n- [x] paid",
		Status: StatusTodo,
		Prio:   3,
	}}}
	got := Parse(Serialize(in))
	if len(got.Tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1 (line injection?)", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.Title != in.Tasks[0].Title {
		t.Errorf("title = %q, want %q", task.Title, in.Tasks[0].Title)
	}
	if task.Prio != 3 || task.Due != "" || task.Effort != "" || len(task.Tags) != 0 {
		t.Errorf("escaped title tokens leaked into metadata: prio=%d due=%q effort=%q tags=%#v",
			task.Prio, task.Due, task.Effort, task.Tags)
	}
	if task.Desc != in.Tasks[0].Desc {
		t.Errorf("desc = %q, want %q", task.Desc, in.Tasks[0].Desc)
	}
	if len(task.Checks) != 0 {
		t.Errorf("desc lines became checks: %#v", task.Checks)
	}
	// The wire form is stable across repeated round trips.
	if s1, s2 := Serialize(in), Serialize(got); s1 != s2 {
		t.Errorf("serialize not stable:\nfirst:  %q\nsecond: %q", s1, s2)
	}
}

func TestBlockedTokenRoundTrip(t *testing.T) {
	in := Board{Title: "B", Tasks: []Task{
		{Title: "Waiting on legal", Status: StatusDoing, Blocked: true, Prio: 3, Effort: "M", Tags: []string{"infra"}},
		{Title: "Free to go", Status: StatusDoing, Prio: 3},
	}}
	wire := Serialize(in)
	if !strings.Contains(wire, "- [ ] Waiting on legal ~M %blocked #infra\n") {
		t.Errorf("blocked token missing or misplaced:\n%s", wire)
	}
	if strings.Contains(wire, "Free to go %blocked") {
		t.Errorf("unblocked task carries the token:\n%s", wire)
	}
	got := Parse(wire)
	if len(got.Tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(got.Tasks))
	}
	if !got.Tasks[0].Blocked {
		t.Error("blocked task parsed as unblocked")
	}
	if got.Tasks[0].Title != "Waiting on legal" {
		t.Errorf("title = %q, want %q", got.Tasks[0].Title, "Waiting on legal")
	}
	if got.Tasks[1].Blocked {
		t.Error("unblocked task parsed as blocked")
	}
}

func TestLiteralBlockedWordStaysInTitle(t *testing.T) {
	for _, title := range []string{`%blocked`, `Why %blocked matters`, `\%blocked`, `%blockedish`} {
		in := Board{Title: "B", Tasks: []Task{{Title: title, Status: StatusTodo, Prio: 3}}}
		got := Parse(Serialize(in))
		if len(got.Tasks) != 1 {
			t.Fatalf("title %q: len(tasks) = %d, want 1", title, len(got.Tasks))
		}
		if got.Tasks[0].Title != title {
			t.Errorf("title = %q, want %q", got.Tasks[0].Title, title)
		}
		if got.Tasks[0].Blocked {
			t.Errorf("title %q leaked into the blocked flag", title)
		}
	}
}

func TestCancelledSection(t *testing.T) {
	in := Board{Title: "B", Tasks: []Task{
		{Title: "Landed", Status: StatusDone, Prio: 3},
		{Title: "Dropped", Status: StatusCancelled, Prio: 3},
	}}
	wire := Serialize(in)
	want := "# B\n\n## To Do\n\n\n## Doing\n\n\n## Done\n\n- [x] Landed\n\n## Cancelled\n\n- [ ] Dropped\n"
	if wire != want {
		t.Errorf("wire =\n%q\nwant\n%q", wire, want)
	}
	got := Parse(wire)
	if len(got.Tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(got.Tasks))
	}
	if got.Tasks[1].Status != StatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Tasks[1].Status)
	}
	if !reflect.DeepEqual(projection(Parse(Serialize(got))), projection(got)) {
		t.Error("cancelled section does not round trip")
	}
}

func TestLegacyThreeSectionBoardIsByteIdentical(t *testing.T) {
	if got := Serialize(Parse(doc)); got != doc {
		t.Errorf("legacy three-section board changed on round trip:\n--- got ---\n%s\n--- want ---\n%s", got, doc)
	}
	if strings.Contains(Serialize(Parse(doc)), "Cancelled") {
		t.Error("empty cancelled section leaked into a legacy board")
	}
}

// TestLegacyPrioTokenReadsAsLow pins the one asymmetry issue #234 leaves in
// the frozen wire format. The scale is three values, but boards written before
// the collapse carry a !4 token, and the shared fixtures no longer do because
// a !4 cannot round-trip: it reads as low and low is the omitted default.
// Narrowing prioRe instead would turn the token into a title word, which is a
// worse answer than reading it as the low priority it always meant.
func TestLegacyPrioTokenReadsAsLow(t *testing.T) {
	got := Parse("# B\n\n## To Do\n\n- [ ] legacy card !4\n")
	if len(got.Tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.Title != "legacy card" {
		t.Errorf("title = %q, want %q (the !4 token must not survive as text)", task.Title, "legacy card")
	}
	if task.Prio != PrioLow {
		t.Errorf("prio = %d, want %d", task.Prio, PrioLow)
	}
	// Re-serializing drops the token, because low is the omitted default.
	if line := Serialize(got); strings.Contains(line, "!4") {
		t.Errorf("Serialize re-emitted the retired token:\n%s", line)
	}
}

// TestSerializeNormalizesRetiredPrio proves the writer never emits a token the
// reader only tolerates for legacy input.
func TestSerializeNormalizesRetiredPrio(t *testing.T) {
	for _, prio := range []int{0, 4, 9, -1} {
		out := Serialize(Board{Title: "B", Tasks: []Task{{Title: "card", Status: StatusTodo, Prio: prio}}})
		if strings.Contains(out, "!") {
			t.Errorf("prio %d serialized a priority token:\n%s", prio, out)
		}
		if got := Parse(out).Tasks[0].Prio; got != PrioLow {
			t.Errorf("prio %d round-tripped to %d, want %d", prio, got, PrioLow)
		}
	}
}

// TestEveryTaskLineParsesBack is the codec side of the empty-title fix
// (store.ValidateTaskFields): as long as a task's title is not blank, its
// serialized line is one Parse reads back as a task rather than as
// description text grafted onto the task before it.
func TestEveryTaskLineParsesBack(t *testing.T) {
	tasks := []Task{
		{Title: "plain", Status: StatusTodo, Prio: 3},
		{Title: "0", Status: StatusTodo, Prio: 3},
		{Title: `\`, Status: StatusTodo, Prio: 3},
		{Title: "%blocked", Status: StatusTodo, Prio: 3, Blocked: true},
		{Title: "- [x] forged", Status: StatusTodo, Prio: 3},
		{Title: "#tag !1 ~S @2026-01-01", Status: StatusDoing, Prio: 2, Blocked: true},
		{Title: "日本語 café 🚀", Emoji: "🔥", Status: StatusDone, Prio: 1, Due: "2026-02-03", Effort: "L", Tags: []string{"a", "k::v"}},
		{Title: "cancelled one", Status: StatusCancelled, Prio: 3, Checks: []Check{{Text: "step", Done: true}}},
	}
	for _, task := range tasks {
		in := Board{Title: "B", Tasks: []Task{{Title: "anchor", Status: task.Status, Prio: 3}, task}}
		got := Parse(Serialize(in))
		if len(got.Tasks) != 2 {
			t.Errorf("task %q: len(tasks) = %d, want 2 (line not read back as a task)", task.Title, len(got.Tasks))
			continue
		}
		if !reflect.DeepEqual(projection(got).Tasks[1], projection(in).Tasks[1]) {
			t.Errorf("task %q changed on round trip:\ngot  %#v\nwant %#v",
				task.Title, projection(got).Tasks[1], projection(in).Tasks[1])
		}
	}
}

func TestGoldenCanonicalSerialize(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.md")
	if err != nil {
		t.Fatal(err)
	}
	golden := string(raw)
	got := Serialize(Parse(golden))
	if got != golden {
		t.Errorf("Serialize(Parse(golden)) differs from golden:\n--- got ---\n%s\n--- want ---\n%s", got, golden)
	}
}

func TestGoldenRoundTripProjection(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.md")
	if err != nil {
		t.Fatal(err)
	}
	first := Parse(string(raw))
	second := Parse(Serialize(first))
	if !reflect.DeepEqual(projection(second), projection(first)) {
		t.Errorf("golden round trip changed content:\nfirst:  %#v\nsecond: %#v",
			projection(first), projection(second))
	}

	wantPositions := map[Status][]int{}
	for _, task := range first.Tasks {
		wantPositions[task.Status] = append(wantPositions[task.Status], task.Position)
	}
	for status, positions := range wantPositions {
		for i, p := range positions {
			if p != i {
				t.Errorf("column %q positions = %v, want 0..n in order", status, positions)
				break
			}
		}
	}
}
