package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/rig"
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

func skillFixtureByName(skills []rig.Skill, name string) (rig.Skill, bool) {
	for _, skill := range skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return rig.Skill{}, false
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
