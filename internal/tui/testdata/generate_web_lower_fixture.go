//go:build ignore

// Command generate_web_lower_fixture freezes Node's Unicode lowercase oracle.
//
// From the repository root:
//
//	go run ./internal/tui/testdata/generate_web_lower_fixture.go
//	go run ./internal/tui/testdata/generate_web_lower_fixture.go -write
//
// Set NODE_BINARY to another absolute executable path when Node is not at
// /usr/bin/node. Relative names are rejected; PATH is never searched.
//
// The default mode byte-compares generated output with the checked-in fixture.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const (
	minimumNodeVersion = "24.15.0"
	oracleNodeVersion  = "v24.15.0"
	oracleUnicode      = "17.0"
	baseUnicode        = "15.0.0"
	defaultFixturePath = "internal/tui/testdata/web_lower_unicode17.json"
	defaultNodeBinary  = "/usr/bin/node"
)

const nodeOracle = `
const [major, minor] = process.versions.node.split(".").map(Number);
if (major < 24 || (major === 24 && minor < 15)) {
  throw new Error("Node >= 24.15.0 is required; found " + process.versions.node);
}
if (process.versions.unicode !== "17.0") {
  throw new Error("Unicode 17.0 is required; found " + process.versions.unicode);
}
process.stdout.write("META;" + process.versions.node + ";" + process.versions.unicode + "\n");
for (let cp = 0; cp <= 0x10ffff; cp++) {
  if (cp >= 0xd800 && cp <= 0xdfff) continue;
  const value = String.fromCodePoint(cp);
  const lower = value.toLowerCase();
  const mapping = lower === value
    ? "-"
    : [...lower].map(char => char.codePointAt(0).toString(16)).join(",");
  const cased = /\p{Cased}/u.test(value) ? "1" : "0";
  const ignorable = /\p{Case_Ignorable}/u.test(value) ? "1" : "0";
  process.stdout.write(cp.toString(16) + ";" + mapping + ";" + cased + ";" + ignorable + "\n");
}
`

type runeRange struct {
	first rune
	last  rune
}

type mapping struct {
	input  rune
	output []rune
}

type mappingRange struct {
	first rune
	last  rune
	delta rune
}

func main() {
	fixturePath := flag.String("fixture", defaultFixturePath, "fixture path relative to the repository root")
	nodeBinary := flag.String("node", configuredNodeBinary(), "absolute path to Node >= 24.15 with Unicode 17")
	write := flag.Bool("write", false, "replace the fixture instead of checking it")
	flag.Parse()

	validatedNode, err := resolveNodeBinary(*nodeBinary)
	if err != nil {
		fatalf("resolve Node binary: %v", err)
	}
	if cases.UnicodeVersion != baseUnicode || unicode.Version != baseUnicode {
		fatalf("base Unicode tables changed: cases=%s stdlib=%s, want %s", cases.UnicodeVersion, unicode.Version, baseUnicode)
	}
	generated, runtimeNode := generate(validatedNode)
	if *write {
		if err := os.WriteFile(*fixturePath, generated, 0o644); err != nil {
			fatalf("write fixture: %v", err)
		}
		fmt.Printf("wrote %s using Node %s / Unicode %s\n", *fixturePath, runtimeNode, oracleUnicode)
		return
	}
	checkedIn, err := os.ReadFile(*fixturePath)
	if err != nil {
		fatalf("read fixture: %v", err)
	}
	if !bytes.Equal(generated, checkedIn) {
		fatalf("%s differs from Node %s / Unicode %s; regenerate with -write", *fixturePath, runtimeNode, oracleUnicode)
	}
	fmt.Printf("%s matches Node %s / Unicode %s\n", *fixturePath, runtimeNode, oracleUnicode)
}

