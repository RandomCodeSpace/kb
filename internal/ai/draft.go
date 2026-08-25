package ai

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// Draft is one validated card proposal returned by a skill run.
type Draft struct {
	Title  string       `json:"title"`
	Emoji  string       `json:"emoji"`
	Desc   string       `json:"desc"`
	Prio   int          `json:"prio"`
	Due    string       `json:"due"`
	Effort string       `json:"effort"`
	Tags   []string     `json:"tags"`
	Checks []DraftCheck `json:"checks"`
	Source int          `json:"-"`
}

type DraftCheck struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

var draftDueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validateDraft(d Draft) error {
	return store.ValidateTaskFields(board.Task{Title: d.Title, Emoji: d.Emoji, Desc: d.Desc, Due: d.Due, Effort: d.Effort, Prio: d.Prio, Tags: d.Tags})
}

func ValidateDraft(d Draft) error { return validateDraft(d) }

func coerceDraftMap(m map[string]any) Draft {
	d := Draft{Prio: 3, Tags: []string{}, Checks: []DraftCheck{}}
	if v, ok := m["title"].(string); ok {
		d.Title = strings.TrimSpace(stripControl(v))
	}
	if v, ok := m["emoji"].(string); ok {
		d.Emoji = board.LeadingEmoji(strings.TrimSpace(v))
	}
	if v, ok := m["desc"].(string); ok {
		d.Desc = stripControlKeepLines(v)
	}
	switch v := m["prio"].(type) {
	case float64:
		d.Prio = clampPrio(int(v))
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			d.Prio = clampPrio(n)
		}
	}
	if v, ok := m["due"].(string); ok {
		due := strings.TrimSpace(v)
		if draftDueRe.MatchString(due) {
			if _, err := time.Parse("2006-01-02", due); err == nil {
				d.Due = due
			}
		}
	}
	if v, ok := m["effort"].(string); ok {
		switch effort := strings.ToUpper(strings.TrimSpace(v)); effort {
		case "S", "M", "L":
			d.Effort = effort
		}
	}
	if values, ok := m["tags"].([]any); ok {
		for _, value := range values {
			tag, ok := value.(string)
			if !ok {
				continue
			}
			tag = strings.TrimSpace(stripControl(tag))
			if tag != "" && !board.ContainsSpace(tag) && tag[0] != '#' {
				d.Tags = append(d.Tags, tag)
			}
		}
	}
	if values, ok := m["checks"].([]any); ok {
		for _, value := range values {
			check, ok := value.(map[string]any)
			if !ok {
				continue
			}
			text, _ := check["text"].(string)
			text = strings.TrimSpace(stripControl(text))
			if text == "" {
				continue
			}
			done, _ := check["done"].(bool)
			d.Checks = append(d.Checks, DraftCheck{Text: text, Done: done})
		}
	}
	if source, ok := m["source"].(float64); ok && source >= 1 && source == float64(int(source)) {
		d.Source = int(source)
	}
	return d
}

func CoerceDraft(m map[string]any) Draft { return coerceDraftMap(m) }

// clampPrio pins a model-proposed priority onto the three-value scale
// (issue #234). A draft is untrusted generated JSON, so this clamps rather
// than rejects: a model that overshoots the scale still produces a usable
// card. Overshooting low now lands on 3 rather than the retired 4.
func clampPrio(p int) int {
	if p < board.PrioHigh {
		return board.PrioHigh
	}
	if p > board.PrioLow {
		return board.PrioLow
	}
	return p
}

func ClampPriority(p int) int { return clampPrio(p) }

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

func NormalizeStoryCount(max int) int { return normalizeStoryCount(max) }

func logSafe(s string) string {
	return stripControl(strings.ReplaceAll(strings.ReplaceAll(s, "\n", ""), "\r", ""))
}

func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func stripControlKeepLines(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
