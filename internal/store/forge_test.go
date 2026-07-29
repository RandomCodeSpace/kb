package store

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const forgeValidationPAT = "glpat-validation-secret-do-not-leak"

func mustSetForge(t *testing.T, s *Store, scope, name, kind string, baseURL, pat *string) bool {
	t.Helper()
	cleared, err := s.SetForgeSource(scope, name, kind, baseURL, pat)
	if err != nil {
		t.Fatalf("SetForgeSource(%q, %q) failed", scope, name)
	}
	return cleared
}

func mustForgeSources(t *testing.T, s *Store, scope string) []ForgeSource {
	t.Helper()
	sources, err := s.ForgeSources(scope)
	if err != nil {
		t.Fatalf("ForgeSources(%q): %v", scope, err)
	}
	return sources
}

func mustForgePAT(t *testing.T, s *Store, scope, name string) (string, string, string) {
	t.Helper()
	kind, baseURL, pat, err := s.ForgePAT(scope, name)
	if err != nil {
		t.Fatalf("ForgePAT(%q, %q) failed", scope, name)
	}
	return kind, baseURL, pat
}

func requireForgePATError(t *testing.T, s *Store, scope, name string, forbidden ...string) {
	t.Helper()
	kind, baseURL, pat, err := s.ForgePAT(scope, name)
	if err == nil {
		t.Fatalf("ForgePAT(%q, %q) succeeded, want error", scope, name)
	}
	if kind != "" || baseURL != "" || pat != "" {
		t.Fatal("ForgePAT error returned non-zero values")
	}
	for _, value := range forbidden {
		if strings.Contains(err.Error(), value) {
			t.Fatal("ForgePAT error exposed secret material")
		}
	}
}

// Forge source CRUD keeps canonical identity, deterministic ordering, and the original creation time.
func TestForgeSourcesCRUDOrdersNormalizedNamesAndPreservesCreationTime(t *testing.T) {
	s := newStore(t)
	fixtures := []struct{ name, kind, base string }{
		{"Zulu", "github", "https://zulu.example/api"},
		{"Alpha.Main_1-Prod", "gitlab", "gitlab.example/api"},
		{"middle", "github", "http://middle.example/api"},
	}
	for _, fixture := range fixtures {
		if mustSetForge(t, s, "alice", fixture.name, fixture.kind, sptr(fixture.base), nil) {
			t.Fatalf("creating %q reported a cleared token", fixture.name)
		}
	}
	sources := mustForgeSources(t, s, "alice")
	wantNames := []string{"alpha.main_1-prod", "middle", "zulu"}
	if len(sources) != len(wantNames) {
		t.Fatalf("ForgeSources length = %d, want %d", len(sources), len(wantNames))
	}
	for i, want := range wantNames {
		if sources[i].Name != want || sources[i].CreatedAt.IsZero() {
			t.Fatalf("source[%d] identity or creation time is wrong", i)
		}
	}
	if sources[0].BaseURL != "https://gitlab.example/api" || sources[1].BaseURL != "http://middle.example/api" {
		t.Fatalf("normalized base URLs = %q, %q", sources[0].BaseURL, sources[1].BaseURL)
	}
	created := sources[0].CreatedAt
	if mustSetForge(t, s, "alice", "ALPHA.MAIN_1-PROD", "gitlab", sptr("gitlab.example/v2"), nil) {
		t.Fatal("tokenless same-origin update reported a cleared token")
	}
	sources = mustForgeSources(t, s, "alice")
	if len(sources) != 3 || sources[0].BaseURL != "https://gitlab.example/v2" || !sources[0].CreatedAt.Equal(created) {
		t.Fatal("updated source did not preserve its canonical row and CreatedAt")
	}
	if err := s.DeleteForgeSource("alice", "middle"); err != nil {
		t.Fatalf("DeleteForgeSource: %v", err)
	}
	sources = mustForgeSources(t, s, "alice")
	if len(sources) != 2 || sources[0].Name != "alpha.main_1-prod" || sources[1].Name != "zulu" {
		t.Fatal("delete did not leave the expected ordered sources")
	}
}

