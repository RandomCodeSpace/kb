package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SimilarHit is a cheap card or import-provenance match.
type SimilarHit struct {
	ID       string
	Title    string
	Status   string
	Via      string
	Link     string
	Reason   string
	KilledAt string
}

// Tombstone records why a task was moved to the cancelled column.
type Tombstone struct {
	TaskID   string
	Reason   string
	KilledAt string
}

// SimilarityFloor rejects FTS candidates sharing too little title vocabulary.
const SimilarityFloor = 0.34

const maxSimilarExcludeLinks = 100

func searchTokenBoundary(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

// FtsQuery converts untrusted text to a bounded OR of literal FTS phrases.
func FtsQuery(raw string) string {
	tokens := strings.FieldsFunc(raw, searchTokenBoundary)
	if len(tokens) > 12 {
		tokens = tokens[:12]
	}
	for i, token := range tokens {
		tokens[i] = `"` + strings.ReplaceAll(token, `"`, `""`) + `"`
	}
	return strings.Join(tokens, " OR ")
}

// Similarity is the Sorensen-Dice coefficient over normalized token sets.
// It is pure and deterministic so the duplicate threshold can be tested
// without a database.
func Similarity(a, b string) float64 {
	aSet := similarityTokenSet(a)
	bSet := similarityTokenSet(b)
	if len(aSet) == 0 || len(bSet) == 0 {
		return 0
	}
	overlap := 0
	for token := range aSet {
		if _, ok := bSet[token]; ok {
			overlap++
		}
	}
	return 2 * float64(overlap) / float64(len(aSet)+len(bSet))
}

func similarityTokenSet(raw string) map[string]struct{} {
	tokens := normalizedSimilarityTokens(raw)
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[token] = struct{}{}
	}
	return set
}

func normalizedSimilarityTokens(raw string) []string {
	tokens := strings.FieldsFunc(strings.ToLower(raw), searchTokenBoundary)
	normalized := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimFunc(token, unicode.IsPunct)
		if utf8.RuneCountInString(token) < 3 {
			continue
		}
		normalized = append(normalized, token)
	}
	return normalized
}

func normalizedSimilarityText(raw string) string {
	return strings.Join(normalizedSimilarityTokens(raw), " ")
}

type scoredSimilarHit struct {
	hit   SimilarHit
	score float64
}

// SearchSimilar returns scoped card and import-provenance hits ranked by title
// similarity.
func (s *Store) SearchSimilar(scope, query, excludeID string, excludeLinks []string, limit int) ([]SimilarHit, error) {
	match := FtsQuery(query)
	if match == "" || limit <= 0 {
		return nil, nil
	}
	if len(excludeLinks) > maxSimilarExcludeLinks {
		excludeLinks = excludeLinks[:maxSimilarExcludeLinks]
	}
	excludedLinks := make(map[string]struct{}, len(excludeLinks))
	for _, link := range excludeLinks {
		excludedLinks[link] = struct{}{}
	}
	candidateLimit := limit * 8
	if candidateLimit < 24 {
		candidateLimit = 24
	}
	normalizedQuery := normalizedSimilarityText(query)
	var candidates []scoredSimilarHit
	keepCandidate := func(hit SimilarHit) {
		score := Similarity(query, hit.Title)
		exactPhrase := normalizedQuery != "" &&
			strings.Contains(normalizedSimilarityText(hit.Title), normalizedQuery)
		if score < SimilarityFloor && !exactPhrase {
			return
		}
		candidates = append(candidates, scoredSimilarHit{hit: hit, score: score})
	}

	rows, err := s.db.Query(`
		SELECT f.id, f.title, t.status,
		       COALESCE(g.reason, ''), COALESCE(g.killed_at, '')
	FROM tasks_fts f
	JOIN tasks t ON t.id = f.id AND t.user = ?1
		LEFT JOIN tombstones g ON g.scope = ?1 AND g.task_id = f.id
		WHERE tasks_fts MATCH ?2 AND f.scope = ?1 AND f.id <> ?3
		ORDER BY bm25(tasks_fts, 5.0, 1.0, 3.0) LIMIT ?4`,
		scope, match, excludeID, candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("store: search tasks: %w", err)
	}
	for rows.Next() {
		var hit SimilarHit
		if err := rows.Scan(&hit.ID, &hit.Title, &hit.Status, &hit.Reason, &hit.KilledAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan task search: %w", err)
		}
		if hit.Status == "cancelled" && hit.Reason != "" {
			hit.Via = "killed"
		} else {
			hit.Via = "card"
			hit.Reason, hit.KilledAt = "", ""
		}
		keepCandidate(hit)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: search tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close task search: %w", err)
	}

	rows, err = s.db.Query(`
		SELECT f.title, l.link
		FROM import_links_fts f
		JOIN import_links l ON l.scope = f.scope AND l.external_key = f.external_key
		WHERE import_links_fts MATCH ?2 AND f.scope = ?1
		ORDER BY bm25(import_links_fts, 5.0) LIMIT ?3`,
		scope, match, candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("store: search imports: %w", err)
	}
	for rows.Next() {
		var hit SimilarHit
		if err := rows.Scan(&hit.Title, &hit.Link); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan import search: %w", err)
		}
		hit.Via = "import"
		if _, excluded := excludedLinks[hit.Link]; excluded {
			continue
		}
		keepCandidate(hit)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: search imports: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close import search: %w", err)
	}

	// Stable sorting preserves each FTS query's bm25 order, and card-first
	// behavior across query branches, when similarity scores tie.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	var hits []SimilarHit
	for _, candidate := range candidates {
		hits = append(hits, candidate.hit)
	}
	return hits, nil
}

