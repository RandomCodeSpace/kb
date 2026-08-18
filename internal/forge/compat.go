package forge

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/kb/internal/ai"
)

const (
	skillRunDeadline                   = 4 * time.Minute
	storageErrorMessage                = "storage error"
	configuredSourceUnavailableMessage = "configured source unavailable"
	connectionFailedMessage            = "connection failed"
)

func packImportIssues(issues []forgeIssue) (string, int) {
	var packed strings.Builder
	count := 0
	for index, issue := range issues {
		if packed.Len() >= maxImportPackBytes {
			break
		}
		comments := make([]string, 0, min(len(issue.Comments), 10))
		for _, comment := range issue.Comments {
			if len(comments) == 10 {
				break
			}
			comments = append(comments, truncateImportText(comment, maxImportCommentBytes))
		}
		section := fmt.Sprintf("Source %d\nTitle: %s\nRef: %s\nLabels: %s\nBody:\n%s\nComments:\n%s\n\n",
			index+1, issue.Title, issue.Ref, strings.Join(issue.Labels, ", "),
			truncateImportText(issue.Body, maxImportIssueBodyBytes), strings.Join(comments, "\n"))
		if len(section) > maxImportPackBytes-packed.Len() {
			break
		}
		packed.WriteString(section)
		count++
	}
	return packed.String(), count
}

func forgeIssueADR(issue forgeIssue) string {
	adr := fmt.Sprintf("# %s\n\n%s", issue.Title, issue.Body)
	discussion := "\n\n## Discussion"
	if len(adr)+len(discussion) > 64<<10 {
		return truncateImportText(adr, 64<<10)
	}
	adr += discussion
	for _, comment := range issue.Comments {
		item := "\n- " + comment
		remaining := 64<<10 - len(adr)
		if len(item) > remaining {
			return adr + truncateImportText(item, remaining)
		}
		adr += item
	}
	return adr
}

func truncateImportText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	for len(text) > limit {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return text
}

func skillBudget(value int64) int64 { return ai.SkillBudget(value) }

func logSafe(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}
