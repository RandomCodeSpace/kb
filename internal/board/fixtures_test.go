package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The testdata/<name>.md + testdata/<name>.json pairs freeze the legacy wire
// format so parser and serializer changes remain backward compatible.
//
//	golden — a legacy three-section board (no Cancelled section, no %blocked)
//	phase3 — the phase-3 grammar: %blocked, an escaped literal "%blocked"
//	         title word, a Cancelled section, unicode titles, and every
//	         metadata-shaped word escaped as title text
var sharedFixtures = []string{"golden", "phase3"}

// normalizeProj makes a projection comparable to one decoded from JSON, where
// an empty list round-trips as an empty slice rather than nil.
func normalizeProj(p boardProj) boardProj {
	for i := range p.Tasks {
		if p.Tasks[i].Tags == nil {
			p.Tasks[i].Tags = []string{}
		}
		if p.Tasks[i].Checks == nil {
			p.Tasks[i].Checks = []Check{}
		}
	}
	if p.Tasks == nil {
		p.Tasks = []taskProj{}
	}
	return p
}

func TestSharedFixturesAreCanonical(t *testing.T) {
	for _, name := range sharedFixtures {
		raw, err := os.ReadFile(filepath.Join("testdata", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		wire := string(raw)
		if got := Serialize(Parse(wire)); got != wire {
			t.Errorf("%s.md is not canonical:\n--- got ---\n%s\n--- want ---\n%s", name, got, wire)
		}
	}
}

func TestSharedFixturesParseToProjection(t *testing.T) {
	for _, name := range sharedFixtures {
		raw, err := os.ReadFile(filepath.Join("testdata", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile(filepath.Join("testdata", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var want boardProj
		if err := json.Unmarshal(expected, &want); err != nil {
			t.Fatalf("%s.json: %v", name, err)
		}
		got := normalizeProj(projection(Parse(string(raw))))
		if !reflect.DeepEqual(got, normalizeProj(want)) {
			t.Errorf("%s.md parsed differently from %s.json:\ngot  %#v\nwant %#v", name, name, got, want)
		}
	}
}
