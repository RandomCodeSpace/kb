package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// SimilarHit is a cheap card or import-provenance match.
type SimilarHit struct {
	ID     string
	Title  string
	Status string
	Via    string
	Link   string
}

// FtsQuery converts untrusted text to a bounded OR of literal FTS phrases.
func FtsQuery(raw string) string {
	tokens := strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
	if len(tokens) > 12 {
		tokens = tokens[:12]
	}
	for i, token := range tokens {
		tokens[i] = `"` + strings.ReplaceAll(token, `"`, `""`) + `"`
	}
	return strings.Join(tokens, " OR ")
}

// SearchSimilar returns scoped card hits first, then import-provenance hits.
func (s *Store) SearchSimilar(scope, query, excludeID string, limit int) ([]SimilarHit, error) {
	match := FtsQuery(query)
	if match == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
SELECT f.id, f.title, t.status
FROM tasks_fts f JOIN tasks t ON t.id = f.id AND t.user = ?1
WHERE tasks_fts MATCH ?2 AND f.scope = ?1 AND f.id <> ?3
ORDER BY bm25(tasks_fts, 5.0, 1.0, 3.0) LIMIT ?4`,
		scope, match, excludeID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search tasks: %w", err)
	}
	var hits []SimilarHit
	for rows.Next() {
		var hit SimilarHit
		if err := rows.Scan(&hit.ID, &hit.Title, &hit.Status); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan task search: %w", err)
		}
		hit.Via = "card"
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: search tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close task search: %w", err)
	}
	if len(hits) == limit {
		return hits, nil
	}

	rows, err = s.db.Query(`
SELECT f.title, l.link
FROM import_links_fts f
JOIN import_links l ON l.scope = f.scope AND l.external_key = f.external_key
WHERE import_links_fts MATCH ?2 AND f.scope = ?1
ORDER BY bm25(import_links_fts, 5.0) LIMIT ?3`,
		scope, match, limit-len(hits))
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
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: search imports: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close import search: %w", err)
	}
	return hits, nil
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
