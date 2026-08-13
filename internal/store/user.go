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
// UserTasks pairs a board owner with their task count.
type UserTasks struct {
	User  string `json:"user"`
	Tasks int    `json:"tasks"`
}

// Users lists every board owner with their task count, sorted by name. A
// board that exists only as a saved title (no tasks yet) counts as zero,
// mirroring HasBoard's definition of existence.
func (s *Store) Users() ([]UserTasks, error) {
	rows, err := s.db.Query(`
		SELECT user, SUM(cnt) FROM (
			SELECT user, COUNT(*) AS cnt FROM tasks GROUP BY user
			UNION ALL
			SELECT substr(k, ?), 0 FROM meta WHERE substr(k, 1, ?) = ?
		) GROUP BY user ORDER BY user`,
		len(titleKey(""))+1, len(titleKey("")), titleKey(""))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserTasks
	for rows.Next() {
		var u UserTasks
		if err := rows.Scan(&u.User, &u.Tasks); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

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
