package board

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// doc mirrors the DOC fixture in src/lib/markdown.test.ts.
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

// taskProj is the id/timestamp/position-free projection used for
// round-trip equality, mirroring projection() in the TS tests.
type taskProj struct {
	Emoji  string
	Title  string
	Desc   string
	Status Status
	Prio   int
	Due    string
	Effort string
	Tags   []string
	Checks []Check
}

type boardProj struct {
	Title string
	Tasks []taskProj
}

func projection(b Board) boardProj {
	p := boardProj{Title: b.Title}
	for _, t := range b.Tasks {
		p.Tasks = append(p.Tasks, taskProj{
			Emoji:  t.Emoji,
			Title:  t.Title,
			Desc:   t.Desc,
			Status: t.Status,
			Prio:   t.Prio,
			Due:    t.Due,
			Effort: t.Effort,
			Tags:   t.Tags,
			Checks: t.Checks,
		})
	}
	return p
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
		"## Overflow", "", "- [ ] d",
	}, "\n"))
	if len(b.Tasks) != 4 {
		t.Fatalf("len(tasks) = %d, want 4", len(b.Tasks))
	}
	want := []Status{StatusTodo, StatusDoing, StatusDone, StatusDone}
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
