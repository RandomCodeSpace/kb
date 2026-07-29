package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestImportLinksUpsertRefreshesAllFieldsAndSupportsBatchLookup(t *testing.T) {
	s := newStore(t)
	first := ImportLink{
		Source: "gitlab.com", Kind: "gitlab", ExternalKey: "gitlab.com/acme/app#12",
		Link: "link::gitlab#12", URL: "https://gitlab.com/acme/app/-/issues/12", Title: "oldunique import title",
	}
	second := ImportLink{
		Source: "github.com", Kind: "github", ExternalKey: "github.com/acme/app#13",
		Link: "link::github#13", URL: "https://github.com/acme/app/issues/13", Title: "second import title",
	}
	if err := s.RecordImportLinks("alice", []ImportLink{first, second}); err != nil {
		t.Fatalf("RecordImportLinks initial: %v", err)
	}
	var importedAt, refreshedAt string
	if err := s.db.QueryRow(`SELECT imported_at FROM import_links WHERE scope = ? AND external_key = ?`, "alice", first.ExternalKey).Scan(&importedAt); err != nil {
		t.Fatalf("read initial imported_at: %v", err)
	}
	updated := ImportLink{
		Source: "enterprise", Kind: "github", ExternalKey: first.ExternalKey,
		Link: "link::github#99", URL: "https://forge.example/acme/app/issues/99", Title: "updatedunique import title",
	}
	if err := s.RecordImportLinks("alice", []ImportLink{updated}); err != nil {
		t.Fatalf("RecordImportLinks update: %v", err)
	}
	if err := s.db.QueryRow(`SELECT imported_at FROM import_links WHERE scope = ? AND external_key = ?`, "alice", first.ExternalKey).Scan(&refreshedAt); err != nil {
		t.Fatalf("read refreshed imported_at: %v", err)
	}
	if refreshedAt == importedAt {
		t.Fatalf("upsert retained imported_at %q", importedAt)
	}
	got, err := s.ImportedAs("alice", []string{first.ExternalKey, second.ExternalKey, "missing"})
	if err != nil {
		t.Fatalf("ImportedAs: %v", err)
	}
	want := map[string]ImportLink{first.ExternalKey: updated, second.ExternalKey: second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImportedAs = %+v, want %+v", got, want)
	}
	hits, err := s.SearchSimilar("alice", "updatedunique", "", 3)
	if err != nil {
		t.Fatalf("SearchSimilar import: %v", err)
	}
	if len(hits) != 1 || hits[0].Via != "import" || hits[0].Link != updated.Link || hits[0].Title != updated.Title {
		t.Fatalf("SearchSimilar import = %+v", hits)
	}
}

func TestImportedAsEmptyInputReturnsInitializedMap(t *testing.T) {
	got, err := newStore(t).ImportedAs("alice", nil)
	if err != nil {
		t.Fatalf("ImportedAs(nil): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ImportedAs(nil) = %#v, want initialized empty map", got)
	}
}

func TestRecordImportLinksRejectsMalformedBatchWithoutPartialWrites(t *testing.T) {
	tests := []ImportLink{
		{ExternalKey: strings.Repeat("k", 2049), URL: "https://example.test", Title: "title"},
		{ExternalKey: "key", URL: strings.Repeat("u", 2049), Title: "title"},
		{ExternalKey: "key", URL: "https://example.test", Title: strings.Repeat("t", 501)},
		{ExternalKey: "key\nbreak", URL: "https://example.test", Title: "title"},
		{ExternalKey: "key", URL: "https://example.test\rbreak", Title: "title"},
		{ExternalKey: "key", URL: "https://example.test", Title: "title\nbreak"},
	}
	for i, bad := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			s := newStore(t)
			valid := ImportLink{Source: "gitlab.com", Kind: "gitlab", ExternalKey: "valid", Link: "link::gitlab#1", URL: "https://example.test/1", Title: "valid"}
			if err := s.RecordImportLinks("alice", []ImportLink{valid, bad}); err == nil {
				t.Fatal("RecordImportLinks accepted malformed input")
			}
			got, err := s.ImportedAs("alice", []string{"valid", bad.ExternalKey})
			if err != nil {
				t.Fatalf("ImportedAs after rejection: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("rejected batch wrote rows: %+v", got)
			}
		})
	}
}