// RecordTombstone inserts or refreshes the reason a task was killed.
func (s *Store) RecordTombstone(scope, taskID, reason string) error {
	if err := validateTombstoneReason(reason); err != nil {
		return err
	}
	killedAt := time.Now().UTC().Format(time.RFC3339Nano)
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
	INSERT INTO tombstones (scope, task_id, reason, killed_at)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(scope, task_id) DO UPDATE SET
		reason = excluded.reason,
		killed_at = excluded.killed_at`,
			scope, taskID, reason, killedAt); err != nil {
			return fmt.Errorf("store: record tombstone: %w", err)
		}
		if _, err := tx.Exec(`
	DELETE FROM tombstones
	WHERE scope = ? AND task_id NOT IN (
		SELECT id FROM tasks WHERE user = ?
	)`, scope, scope); err != nil {
			return fmt.Errorf("store: sweep tombstones: %w", err)
		}
		return nil
	})
}

// Tombstone returns the scoped graveyard reason for taskID when one exists.
func (s *Store) Tombstone(scope, taskID string) (Tombstone, bool, error) {
	var tombstone Tombstone
	err := s.db.QueryRow(`
	SELECT task_id, reason, killed_at
	FROM tombstones
	WHERE scope = ? AND task_id = ?`,
		scope, taskID,
	).Scan(&tombstone.TaskID, &tombstone.Reason, &tombstone.KilledAt)
	switch {
	case err == nil:
		return tombstone, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return Tombstone{}, false, nil
	default:
		return Tombstone{}, false, fmt.Errorf("store: read tombstone: %w", err)
	}
}

// DeleteTombstone removes one scoped graveyard reason when it exists.
func (s *Store) DeleteTombstone(scope, taskID string) error {
	if _, err := s.db.Exec(
		`DELETE FROM tombstones WHERE scope = ? AND task_id = ?`,
		scope, taskID,
	); err != nil {
		return fmt.Errorf("store: delete tombstone: %w", err)
	}
	return nil
}

func validateTombstoneReason(reason string) error {
	if reason == "" {
		return errors.New("store: tombstone reason must not be empty")
	}
	if len(reason) > 2000 {
		return errors.New("store: tombstone reason exceeds 2000 bytes")
	}
	if strings.ContainsAny(reason, "\r\n") {
		return errors.New("store: tombstone reason contains a line break")
	}
	return nil
}

// TasksByLink returns cards carrying link as one complete tag.
func (s *Store) TasksByLink(scope, link string) ([]SimilarHit, error) {
	rows, err := s.db.Query(`
SELECT id, title, status
FROM tasks
WHERE user = ? AND EXISTS (
	SELECT 1 FROM json_each(tasks.tags) WHERE json_each.value = ?
)
ORDER BY `+statusRank+`, position, id LIMIT 10`, scope, link)
	if err != nil {
		return nil, fmt.Errorf("store: tasks by link: %w", err)
	}
	var hits []SimilarHit
	for rows.Next() {
		var hit SimilarHit
		if err := rows.Scan(&hit.ID, &hit.Title, &hit.Status); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan task by link: %w", err)
		}
		hit.Via, hit.Link = "card", link
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: tasks by link: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close tasks by link: %w", err)
	}
	return hits, nil
}

// ImportLink is the durable provenance recorded for an imported forge item.
type ImportLink struct {
	Source      string
	Kind        string
	ExternalKey string
	Link        string
	URL         string
	Title       string
}

// ImportBaseline records what an imported item looked like when last checked.
type ImportBaseline struct{ Title, Hash, Excerpt, At string }

const maxImportBaselineExcerptBytes = 8 << 10

// NewImportBaseline hashes the complete body before keeping a bounded,
// rune-safe excerpt, so exact comparison does not depend on lossy storage.
func NewImportBaseline(title, body, at string) ImportBaseline {
	sum := sha256.Sum256([]byte(body))
	excerpt := body
	for len(excerpt) > maxImportBaselineExcerptBytes {
		_, size := utf8.DecodeLastRuneInString(excerpt)
		excerpt = excerpt[:len(excerpt)-size]
	}
	return ImportBaseline{
		Title:   title,
		Hash:    hex.EncodeToString(sum[:]),
		Excerpt: excerpt,
		At:      at,
	}
}

// ImportBaseline returns the scoped baseline for externalKey when one exists.
func (s *Store) ImportBaseline(scope, externalKey string) (ImportBaseline, bool, error) {
	var baseline ImportBaseline
	err := s.db.QueryRow(`
SELECT baseline_title, baseline_hash, baseline_excerpt, baseline_at
FROM import_links
WHERE scope = ? AND external_key = ?`, scope, externalKey).Scan(
		&baseline.Title, &baseline.Hash, &baseline.Excerpt, &baseline.At,
	)
	switch {
	case err == nil:
		return baseline, baseline != (ImportBaseline{}), nil
	case errors.Is(err, sql.ErrNoRows):
		return ImportBaseline{}, false, nil
	default:
		return ImportBaseline{}, false, fmt.Errorf("store: read import baseline: %w", err)
	}
}

// SetImportBaseline updates one existing scoped import baseline.
func (s *Store) SetImportBaseline(scope, externalKey string, baseline ImportBaseline) error {
	if err := validateImportBaseline(baseline); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
UPDATE import_links
SET baseline_title = ?, baseline_hash = ?, baseline_excerpt = ?, baseline_at = ?
WHERE scope = ? AND external_key = ?`,
		baseline.Title, baseline.Hash, baseline.Excerpt, baseline.At, scope, externalKey); err != nil {
		return fmt.Errorf("store: set import baseline: %w", err)
	}
	return nil
}

