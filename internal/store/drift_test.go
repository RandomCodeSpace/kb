package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func recordBaselineLink(t *testing.T, s *Store, scope, externalKey string) {
	t.Helper()
	if err := s.RecordImportLinks(scope, []ImportLink{{
		Source: "forge.example", Kind: "gitlab", ExternalKey: externalKey,
		Link: "link::" + externalKey, URL: "https://forge.example/" + externalKey, Title: "Imported item",
	}}); err != nil {
		t.Fatalf("RecordImportLinks: %v", err)
	}
}

// TestNewImportBaselineHashesTheFullBodyBeforeRuneSafeTruncation prevents the
// lossy excerpt cap from weakening exact drift detection or corrupting UTF-8.
func TestNewImportBaselineHashesTheFullBodyBeforeRuneSafeTruncation(t *testing.T) {
	body := strings.Repeat("a", maxImportBaselineExcerptBytes-1) + "界"
	got := NewImportBaseline("Upstream title", body, "2026-07-29T12:00:00Z")
	fullSum := sha256.Sum256([]byte(body))
	excerptSum := sha256.Sum256([]byte(got.Excerpt))

	if got.Title != "Upstream title" || got.At != "2026-07-29T12:00:00Z" {
		t.Fatalf("NewImportBaseline metadata = (%q, %q), want title and observation time", got.Title, got.At)
	}
	if len(got.Excerpt) > maxImportBaselineExcerptBytes || !utf8.ValidString(got.Excerpt) {
		t.Fatalf("excerpt is %d bytes and valid=%t, want at most %d valid UTF-8 bytes",
			len(got.Excerpt), utf8.ValidString(got.Excerpt), maxImportBaselineExcerptBytes)
	}
	if got.Excerpt != strings.Repeat("a", maxImportBaselineExcerptBytes-1) {
		t.Fatalf("excerpt ended at a partial rune: %q", got.Excerpt[len(got.Excerpt)-4:])
	}
	if got.Hash != hex.EncodeToString(fullSum[:]) || got.Hash == hex.EncodeToString(excerptSum[:]) {
		t.Fatalf("hash = %q, want the full-body SHA-256 before truncation", got.Hash)
	}
}

// TestImportBaselineRoundTripsAllFields prevents a stored comparison point
// from losing the title, exact hash, lossy excerpt, or observation time.
func TestImportBaselineRoundTripsAllFields(t *testing.T) {
	s := newStore(t)
	const externalKey = "gitlab:forge.example/acme/app#12"
	recordBaselineLink(t, s, "alice", externalKey)

	want := ImportBaseline{
		Title: "Fix the migrator", Hash: "abc123", Excerpt: "first lines", At: "2026-07-29T12:00:00Z",
	}
	if err := s.SetImportBaseline("alice", externalKey, want); err != nil {
		t.Fatalf("SetImportBaseline: %v", err)
	}
	got, found, err := s.ImportBaseline("alice", externalKey)
	if err != nil {
		t.Fatalf("ImportBaseline: %v", err)
	}
	if !found || got != want {
		t.Fatalf("ImportBaseline = (%+v, %t), want (%+v, true)", got, found, want)
	}
}

