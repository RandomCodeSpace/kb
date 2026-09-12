package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
)

type releasedFixture struct {
	Version        string                `json:"version"`
	DatabaseSHA256 string                `json:"database_sha256"`
	SchemaVersion  int                   `json:"schema_version"`
	RowCounts      map[string]int        `json:"row_counts"`
	Tasks          []releasedFixtureTask `json:"tasks"`
}

type releasedFixtureTask struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Desc      string        `json:"desc"`
	Status    board.Status  `json:"status"`
	Prio      int           `json:"prio"`
	Due       string        `json:"due"`
	Effort    string        `json:"effort"`
	Tags      []string      `json:"tags"`
	Checks    []board.Check `json:"checks"`
	CreatedAt time.Time     `json:"created_at"`
	MovedAt   time.Time     `json:"moved_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Seq       int           `json:"seq"`
}

func TestReleasedDatabaseFixturesMigrate(t *testing.T) {
	root := filepath.Join("testdata", "migrations")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		found[entry.Name()] = true
		t.Run(entry.Name(), func(t *testing.T) {
			dir := filepath.Join(root, entry.Name())
			manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var fixture releasedFixture
			if err := json.Unmarshal(manifestBytes, &fixture); err != nil {
				t.Fatal(err)
			}
			if fixture.Version != entry.Name() || len(fixture.Tasks) != 3 {
				t.Fatalf("unexpected fixture identity/count: %q/%d", fixture.Version, len(fixture.Tasks))
			}
			source := filepath.Join(dir, "kb.db")
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != fixture.DatabaseSHA256 {
				t.Fatalf("fixture checksum = %s, want %s", got, fixture.DatabaseSHA256)
			}
			t.Cleanup(func() {
				after, err := os.ReadFile(source)
				if err != nil || !reflect.DeepEqual(after, data) {
					t.Errorf("historical fixture changed: %v", err)
				}
			})
			path := filepath.Join(t.TempDir(), "kb.db")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			historical, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			var historicalSchema int
			err = historical.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&historicalSchema)
			if closeErr := historical.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil || historicalSchema != fixture.SchemaVersion {
				t.Fatalf("historical schema = %d, want %d: %v", historicalSchema, fixture.SchemaVersion, err)
			}
			var first []board.Task
			for attempt := 0; attempt < 2; attempt++ {
				st, err := Open(path, []byte("synthetic-migration-fixture-secret-only"))
				if err != nil {
					t.Fatalf("open %s attempt %d: %v", fixture.Version, attempt+1, err)
				}
				t.Cleanup(func() { _ = st.Close() })
				tasks := assertReleasedFixture(t, st, fixture)
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				if attempt == 0 {
					first = tasks
				} else if !reflect.DeepEqual(tasks, first) {
					t.Fatal("opening the migrated fixture again changed its tasks")
				}
			}
		})
	}
	for _, version := range []string{"v0.2.0", "v1.10.0"} {
		if !found[version] {
			t.Errorf("missing required released fixture %s", version)
		}
	}
}

func assertReleasedFixture(t *testing.T, st *Store, fixture releasedFixture) []board.Task {
	t.Helper()
	var schema string
	if err := st.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if schema != strconv.Itoa(len(migrations)) {
		t.Errorf("schema = %s, want %d", schema, len(migrations))
	}
	for table, want := range fixture.RowCounts {
		var got int
		// Table names come from the checked-in manifest, quoted as identifiers.
		query := fmt.Sprintf("SELECT count(*) FROM %q", table)
		if err := st.db.QueryRow(query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
	tasks, err := st.ListTasks("default", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != len(fixture.Tasks) {
		t.Fatalf("tasks = %d, want %d", len(tasks), len(fixture.Tasks))
	}
	for _, want := range fixture.Tasks {
		got, err := st.Task("default", want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want.Prio < 1 || want.Prio > 3 {
			want.Prio = board.PrioLow
		}
		if want.UpdatedAt.IsZero() {
			want.UpdatedAt = want.MovedAt
		}
		if want.Seq == 0 {
			if got.Seq < 1 {
				t.Errorf("task %s has no migrated sequence", want.ID)
			}
			want.Seq = got.Seq
		}
		actual := releasedFixtureTask{
			ID: got.ID, Title: got.Title, Desc: got.Desc, Status: got.Status, Prio: got.Prio,
			Due: got.Due, Effort: got.Effort, Tags: got.Tags, Checks: got.Checks,
			CreatedAt: got.CreatedAt, MovedAt: got.MovedAt, UpdatedAt: got.UpdatedAt, Seq: got.Seq,
		}
		if !reflect.DeepEqual(actual, want) {
			t.Errorf("task %s after migration:\n got %#v\nwant %#v", want.ID, actual, want)
		}
	}
	return tasks
}