// A listed forge source exposes token presence while the raw database stores only sealed bytes.
func TestForgePATIsSealedAndNeverListed(t *testing.T) {
	s := newStore(t)
	const pat = "glpat-sealed-secret-do-not-leak-123456"
	mustSetForge(t, s, "alice", "primary", "gitlab", sptr("https://gitlab.example/api"), sptr(pat))
	var enc []byte
	if err := s.db.QueryRow(`SELECT pat_enc FROM forge_sources WHERE scope = ? AND name = ?`, "alice", "primary").Scan(&enc); err != nil {
		t.Fatalf("read raw pat_enc: %v", err)
	}
	if len(enc) == 0 || bytes.Equal(enc, []byte(pat)) || bytes.Contains(enc, []byte(pat)) {
		t.Fatal("pat_enc is empty or contains plaintext")
	}
	kind, baseURL, gotPAT := mustForgePAT(t, s, "alice", "primary")
	if kind != "gitlab" || baseURL != "https://gitlab.example/api" || gotPAT != pat {
		t.Fatal("ForgePAT did not return the exact stored values")
	}
	sources := mustForgeSources(t, s, "alice")
	if len(sources) != 1 || !sources[0].HasToken || strings.Contains(fmt.Sprintf("%+v", sources), pat) {
		t.Fatal("listed source exposed the token or lost HasToken")
	}
}

// Token patches keep credentials deliberately and clear them before an endpoint changes origin.
func TestForgeSourcePATPatchAndOriginInvariants(t *testing.T) {
	s := newStore(t)
	const firstPAT, freshPAT = "glpat-first", "glpat-fresh"
	mustSetForge(t, s, "alice", "primary", "gitlab", sptr("https://forge.example/v1"), sptr(firstPAT))
	if mustSetForge(t, s, "alice", "primary", "gitlab", nil, nil) {
		t.Fatal("nil patch reported a cleared token")
	}
	if _, baseURL, pat := mustForgePAT(t, s, "alice", "primary"); baseURL != "https://forge.example/v1" || pat != firstPAT {
		t.Fatal("nil patch changed the base URL or PAT")
	}
	if mustSetForge(t, s, "alice", "primary", "gitlab", sptr("https://forge.example/v2"), nil) {
		t.Fatal("same-origin patch cleared the token")
	}
	if _, _, pat := mustForgePAT(t, s, "alice", "primary"); pat != firstPAT {
		t.Fatal("same-origin patch did not preserve the PAT")
	}
	if !mustSetForge(t, s, "alice", "primary", "gitlab", sptr("https://other.example/v2"), nil) {
		t.Fatal("different-origin patch did not report tokenCleared")
	}
	if _, baseURL, pat := mustForgePAT(t, s, "alice", "primary"); baseURL != "https://other.example/v2" || pat != "" {
		t.Fatal("different-origin patch retained the PAT or wrong base URL")
	}
	if mustSetForge(t, s, "alice", "primary", "gitlab", sptr("https://third.example/v1"), sptr(freshPAT)) {
		t.Fatal("different-origin patch with a fresh PAT reported tokenCleared")
	}
	if _, _, pat := mustForgePAT(t, s, "alice", "primary"); pat != freshPAT {
		t.Fatal("different-origin patch did not store the fresh PAT")
	}
	mustSetForge(t, s, "alice", "primary", "gitlab", nil, sptr(""))
	if _, _, pat := mustForgePAT(t, s, "alice", "primary"); pat != "" {
		t.Fatal("explicit empty PAT did not clear the credential")
	}
	if sources := mustForgeSources(t, s, "alice"); len(sources) != 1 || sources[0].HasToken {
		t.Fatal("listed source still reports a token after explicit clear")
	}
}