// TestImportBaselineTreatsMigratedEmptyColumnsAsAbsent prevents a phase-7
// provenance row from being mistaken for a comparison the server never made.
func TestImportBaselineTreatsMigratedEmptyColumnsAsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatalf("create meta: %v", err)
	}
	for version, migration := range migrations[:3] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply migration v%d: %v", version+1, err)
		}
		if _, err := db.Exec(`INSERT INTO meta (k, v) VALUES ('schema_version', ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, version+1); err != nil {
			t.Fatalf("record migration v%d: %v", version+1, err)
		}
	}
	if _, err := db.Exec(`
INSERT INTO import_links (scope, source, kind, external_key, link, url, title, imported_at)
VALUES ('alice', 'forge.example', 'gitlab', 'gitlab:forge.example/acme/app#12',
        'link::gitlab:forge.example/acme/app#12', 'https://forge.example/acme/app/-/issues/12', 'Imported item', '2026-07-29T12:00:00Z')`); err != nil {
		t.Fatalf("seed v3 import: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v3 database: %v", err)
	}

	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	got, found, err := s.ImportBaseline("alice", "gitlab:forge.example/acme/app#12")
	if err != nil {
		t.Fatalf("ImportBaseline: %v", err)
	}
	if found || got != (ImportBaseline{}) {
		t.Fatalf("ImportBaseline migrated v3 row = (%+v, %t), want (zero, false)", got, found)
	}
}

// TestSetImportBaselineLeavesMissingKeysAbsent prevents a stale request from
// recreating provenance that no longer exists.
func TestSetImportBaselineLeavesMissingKeysAbsent(t *testing.T) {
	s := newStore(t)
	if err := s.SetImportBaseline("alice", "missing", ImportBaseline{Title: "missing"}); err != nil {
		t.Fatalf("SetImportBaseline missing key: %v", err)
	}
	if _, found, err := s.ImportBaseline("alice", "missing"); err != nil || found {
		t.Fatalf("ImportBaseline missing key = (found %t, err %v), want (false, nil)", found, err)
	}
}

// TestSetImportBaselineRejectsInvalidInputBeforeWriting prevents oversized
// excerpts or forged single-line fields from replacing a valid baseline.
func TestSetImportBaselineRejectsInvalidInputBeforeWriting(t *testing.T) {
	tests := []ImportBaseline{
		{Excerpt: strings.Repeat("x", maxImportBaselineExcerptBytes+1)},
		{Title: "title\nbreak"},
		{Title: "title\rbreak"},
		{Hash: "hash\nbreak"},
		{Hash: "hash\rbreak"},
	}
	for i, baseline := range tests {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			s := newStore(t)
			recordBaselineLink(t, s, "alice", "key")
			previous := ImportBaseline{
				Title: "Previous title", Hash: "previous-hash",
				Excerpt: "Previous excerpt", At: "2026-07-29T12:00:00Z",
			}
			if err := s.SetImportBaseline("alice", "key", previous); err != nil {
				t.Fatalf("seed baseline: %v", err)
			}
			if err := s.SetImportBaseline("alice", "key", baseline); err == nil {
				t.Fatal("SetImportBaseline accepted invalid baseline")
			}
			if got, found, err := s.ImportBaseline("alice", "key"); err != nil || !found || got != previous {
				t.Fatalf("ImportBaseline after rejected input = (%+v, %t, %v), want (%+v, true, nil)",
					got, found, err, previous)
			}
		})
	}
}

// TestImportBaselinesNeverCrossScopes prevents equal forge keys from becoming
// a side channel between users now or future team scopes.
func TestImportBaselinesNeverCrossScopes(t *testing.T) {
	s := newStore(t)
	const externalKey = "gitlab:forge.example/acme/app#12"
	recordBaselineLink(t, s, "alice", externalKey)
	recordBaselineLink(t, s, "bob", externalKey)
	wants := map[string]ImportBaseline{
		"alice": {Title: "Alice item", Hash: "alice-hash", Excerpt: "alice excerpt", At: "2026-07-29T12:00:00Z"},
		"bob":   {Title: "Bob item", Hash: "bob-hash", Excerpt: "bob excerpt", At: "2026-07-29T13:00:00Z"},
	}
	for scope, want := range wants {
		if err := s.SetImportBaseline(scope, externalKey, want); err != nil {
			t.Fatalf("SetImportBaseline %s: %v", scope, err)
		}
	}
	for scope, want := range wants {
		got, found, err := s.ImportBaseline(scope, externalKey)
		if err != nil || !found || got != want {
			t.Fatalf("ImportBaseline %s = (%+v, %t, %v), want (%+v, true, nil)",
				scope, got, found, err, want)
		}
	}
}
