package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type webLowerOracleFixture struct {
	NodeVersion        string `json:"node_version"`
	UnicodeVersion     string `json:"unicode_version"`
	BaseUnicodeVersion string `json:"base_unicode_version"`
	ScalarMapping      struct {
		Pairs  [][2]string `json:"pairs"`
		Ranges []struct {
			First string `json:"first"`
			Last  string `json:"last"`
			Delta int32  `json:"delta"`
		} `json:"ranges"`
	} `json:"scalar_mapping"`
	Cased         webLowerPropertyFixture `json:"cased"`
	CaseIgnorable webLowerPropertyFixture `json:"case_ignorable"`
}

type webLowerPropertyFixture struct {
	Add    [][2]string `json:"add"`
	Remove [][2]string `json:"remove"`
}

func TestWebLowerMatchesFrozenNode24Unicode17Oracle(t *testing.T) {
	fixture := loadWebLowerOracleFixture(t)
	if fixture.NodeVersion != "v24.15.0" || fixture.UnicodeVersion != "17.0" {
		t.Fatalf("unexpected frozen oracle: Node %s Unicode %s", fixture.NodeVersion, fixture.UnicodeVersion)
	}
	if cases.UnicodeVersion != fixture.BaseUnicodeVersion {
		t.Fatalf("cases Unicode tables drifted: cases=%s fixture=%s", cases.UnicodeVersion, fixture.BaseUnicodeVersion)
	}
	if unicode.Version != fixture.BaseUnicodeVersion {
		t.Fatalf("stdlib Unicode tables drifted: unicode=%s fixture=%s", unicode.Version, fixture.BaseUnicodeVersion)
	}

	wantMapping := fixtureScalarMappings(t, fixture)
	baseLower := cases.Lower(language.Und, cases.HandleFinalSigma(false))
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if 0xd800 <= r && r <= 0xdfff {
			continue
		}
		want := baseLower.String(string(r))
		if mapped, ok := wantMapping[r]; ok {
			want = string(mapped)
		}
		if got := webLower(string(r)); got != want {
			t.Fatalf("webLower(%U) = %U, frozen Node oracle wants %U", r, []rune(got), []rune(want))
		}
	}
	if len(wantMapping) != 55 {
		t.Fatalf("frozen scalar delta count = %d, want 55", len(wantMapping))
	}

	assertWebPropertyFixture(t, fixture.Cased, 108, webUnicode17Cased, baseUnicode15Cased)
	assertWebPropertyFixture(t, fixture.CaseIgnorable, 89, webUnicode17CaseIgnorable, baseUnicode15CaseIgnorable)
}

func TestWebLowerFrozenContextAndExpansionVectors(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{"expanding lowercase", "İ", "i\u0307"},
		{"Unicode 17 mapping", "\ua7dc", "\u019b"},
		{"new cased before sigma", "\ua7dcΣ", "\u019bς"},
		{"new cased after sigma", "AΣ\ua7dc", "aσ\u019b"},
		{"removed cased before sigma", "\u0295Σ", "\u0295σ"},
		{"new ignorable before sigma", "A\u0897Σ", "a\u0897ς"},
		{"removed ignorable after sigma", "AΣ\U0001171eA", "aς\U0001171ea"},
		{"cased ignorable is ignored", "\ua7f1Σ", "\ua7f1σ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := webLower(test.input); got != test.want {
				t.Fatalf("webLower(%q) = %q, frozen Node oracle wants %q", test.input, got, test.want)
			}
		})
	}
}

func TestWebLowerPreservesInvalidUTF8AsContextBoundary(t *testing.T) {
	for _, test := range []struct {
		name  string
		input []byte
		want  []byte
	}{
		{
			"invalid byte breaks preceding context",
			[]byte{'A', 0xff, 0xce, 0xa3},
			[]byte{'a', 0xff, 0xcf, 0x83},
		},
		{
			"invalid byte breaks following context",
			[]byte{'A', 0xce, 0xa3, 0xff, 'A'},
			[]byte{'a', 0xcf, 0x82, 0xff, 'a'},
		},
		{
			"invalid sequences remain byte exact",
			[]byte{0xc0, 0x80, 'I', 0xf5, 0x80, 0x80, 0x80},
			[]byte{0xc0, 0x80, 'i', 0xf5, 0x80, 0x80, 0x80},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := []byte(webLower(string(test.input))); string(got) != string(test.want) {
				t.Fatalf("webLower bytes = % x, want % x", got, test.want)
			}
		})
	}
}

func loadWebLowerOracleFixture(t *testing.T) webLowerOracleFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "web_lower_unicode17.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture webLowerOracleFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func fixtureScalarMappings(t *testing.T, fixture webLowerOracleFixture) map[rune]rune {
	t.Helper()
	mappings := make(map[rune]rune)
	for _, pair := range fixture.ScalarMapping.Pairs {
		mappings[fixtureRune(t, pair[0])] = fixtureRune(t, pair[1])
	}
	for _, value := range fixture.ScalarMapping.Ranges {
		first, last := fixtureRune(t, value.First), fixtureRune(t, value.Last)
		for r := first; r <= last; r++ {
			mappings[r] = r + rune(value.Delta)
		}
	}
	return mappings
}

func assertWebPropertyFixture(
	t *testing.T,
	fixture webLowerPropertyFixture,
	wantDeltaCount int,
	gotProperty func(rune) bool,
	baseProperty func(rune) bool,
) {
	t.Helper()
	add := fixtureRanges(t, fixture.Add)
	remove := fixtureRanges(t, fixture.Remove)
	deltaCount := 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		want := baseProperty(r)
		if webInRuneRanges(r, remove) {
			want = false
		} else if webInRuneRanges(r, add) {
			want = true
		}
		if want != baseProperty(r) {
			deltaCount++
		}
		if got := gotProperty(r); got != want {
			t.Fatalf("property(%U) = %t, frozen Node oracle wants %t", r, got, want)
		}
	}
	if deltaCount != wantDeltaCount {
		t.Fatalf("property delta count = %d, want %d", deltaCount, wantDeltaCount)
	}
}

func fixtureRanges(t *testing.T, values [][2]string) []webRuneRange {
	t.Helper()
	ranges := make([]webRuneRange, len(values))
	for i, value := range values {
		ranges[i] = webRuneRange{first: fixtureRune(t, value[0]), last: fixtureRune(t, value[1])}
	}
	return ranges
}

func fixtureRune(t *testing.T, value string) rune {
	t.Helper()
	parsed, err := strconv.ParseInt(value, 16, 32)
	if err != nil || parsed < 0 || parsed > unicode.MaxRune {
		t.Fatalf("invalid fixture rune %q: %v", value, err)
	}
	return rune(parsed)
}

func baseUnicode15Cased(r rune) bool {
	return unicode.IsLower(r) || unicode.Is(unicode.Other_Lowercase, r) ||
		unicode.IsUpper(r) || unicode.Is(unicode.Other_Uppercase, r) || unicode.IsTitle(r)
}

func baseUnicode15CaseIgnorable(r rune) bool {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	switch r {
	case 0x0027, 0x002e, 0x003a, 0x00b7, 0x0387, 0x055f, 0x05f4,
		0x2018, 0x2019, 0x2024, 0x2027, 0xfe13, 0xfe52, 0xfe55,
		0xff07, 0xff0e, 0xff1a:
		return true
	default:
		return false
	}
}