// Forge source validation rejects unusable rows atomically without echoing supplied secrets.
func TestForgeSourceValidationAndNormalization(t *testing.T) {
	s := newStore(t)
	valid64 := strings.Repeat("A", 64)
	mustSetForge(t, s, "alice", valid64, "github", sptr("github.example/api"), nil)
	if got := mustForgeSources(t, s, "alice"); len(got) != 1 || got[0].Name != strings.Repeat("a", 64) {
		t.Fatal("64-character name was not accepted and normalized")
	}

	const userinfoSecret = "url-userinfo-secret-do-not-leak"
	tests := []struct {
		name, source, kind, base, forbidden string
		nilBase                             bool
	}{
		{"kind", "prod", "gitea", "https://forge.example", "", false},
		{"kind case", "prod", "GitLab", "https://forge.example", "", false},
		{"empty name", "", "gitlab", "https://forge.example", "", false},
		{"slash name", "bad/name", "gitlab", "https://forge.example", "", false},
		{"space name", "bad name", "gitlab", "https://forge.example", "", false},
		{"unicode name", "förge", "gitlab", "https://forge.example", "", false},
		{"long name", strings.Repeat("a", 65), "gitlab", "https://forge.example", "", false},
		{"nil base", "prod", "gitlab", "", "", true},
		{"empty base", "prod", "gitlab", "", "", false},
		{"missing host", "prod", "gitlab", "https:///api", "", false},
		{"non-http base", "prod", "gitlab", "ftp://forge.example", "", false},
		{"userinfo", "prod", "gitlab", "https://user:" + userinfoSecret + "@forge.example", userinfoSecret, false},
		{"query", "prod", "gitlab", "https://forge.example?x=y", "", false},
		{"fragment", "prod", "gitlab", "https://forge.example#fragment", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore(t)
			var baseURL *string
			if !tt.nilBase {
				baseURL = sptr(tt.base)
			}
			if _, err := store.SetForgeSource("alice", tt.source, tt.kind, baseURL, sptr(forgeValidationPAT)); err == nil {
				t.Fatal("SetForgeSource accepted invalid input")
			} else if strings.Contains(err.Error(), forgeValidationPAT) || tt.forbidden != "" && strings.Contains(err.Error(), tt.forbidden) {
				t.Fatal("validation error exposed secret material")
			}
			var count int
			if err := store.db.QueryRow(`SELECT count(*) FROM forge_sources`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid input left %d rows, err=%v", count, err)
			}
		})
	}
}

// Every forge operation preserves scope boundaries, including deletion and missing-source errors.
func TestForgeSourcesAreScopeIsolatedAndDeleteIsScoped(t *testing.T) {
	s := newStore(t)
	const alicePAT, bobPAT = "glpat-alice-only", "ghp-bob-only"
	mustSetForge(t, s, "alice", "shared", "gitlab", sptr("https://alice.example/api"), sptr(alicePAT))
	mustSetForge(t, s, "bob", "shared", "github", sptr("https://bob.example/api"), sptr(bobPAT))
	mustSetForge(t, s, "bob", "bob-only", "github", sptr("https://bob-only.example/api"), sptr(bobPAT))
	if got := mustForgeSources(t, s, "alice"); len(got) != 1 || got[0].BaseURL != "https://alice.example/api" {
		t.Fatal("alice list contains the wrong scoped sources")
	}
	if _, _, pat := mustForgePAT(t, s, "alice", "shared"); pat != alicePAT {
		t.Fatal("alice PAT round-trip failed")
	}
	if _, _, pat := mustForgePAT(t, s, "bob", "shared"); pat != bobPAT {
		t.Fatal("bob PAT round-trip failed")
	}
	requireForgePATError(t, s, "alice", "bob-only", bobPAT, "https://bob-only.example/api")
	if err := s.DeleteForgeSource("alice", "shared"); err != nil {
		t.Fatalf("DeleteForgeSource: %v", err)
	}
	requireForgePATError(t, s, "alice", "shared", alicePAT)
	if got := mustForgeSources(t, s, "alice"); len(got) != 0 {
		t.Fatal("alice source remained after scoped delete")
	}
	if _, _, pat := mustForgePAT(t, s, "bob", "shared"); pat != bobPAT {
		t.Fatal("scoped delete changed bob PAT")
	}
	var bobCount int
	if err := s.db.QueryRow(`SELECT count(*) FROM forge_sources WHERE scope = ? AND name = ?`, "bob", "shared").Scan(&bobCount); err != nil || bobCount != 1 {
		t.Fatalf("bob shared row count = %d, err=%v", bobCount, err)
	}
}

// Corrupt ciphertext fails closed without copying encrypted bytes into the returned error.
func TestForgePATCorruptCiphertextDoesNotLeak(t *testing.T) {
	s := newStore(t)
	const pat, corrupt = "glpat-original-secret", "ciphertext-leak-sentinel"
	mustSetForge(t, s, "alice", "primary", "gitlab", sptr("https://forge.example/api"), sptr(pat))
	if _, err := s.db.Exec(`UPDATE forge_sources SET pat_enc = ? WHERE scope = ? AND name = ?`, []byte(corrupt), "alice", "primary"); err != nil {
		t.Fatalf("corrupt pat_enc: %v", err)
	}
	if got := mustForgeSources(t, s, "alice"); len(got) != 1 || !got[0].HasToken {
		t.Fatal("listed corrupt source lost its HasToken marker")
	}
	requireForgePATError(t, s, "alice", "primary", pat, corrupt)
}
