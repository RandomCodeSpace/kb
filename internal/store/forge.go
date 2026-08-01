package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var forgeSourceNameRE = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// ForgeSource is the client-visible view of a configured forge. The PAT is
// deliberately represented only by HasToken so list callers cannot expose it.
type ForgeSource struct {
	Name      string
	Kind      string
	BaseURL   string
	HasToken  bool
	CreatedAt time.Time
}

type forgeSourceInput struct {
	name    string
	kind    string
	baseURL *string
	pat     *string
}

type forgeSourceState struct {
	baseURL   string
	patEnc    []byte
	createdAt string
	exists    bool
}

// ForgeSources returns the scope's configured forges ordered by canonical name.
// It checks only whether ciphertext exists; listing never decrypts a PAT.
func (s *Store) ForgeSources(scope string) ([]ForgeSource, error) {
	rows, err := s.db.Query(`
SELECT name, kind, base_url, pat_enc, created_at
FROM forge_sources
WHERE scope = ?
ORDER BY name`, scope)
	if err != nil {
		return nil, fmt.Errorf("store: forge sources: %w", err)
	}
	sources := make([]ForgeSource, 0)
	for rows.Next() {
		var source ForgeSource
		var enc []byte
		var createdAt string
		if err := rows.Scan(&source.Name, &source.Kind, &source.BaseURL, &enc, &createdAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan forge source: %w", err)
		}
		source.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: parse forge source creation time: %w", err)
		}
		source.HasToken = len(enc) > 0
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: forge sources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close forge sources: %w", err)
	}
	for i := range sources {
		clean := stripURLSuffix(sources[i].BaseURL)
		if clean == sources[i].BaseURL {
			continue
		}
		if _, err := s.db.Exec(`UPDATE forge_sources SET base_url = ? WHERE scope = ? AND name = ?`, clean, scope, sources[i].Name); err != nil {
			return nil, fmt.Errorf("store: repair forge source URL: %w", err)
		}
		sources[i].BaseURL = clean
	}
	return sources, nil
}

