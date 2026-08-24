// Package project carries the project vocabulary every kb surface shares.
//
// Every task carries exactly one project, spelled as the scoped label
// "project::<name>". Projects slice one board rather than splitting it into
// several: the label is ordinary task data, so nothing about the schema, the
// wire format, or the server changes.
//
// The rules live here rather than in one surface because more than one surface
// writes tasks: the CLI resolves an active project and stamps it, and the TUI
// switches, filters and edits by it. A second implementation of "exactly one
// project:: label" is a second chance to get it wrong.
package project

import (
	"errors"
	"fmt"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/board"
)

const (
	// Scope is the label scope projects live under.
	Scope = "project"
	// LabelPrefix is what a project label starts with.
	LabelPrefix = Scope + "::"
	// Inbox is where tasks that never named a project end up.
	Inbox = "inbox"
)

// ValidateName checks a project name by the rule already applied to label
// values: non-empty, no whitespace, no leading '#'. It additionally rejects
// "::" so a name can never smuggle a second scope into the label.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("project name must not be empty")
	case board.ContainsSpace(name):
		return "", fmt.Errorf("invalid project name %q: must not contain whitespace", name)
	case strings.HasPrefix(name, "#"):
		return "", fmt.Errorf("invalid project name %q: must not start with '#'", name)
	case strings.Contains(name, "::"):
		return "", fmt.Errorf("invalid project name %q: must not contain %q", name, "::")
	}
	return name, nil
}

// Label renders the scoped label for a project name.
func Label(name string) string { return LabelPrefix + name }

// SplitTags separates the project names a tag list carries from the tags that
// are not project labels, both in their original order.
func SplitTags(tags []string) (projects, rest []string) {
	for _, tag := range tags {
		if name, ok := strings.CutPrefix(tag, LabelPrefix); ok {
			projects = append(projects, name)
			continue
		}
		rest = append(rest, tag)
	}
	return projects, rest
}

// Of returns the single project a tag list carries, or "" when it carries none
// or more than one — both of which a write path then replaces.
func Of(tags []string) string {
	named, _ := SplitTags(tags)
	if len(named) != 1 {
		return ""
	}
	return named[0]
}

// Ensure returns tags carrying exactly one project:: label: whatever project
// labels were in tags are dropped and name's label is appended. It is the
// invariant every write surface funnels through, so a task can never reach the
// store with zero or two projects.
func Ensure(tags []string, name string) ([]string, error) {
	name, err := ValidateName(name)
	if err != nil {
		return nil, err
	}
	_, rest := SplitTags(tags)
	return append(rest, Label(name)), nil
}

// Lead reorders a tag list so the project label comes first, leaving every
// other tag in its original order. The project is the card's loudest label, and
// the label row is rendered in survival order, so leading with it is what keeps
// it on the card when the row runs out of width.
func Lead(tags []string) []string {
	named, rest := SplitTags(tags)
	if len(named) == 0 {
		return append([]string(nil), tags...)
	}
	out := make([]string, 0, len(tags))
	for _, name := range named {
		out = append(out, Label(name))
	}
	return append(out, rest...)
}
