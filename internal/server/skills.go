package server

import (
	"embed"
	"errors"
	"io/fs"
	"os"

	"github.com/RandomCodeSpace/rig"
)

// embeddedSkills carries the built-in skill instructions. Files must sit
// directly under skills/ and end in .md: rig does not walk subdirectories.
//
//go:embed skills/*.md
var embeddedSkills embed.FS

// loadSkills returns the built-in skills with the operator's directory layered
// on top, an override replacing the built-in of the same name. An absent
// Config.SkillsDir is not an error, the directory is optional; a directory
// that exists but holds an unparseable skill file is, because a skill that
// silently vanishes from the advertisement is worse than a failed request.
func (s *server) loadSkills() ([]rig.Skill, error) {
	base, err := rig.LoadSkills(embeddedSkills, "skills")
	if err != nil {
		return nil, err
	}
	if s.cfg.SkillsDir == "" {
		return base, nil
	}
	overrides, err := rig.LoadSkills(os.DirFS(s.cfg.SkillsDir), ".")
	if err != nil {
		// Only the directory read reports fs.ErrNotExist through %w; rig
		// formats per-file problems into an unwrapped message, so a deleted
		// file mid-read still surfaces as an error rather than as no
		// overrides.
		if errors.Is(err, fs.ErrNotExist) {
			return base, nil
		}
		return nil, err
	}
	return rig.MergeSkills(base, overrides), nil
}