func validateImportBaseline(baseline ImportBaseline) error {
	if len(baseline.Excerpt) > maxImportBaselineExcerptBytes {
		return errors.New("store: import baseline excerpt exceeds 8192 bytes")
	}
	for name, value := range map[string]string{
		"title": baseline.Title,
		"hash":  baseline.Hash,
	} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("store: import baseline %s contains a line break", name)
		}
	}
	return nil
}

// ImportedAs returns provenance rows keyed by external key.
func (s *Store) ImportedAs(scope string, externalKeys []string) (map[string]ImportLink, error) {
	found := make(map[string]ImportLink)
	if len(externalKeys) == 0 {
		return found, nil
	}
	placeholders := make([]string, len(externalKeys))
	args := make([]any, 1, len(externalKeys)+1)
	args[0] = scope
	for i, key := range externalKeys {
		placeholders[i] = "?"
		args = append(args, key)
	}
	rows, err := s.db.Query(`
SELECT source, kind, external_key, link, url, title
FROM import_links
WHERE scope = ? AND external_key IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: imported as: %w", err)
	}
	for rows.Next() {
		var link ImportLink
		if err := rows.Scan(&link.Source, &link.Kind, &link.ExternalKey, &link.Link, &link.URL, &link.Title); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan import link: %w", err)
		}
		found[link.ExternalKey] = link
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: imported as: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close imported as: %w", err)
	}
	return found, nil
}

// ImportLinksByLink returns every scoped provenance row carrying an exact link.
func (s *Store) ImportLinksByLink(scope, link string) ([]ImportLink, error) {
	rows, err := s.db.Query(`
SELECT source, kind, external_key, link, url, title
FROM import_links
WHERE scope = ? AND link = ?
ORDER BY source, external_key`, scope, link)
	if err != nil {
		return nil, fmt.Errorf("store: import links by link: %w", err)
	}
	var found []ImportLink
	for rows.Next() {
		var imported ImportLink
		if err := rows.Scan(&imported.Source, &imported.Kind, &imported.ExternalKey, &imported.Link, &imported.URL, &imported.Title); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan import links by link: %w", err)
		}
		found = append(found, imported)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: import links by link: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close import links by link: %w", err)
	}
	return found, nil
}

// RecordImportLinks atomically inserts or refreshes import provenance.
func (s *Store) RecordImportLinks(scope string, links []ImportLink) error {
	for _, link := range links {
		if err := validateImportLink(link); err != nil {
			return err
		}
	}
	importedAt := time.Now().UTC().Format(time.RFC3339Nano)
	return s.withTx(func(tx *sql.Tx) error {
		for _, link := range links {
			if _, err := tx.Exec(`
INSERT INTO import_links (scope, source, kind, external_key, link, url, title, imported_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scope, external_key) DO UPDATE SET
	source = excluded.source,
	kind = excluded.kind,
	link = excluded.link,
	url = excluded.url,
	title = excluded.title,
	imported_at = excluded.imported_at`,
				scope, link.Source, link.Kind, link.ExternalKey, link.Link, link.URL, link.Title, importedAt); err != nil {
				return fmt.Errorf("store: record import link %q: %w", link.ExternalKey, err)
			}
		}
		return nil
	})
}

func validateImportLink(link ImportLink) error {
	if len(link.ExternalKey) > 2048 {
		return fmt.Errorf("store: import external key exceeds 2048 bytes")
	}
	if len(link.URL) > 2048 {
		return fmt.Errorf("store: import URL exceeds 2048 bytes")
	}
	if len(link.Title) > 500 {
		return fmt.Errorf("store: import title exceeds 500 bytes")
	}
	for name, value := range map[string]string{
		"source": link.Source, "kind": link.Kind, "external key": link.ExternalKey,
		"link": link.Link, "URL": link.URL, "title": link.Title,
	} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("store: import %s contains a line break", name)
		}
	}
	return nil
}
