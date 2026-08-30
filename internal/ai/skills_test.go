package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"google.golang.org/adk/v2/agent"
)

// newSkillsServer builds a server whose only interesting field is the skills
// directory; loadSkills touches neither the store nor the network.
func newSkillsServer(t *testing.T, skillsDir string) *Runner {
	t.Helper()
	return NewRunner(newTestStore(t), skillsDir, nil, nil)
}

func writeSkillFixture(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		t.Fatalf("write skill fixture %s: %v", file, err)
	}
}

func skillFixtureByName(skills []Skill, name string) (Skill, bool) {
	for _, skill := range skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return Skill{}, false
}

func skillMapFile(body string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(body)}
}

func TestLoadSkillCatalogIsFlatSortedAndStrict(t *testing.T) {
	t.Run("loads direct children in skill-name order", func(t *testing.T) {
		catalogue := fstest.MapFS{
			"skills/z.md":            skillMapFile("---\nname: alpha\ndescription: first\n---\nalpha body\n"),
			"skills/a.md":            skillMapFile("---\r\nname: beta\r\ndescription: 'use when: second'\r\n---\r\nbeta body\r\n"),
			"skills/notes.txt":       skillMapFile("ignored"),
			"skills/nested/inner.md": skillMapFile("not valid, but not a direct child"),
		}
		skills, err := loadSkillCatalog(catalogue, "skills")
		if err != nil {
			t.Fatalf("loadSkillCatalog: %v", err)
		}
		if len(skills) != 2 || skills[0].Name != "alpha" || skills[1].Name != "beta" {
			t.Fatalf("skills = %+v, want alpha then beta", skills)
		}
		if skills[1].Description != "use when: second" || skills[1].Body != "beta body" {
			t.Errorf("beta = %+v, want quoted description and trimmed CRLF body", skills[1])
		}
	})

	t.Run("duplicate and malformed files fail the whole catalogue", func(t *testing.T) {
		catalogue := fstest.MapFS{
			"skills/a.md": skillMapFile("---\nname: duplicate\ndescription: first\n---\nbody\n"),
			"skills/b.md": skillMapFile("---\nname: duplicate\ndescription: second\n---\nbody\n"),
			"skills/c.md": skillMapFile("missing frontmatter\n"),
		}
		skills, err := loadSkillCatalog(catalogue, "skills")
		if err == nil {
			t.Fatalf("loadSkillCatalog = %+v, want an error", skills)
		}
		if skills != nil {
			t.Errorf("loadSkillCatalog returned %+v alongside the error", skills)
		}
		for _, want := range []string{"skills/b.md", "duplicate skill name", "skills/a.md", "skills/c.md", "opening delimiter"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q missing %q", err, want)
			}
		}
	})
}

func TestMergeAndAdvertiseSkillsAreDeterministic(t *testing.T) {
	base := []Skill{
		{Name: "zeta", Description: "base zeta", Body: "base body"},
		{Name: "alpha", Description: "base alpha", Body: "alpha body"},
	}
	overrides := []Skill{
		{Name: "zeta", Description: "operator\n override", Body: "operator body"},
		{Name: "middle", Description: "new skill", Body: "middle body"},
	}
	merged := mergeSkills(base, overrides)
	if len(merged) != 3 || merged[0].Name != "alpha" || merged[1].Name != "middle" || merged[2].Name != "zeta" {
		t.Fatalf("mergeSkills = %+v, want sorted alpha, middle, zeta", merged)
	}
	if merged[2].Description != "operator\n override" || merged[2].Body != "operator body" {
		t.Errorf("zeta = %+v, want the complete override", merged[2])
	}

	want := advertiseSkillsHeader + "\n\n- alpha: base alpha\n- middle: new skill\n- zeta: operator override"
	if got := advertiseSkills([]Skill{merged[2], merged[0], merged[1]}); got != want {
		t.Errorf("advertiseSkills = %q, want %q", got, want)
	}
	if base[0].Name != "zeta" || overrides[0].Description != "operator\n override" {
		t.Errorf("merge or advertise mutated its input: base=%+v overrides=%+v", base, overrides)
	}
}

func TestLoadSkillToolKeepsTheCatalogContract(t *testing.T) {
	tool := loadSkillTool([]Skill{
		{Name: "zeta", Body: "zeta body"},
		{Name: "alpha", Body: "alpha body"},
	})
	if tool.Name() != loadSkillToolName {
		t.Fatalf("tool name = %q, want %q", tool.Name(), loadSkillToolName)
	}
	got, err := runTool(t, tool, `{"name":" alpha "}`)
	if err != nil || got != "alpha body" {
		t.Fatalf("load alpha = %q, %v", got, err)
	}
	_, err = runTool(t, tool, `{"name":"missing"}`)
	if err == nil || err.Error() != `unknown skill "missing", available skills: alpha, zeta` {
		t.Fatalf("unknown skill error = %q", err)
	}

	ctx := &agent.StrictContextMock{Ctx: context.Background()}
	result, err := tool.Run(ctx, map[string]any{"name": "alpha"})
	if err != nil {
		t.Fatalf("native load result = %#v, %v", result, err)
	}
	requireRawChatToolResult(t, result)
}

