package ai

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

// Skill is one markdown instruction file with name and description
// frontmatter followed by the instruction body.
type Skill struct {
	Name        string
	Description string
	Body        string
}

const (
	frontmatterDelimiter  = "---"
	skillExtension        = ".md"
	loadSkillToolName     = "load_skill"
	advertiseSkillsHeader = "Available skills. Call the " + loadSkillToolName +
		" tool with a skill name to read its full instructions before acting on it."
)

// embeddedSkills carries the built-in skill instructions. Files must sit
// directly under skills/ and end in .md.
//
//go:embed skills/*.md
var embeddedSkills embed.FS

// LoadSkills returns the built-in skills with the operator's directory layered
// on top. An override replaces the built-in with the same frontmatter name.
// The operator directory is optional, but a malformed existing catalogue is a
// whole-catalogue failure.
func (r *Runner) LoadSkills() ([]Skill, error) {
	base, err := loadSkillCatalog(embeddedSkills, "skills")
	if err != nil {
		return nil, err
	}
	if r.skillsDir == "" {
		return base, nil
	}
	overrides, err := loadSkillCatalog(os.DirFS(r.skillsDir), ".")
	if err != nil {
		// Only the directory read wraps fs.ErrNotExist. A file removed during
		// the read still fails the catalogue instead of silently disappearing.
		if errors.Is(err, fs.ErrNotExist) {
			return base, nil
		}
		return nil, err
	}
	return mergeSkills(base, overrides), nil
}

func (r *Runner) loadSkills() ([]Skill, error) { return r.LoadSkills() }

// loadSkillCatalog reads direct-child markdown files from dir. It reports all
// malformed files and duplicate names together, and returns no partial result.
func loadSkillCatalog(fsys fs.FS, dir string) ([]Skill, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	var (
		skills   []Skill
		problems []string
		seen     = make(map[string]string)
	)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), skillExtension) {
			continue
		}
		name := path.Join(dir, entry.Name())
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		skill, err := parseSkill(data)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if first, duplicate := seen[skill.Name]; duplicate {
			problems = append(problems, fmt.Sprintf("%s: duplicate skill name %q, already defined by %s", name, skill.Name, first))
			continue
		}
		seen[skill.Name] = name
		skills = append(skills, skill)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("load skills: %s", strings.Join(problems, "; "))
	}

	sortSkills(skills)
	return skills, nil
}

func parseSkill(data []byte) (Skill, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return Skill{}, errors.New("missing frontmatter opening delimiter")
	}

	var skill Skill
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == frontmatterDelimiter {
			skill.Body = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			if skill.Name == "" {
				return Skill{}, errors.New("frontmatter has no name")
			}
			if skill.Description == "" {
				return Skill{}, errors.New("frontmatter has no description")
			}
			return skill, nil
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = unquoteSkillValue(strings.TrimSpace(value))
		switch strings.TrimSpace(key) {
		case "name":
			skill.Name = value
		case "description":
			skill.Description = value
		}
	}
	return Skill{}, errors.New("missing frontmatter closing delimiter")
}

func unquoteSkillValue(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') ||
		(value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

func mergeSkills(base, overrides []Skill) []Skill {
	byName := make(map[string]Skill, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	for _, catalogue := range [][]Skill{base, overrides} {
		for _, skill := range catalogue {
			if _, exists := byName[skill.Name]; !exists {
				order = append(order, skill.Name)
			}
			byName[skill.Name] = skill
		}
	}

	merged := make([]Skill, 0, len(order))
	for _, name := range order {
		merged = append(merged, byName[name])
	}
	sortSkills(merged)
	return merged
}

func sortSkills(skills []Skill) {
	slices.SortFunc(skills, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
}

func advertiseSkills(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	sorted := slices.Clone(skills)
	sortSkills(sorted)

	var b strings.Builder
	b.WriteString(advertiseSkillsHeader)
	b.WriteString("\n\n")
	for i, skill := range sorted {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s: %s", skill.Name, strings.Join(strings.Fields(skill.Description), " "))
	}
	return b.String()
}