func generate(nodeBinary string) ([]byte, string) {
	// Path is validated as absolute so os/exec never performs a PATH lookup.
	command := &exec.Cmd{
		Path: nodeBinary,
		Args: []string{nodeBinary, "-e", nodeOracle},
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		fatalf("open Node stdout: %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		fatalf("start Node >= %s: %v", minimumNodeVersion, err)
	}

	baseLower := cases.Lower(language.Und, cases.HandleFinalSigma(false))
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		fatalf("read Node metadata: %v %s", scanner.Err(), strings.TrimSpace(stderr.String()))
	}
	metadata := strings.Split(scanner.Text(), ";")
	if len(metadata) != 3 || metadata[0] != "META" || metadata[2] != oracleUnicode {
		fatalf("unexpected Node metadata %q", scanner.Text())
	}
	runtimeNode := metadata[1]

	var mappings []mapping
	var casedAdd, casedRemove []rune
	var ignorableAdd, ignorableRemove []rune
	seenScalars := 0
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ";")
		if len(fields) != 4 {
			fatalf("invalid Node oracle row %q", scanner.Text())
		}
		r := parseHexRune(fields[0])
		nodeLower := parseMapping(fields[1], r)
		if got := []rune(baseLower.String(string(r))); !runesEqual(got, nodeLower) {
			mappings = append(mappings, mapping{input: r, output: nodeLower})
		}
		recordPropertyDelta(r, baseCased(r), fields[2], &casedAdd, &casedRemove)
		recordPropertyDelta(r, baseCaseIgnorable(r), fields[3], &ignorableAdd, &ignorableRemove)
		seenScalars++
	}
	if err := scanner.Err(); err != nil {
		fatalf("read Node oracle: %v", err)
	}
	if err := command.Wait(); err != nil {
		fatalf("Node oracle failed: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	const scalarCount = unicode.MaxRune + 1 - (0xdfff - 0xd800 + 1)
	if seenScalars != scalarCount {
		fatalf("Node emitted %d scalars, want %d", seenScalars, scalarCount)
	}

	pairs, mappingRanges := compactMappings(mappings)
	return renderFixture(
		pairs,
		mappingRanges,
		compactRanges(casedAdd),
		compactRanges(casedRemove),
		compactRanges(ignorableAdd),
		compactRanges(ignorableRemove),
	), runtimeNode
}

func configuredNodeBinary() string {
	if configured := os.Getenv("NODE_BINARY"); configured != "" {
		return configured
	}
	return defaultNodeBinary
}

func resolveNodeBinary(value string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("path must be absolute, got %q", value)
	}
	value = filepath.Clean(value)
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", fmt.Errorf("evaluate %q: %w", value, err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("resolved path must be absolute, got %q", resolved)
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", resolved)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%q is not executable", resolved)
	}
	return resolved, nil
}

func recordPropertyDelta(r rune, base bool, encoded string, add, remove *[]rune) {
	node := encoded == "1"
	if encoded != "0" && !node {
		fatalf("invalid property value %q for %U", encoded, r)
	}
	if base == node {
		return
	}
	if node {
		*add = append(*add, r)
	} else {
		*remove = append(*remove, r)
	}
}

func compactMappings(values []mapping) (pairs []mapping, ranges []mappingRange) {
	for i := 0; i < len(values); {
		if len(values[i].output) == 1 {
			delta := values[i].output[0] - values[i].input
			last := i
			for last+1 < len(values) && len(values[last+1].output) == 1 &&
				values[last+1].input == values[last].input+1 &&
				values[last+1].output[0]-values[last+1].input == delta {
				last++
			}
			if last > i {
				ranges = append(ranges, mappingRange{first: values[i].input, last: values[last].input, delta: delta})
				i = last + 1
				continue
			}
		}
		pairs = append(pairs, values[i])
		i++
	}
	return pairs, ranges
}

func compactRanges(values []rune) []runeRange {
	ranges := make([]runeRange, 0, len(values))
	for i := 0; i < len(values); {
		last := i
		for last+1 < len(values) && values[last+1] == values[last]+1 {
			last++
		}
		ranges = append(ranges, runeRange{first: values[i], last: values[last]})
		i = last + 1
	}
	return ranges
}