// SetForgeSource creates or patches one scoped forge source. A nil baseURL or
// PAT keeps the stored value. An empty PAT explicitly clears it, while a
// non-empty PAT is sealed before storage.
//
// A stored credential may not follow a base URL to another origin. When the
// origin changes without a PAT in the same call, the old ciphertext is cleared
// atomically and tokenCleared reports that the caller must request a new PAT.
func (s *Store) SetForgeSource(scope, name, kind string, baseURL, pat *string) (tokenCleared bool, err error) {
	input, err := normalizeForgeSourceInput(name, kind, baseURL, pat)
	if err != nil {
		return false, err
	}

	err = s.withTx(func(tx *sql.Tx) error {
		state, err := loadForgeSourceState(tx, scope, input.name)
		if err != nil {
			return err
		}
		tokenCleared, err = s.patchForgeSourceState(&state, input)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(`
INSERT INTO forge_sources (scope, name, kind, base_url, pat_enc, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(scope, name) DO UPDATE SET
	kind = excluded.kind,
	base_url = excluded.base_url,
	pat_enc = excluded.pat_enc`,
			scope, input.name, input.kind, state.baseURL, state.patEnc, state.createdAt); err != nil {
			return fmt.Errorf("store: write forge source: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return tokenCleared, nil
}

func normalizeForgeSourceInput(name, kind string, baseURL, pat *string) (forgeSourceInput, error) {
	name, err := normalizeForgeSourceName(name)
	if err != nil {
		return forgeSourceInput{}, err
	}
	if kind != "gitlab" && kind != "github" {
		return forgeSourceInput{}, errors.New("store: invalid forge kind")
	}
	if baseURL == nil {
		return forgeSourceInput{name: name, kind: kind, pat: pat}, nil
	}
	base, err := normalizeForgeBaseURL(*baseURL)
	if err != nil {
		return forgeSourceInput{}, err
	}
	return forgeSourceInput{name: name, kind: kind, baseURL: &base, pat: pat}, nil
}

func loadForgeSourceState(tx *sql.Tx, scope, name string) (forgeSourceState, error) {
	state := forgeSourceState{exists: true}
	err := tx.QueryRow(`
SELECT base_url, pat_enc, created_at
FROM forge_sources
WHERE scope = ? AND name = ?`, scope, name).Scan(&state.baseURL, &state.patEnc, &state.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		state.exists = false
		return state, nil
	}
	if err != nil {
		return forgeSourceState{}, fmt.Errorf("store: read forge source: %w", err)
	}
	clean := stripURLSuffix(state.baseURL)
	if clean == state.baseURL {
		return state, nil
	}
	if _, err := tx.Exec(`UPDATE forge_sources SET base_url = ? WHERE scope = ? AND name = ?`, clean, scope, name); err != nil {
		return forgeSourceState{}, fmt.Errorf("store: repair forge source URL: %w", err)
	}
	state.baseURL = clean
	return state, nil
}

func (s *Store) patchForgeSourceState(state *forgeSourceState, input forgeSourceInput) (bool, error) {
	if !state.exists {
		if input.baseURL == nil {
			return false, errors.New("store: forge base URL is required")
		}
		state.createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	tokenCleared := patchForgeBaseURL(state, input.baseURL, input.pat)
	if err := s.patchForgePAT(state, input.pat); err != nil {
		return false, err
	}
	return tokenCleared, nil
}

func patchForgeBaseURL(state *forgeSourceState, baseURL, pat *string) bool {
	if baseURL == nil {
		return false
	}
	tokenCleared := pat == nil && len(state.patEnc) > 0 && !SameAIOrigin(state.baseURL, *baseURL)
	if tokenCleared {
		state.patEnc = nil
	}
	state.baseURL = *baseURL
	return tokenCleared
}

func (s *Store) patchForgePAT(state *forgeSourceState, pat *string) error {
	if pat == nil {
		return nil
	}
	if *pat == "" {
		state.patEnc = nil
		return nil
	}
	sealed, err := s.seal([]byte(*pat))
	if err != nil {
		return err
	}
	state.patEnc = sealed
	return nil
}

// DeleteForgeSource deletes one scoped source. Deleting a missing source is a
// successful no-op.
func (s *Store) DeleteForgeSource(scope, name string) error {
	name, err := normalizeForgeSourceName(name)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM forge_sources WHERE scope = ? AND name = ?`, scope, name); err != nil {
		return fmt.Errorf("store: delete forge source: %w", err)
	}
	return nil
}

// ForgePAT returns one source and its decrypted PAT. Missing sources and
// decryption failures return no partial values, and errors never include the
// URL, ciphertext, or plaintext credential.
func (s *Store) ForgePAT(scope, name string) (kind, baseURL, pat string, err error) {
	name, err = normalizeForgeSourceName(name)
	if err != nil {
		return "", "", "", err
	}

	var enc []byte
	switch err := s.db.QueryRow(`
SELECT kind, base_url, pat_enc
FROM forge_sources
WHERE scope = ? AND name = ?`, scope, name).Scan(&kind, &baseURL, &enc); {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", "", errors.New("store: forge source not found")
	case err != nil:
		return "", "", "", fmt.Errorf("store: forge PAT: %w", err)
	}
	clean := stripURLSuffix(baseURL)
	if clean != baseURL {
		if _, err := s.db.Exec(`UPDATE forge_sources SET base_url = ? WHERE scope = ? AND name = ?`, clean, scope, name); err != nil {
			return "", "", "", fmt.Errorf("store: repair forge source URL: %w", err)
		}
		baseURL = clean
	}
	if len(enc) == 0 {
		return kind, baseURL, "", nil
	}
	plain, err := s.openSealed(enc)
	if err != nil {
		return "", "", "", err
	}
	return kind, baseURL, string(plain), nil
}

func stripURLSuffix(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

func normalizeForgeSourceName(name string) (string, error) {
	name = strings.ToLower(name)
	if !forgeSourceNameRE.MatchString(name) {
		return "", errors.New("store: invalid forge source name")
	}
	return name, nil
}

func normalizeForgeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("store: invalid forge base URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("store: invalid forge base URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("store: forge base URL scheme must be http or https")
	}
	if u.User != nil {
		return "", errors.New("store: forge base URL must not contain userinfo")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("store: forge base URL must not contain query or fragment")
	}
	return u.String(), nil
}