// TestLoadSkillsEmbedded pins the built-in catalogue: the embed directive has
// to resolve and every shipped file has to carry parseable frontmatter, both
// of which only fail at run time.
func TestLoadSkillsEmbedded(t *testing.T) {
	s := newSkillsServer(t, "")
	skills, err := s.loadSkills()
	if err != nil {
		t.Fatalf("loadSkills: %v", err)
	}
	if len(skills) == 0 {
		t.Fatal("loadSkills returned no embedded skills")
	}
	for _, skill := range skills {
		if strings.Contains(strings.ToLower(skill.Body), "any other tool") {
			t.Errorf("skill %q forbids the tools the runner requires", skill.Name)
		}
	}

	adr, ok := skillFixtureByName(skills, "adr-split")
	if !ok {
		t.Fatalf("embedded skills = %v, want adr-split", skills)
	}
	if adr.Description != "Split an ADR or design document into implementation stories" {
		t.Errorf("adr-split description = %q", adr.Description)
	}
	// The body is the prompt: without the two tools the runner exposes for
	// this flow the skill cannot do its job.
	for _, want := range []string{"propose_card", "find_similar"} {
		if !strings.Contains(adr.Body, want) {
			t.Errorf("adr-split body does not mention %q", want)
		}
	}
	story, ok := skillFixtureByName(skills, "story-draft")
	if !ok {
		t.Fatalf("embedded skills = %v, want story-draft", skills)
	}
	for _, want := range []string{"find_similar", "Always call it"} {
		if !strings.Contains(story.Body, want) {
			t.Errorf("story-draft body does not mention %q", want)
		}
	}
}

func TestLoadSkillsOverrides(t *testing.T) {
	const overrideSkill = "---\nname: adr-split\ndescription: operator override\n---\noperator body\n"

	t.Run("absent directory keeps the embedded skills", func(t *testing.T) {
		s := newSkillsServer(t, filepath.Join(t.TempDir(), "no-such-dir"))
		skills, err := s.loadSkills()
		if err != nil {
			t.Fatalf("loadSkills: %v", err)
		}
		if _, ok := skillFixtureByName(skills, "adr-split"); !ok {
			t.Fatalf("skills = %v, want embedded adr-split", skills)
		}
	})

	t.Run("empty directory keeps the embedded skills", func(t *testing.T) {
		s := newSkillsServer(t, t.TempDir())
		skills, err := s.loadSkills()
		if err != nil {
			t.Fatalf("loadSkills: %v", err)
		}
		if _, ok := skillFixtureByName(skills, "adr-split"); !ok {
			t.Fatalf("skills = %v, want embedded adr-split", skills)
		}
	})

	t.Run("override replaces the embedded skill by name", func(t *testing.T) {
		dir := t.TempDir()
		// A different file name than the embedded one: the merge key is the
		// frontmatter name, not the path.
		writeSkillFixture(t, dir, "local-split.md", overrideSkill)
		s := newSkillsServer(t, dir)
		skills, err := s.loadSkills()
		if err != nil {
			t.Fatalf("loadSkills: %v", err)
		}
		adr, ok := skillFixtureByName(skills, "adr-split")
		if !ok {
			t.Fatalf("skills = %v, want adr-split", skills)
		}
		if adr.Description != "operator override" || adr.Body != "operator body" {
			t.Errorf("adr-split = %+v, want the override wholesale", adr)
		}
		var count int
		for _, skill := range skills {
			if skill.Name == "adr-split" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("adr-split appears %d times, want 1", count)
		}
	})

	t.Run("override adds a new skill alongside the embedded ones", func(t *testing.T) {
		dir := t.TempDir()
		writeSkillFixture(t, dir, "triage.md", "---\nname: triage\ndescription: sort the inbox\n---\ntriage body\n")
		s := newSkillsServer(t, dir)
		skills, err := s.loadSkills()
		if err != nil {
			t.Fatalf("loadSkills: %v", err)
		}
		if _, ok := skillFixtureByName(skills, "adr-split"); !ok {
			t.Fatalf("skills = %v, want embedded adr-split retained", skills)
		}
		triage, ok := skillFixtureByName(skills, "triage")
		if !ok {
			t.Fatalf("skills = %v, want triage", skills)
		}
		if triage.Body != "triage body" {
			t.Errorf("triage body = %q", triage.Body)
		}
	})

	t.Run("non-markdown files are ignored", func(t *testing.T) {
		dir := t.TempDir()
		writeSkillFixture(t, dir, "notes.txt", "not a skill at all")
		s := newSkillsServer(t, dir)
		if _, err := s.loadSkills(); err != nil {
			t.Fatalf("loadSkills: %v", err)
		}
	})

	t.Run("broken override is loud", func(t *testing.T) {
		tests := []struct {
			name    string
			content string
		}{
			{name: "no frontmatter", content: "just a body\n"},
			{name: "unterminated frontmatter", content: "---\nname: x\ndescription: y\n"},
			{name: "no name", content: "---\ndescription: y\n---\nbody\n"},
			{name: "no description", content: "---\nname: x\n---\nbody\n"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				dir := t.TempDir()
				writeSkillFixture(t, dir, "broken.md", test.content)
				s := newSkillsServer(t, dir)
				skills, err := s.loadSkills()
				if err == nil {
					t.Fatalf("loadSkills = %v, want an error", skills)
				}
				if skills != nil {
					t.Errorf("loadSkills returned %v alongside the error", skills)
				}
			})
		}
	})

	t.Run("skills dir that is a file is an error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "skills")
		if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		s := newSkillsServer(t, path)
		if skills, err := s.loadSkills(); err == nil {
			t.Fatalf("loadSkills = %v, want an error", skills)
		}
	})
}