func renderFixture(
	pairs []mapping,
	mappingRanges []mappingRange,
	casedAdd, casedRemove, ignorableAdd, ignorableRemove []runeRange,
) []byte {
	var output bytes.Buffer
	output.WriteString("{\n")
	fmt.Fprintf(&output, "  \"node_version\": %q,\n", oracleNodeVersion)
	fmt.Fprintf(&output, "  \"unicode_version\": %q,\n", oracleUnicode)
	fmt.Fprintf(&output, "  \"base_unicode_version\": %q,\n", baseUnicode)
	output.WriteString("  \"source\": \"Generated by generate_web_lower_fixture.go from exhaustive JS toLowerCase, Cased, and Case_Ignorable enumeration\",\n")
	output.WriteString("  \"scalar_mapping\": {\n")
	output.WriteString("    \"pairs\": [\n")
	for i, value := range pairs {
		if len(value.output) != 1 {
			fatalf("fixture cannot compact multi-rune delta %U -> %U", value.input, value.output)
		}
		comma := ","
		if i == len(pairs)-1 {
			comma = ""
		}
		fmt.Fprintf(&output, "      [%q, %q]%s\n", formatRune(value.input), formatRune(value.output[0]), comma)
	}
	output.WriteString("    ],\n")
	output.WriteString("    \"ranges\": [\n")
	for i, value := range mappingRanges {
		comma := ","
		if i == len(mappingRanges)-1 {
			comma = ""
		}
		fmt.Fprintf(&output, "      {\"first\": %q, \"last\": %q, \"delta\": %d}%s\n",
			formatRune(value.first), formatRune(value.last), value.delta, comma)
	}
	output.WriteString("    ]\n")
	output.WriteString("  },\n")
	writeProperty := func(name string, add, remove []runeRange, final bool) {
		fmt.Fprintf(&output, "  %q: {\n", name)
		output.WriteString("    \"add\": [\n")
		writeRanges(&output, add, "      ")
		output.WriteString("    ],\n")
		output.WriteString("    \"remove\": [")
		writeInlineRanges(&output, remove)
		output.WriteString("]\n")
		if final {
			output.WriteString("  }\n")
		} else {
			output.WriteString("  },\n")
		}
	}
	writeProperty("cased", casedAdd, casedRemove, false)
	writeProperty("case_ignorable", ignorableAdd, ignorableRemove, true)
	output.WriteString("}\n")
	return output.Bytes()
}

func writeRanges(output *bytes.Buffer, values []runeRange, indent string) {
	for i, value := range values {
		comma := ","
		if i == len(values)-1 {
			comma = ""
		}
		fmt.Fprintf(output, "%s[%q, %q]%s\n", indent, formatRune(value.first), formatRune(value.last), comma)
	}
}

func writeInlineRanges(output *bytes.Buffer, values []runeRange) {
	for i, value := range values {
		if i > 0 {
			output.WriteString(", ")
		}
		fmt.Fprintf(output, "[%q, %q]", formatRune(value.first), formatRune(value.last))
	}
}

func parseMapping(encoded string, identity rune) []rune {
	if encoded == "-" {
		return []rune{identity}
	}
	parts := strings.Split(encoded, ",")
	result := make([]rune, len(parts))
	for i, value := range parts {
		result[i] = parseHexRune(value)
	}
	return result
}

func parseHexRune(value string) rune {
	parsed, err := strconv.ParseInt(value, 16, 32)
	if err != nil || parsed < 0 || parsed > unicode.MaxRune {
		fatalf("invalid scalar %q", value)
	}
	return rune(parsed)
}

func formatRune(r rune) string {
	return fmt.Sprintf("%04X", r)
}

func runesEqual(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func baseCased(r rune) bool {
	return unicode.IsLower(r) || unicode.Is(unicode.Other_Lowercase, r) ||
		unicode.IsUpper(r) || unicode.Is(unicode.Other_Uppercase, r) || unicode.IsTitle(r)
}

func baseCaseIgnorable(r rune) bool {
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

func fatalf(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "generate web lower fixture: "+format+"\n", values...)
	os.Exit(1)
}
