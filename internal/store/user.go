package store

import (
	"errors"
	"fmt"
	"strings"
)

// SanitizeUser maps a user identity to the safe storage key every surface
// (HTTP server, CLI, MCP) must share: lowercased, and any char outside
// [a-z0-9._@-] is rejected (never substituted — substitution would collapse
// distinct identities onto the same board). Empty identities, names starting
// with '.', and over-long names are rejected. Path separators are outside
// the allowed set, so traversal is impossible.
func SanitizeUser(user string) (string, error) {
	if user == "" {
		return "", errors.New("empty user identity")
	}
	out := strings.ToLower(user)
	for _, r := range out {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '@', r == '-':
		default:
			return "", fmt.Errorf("invalid character in user identity %q", user)
		}
	}
	if strings.HasPrefix(out, ".") {
		return "", fmt.Errorf("invalid user identity %q", user)
	}
	if len(out) > 250 {
		return "", errors.New("user identity too long")
	}
	return out, nil
}
