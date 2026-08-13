package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
)

func TestDispatchArgs(t *testing.T) {
	original := subcommands
	t.Cleanup(func() { subcommands = original })

	var received []string
	subcommands = map[string]func([]string) error{
		"alpha": func(args []string) error {
			received = append([]string(nil), args...)
			return nil
		},
		"zeta": func([]string) error { return errors.New("broken") },
	}

	for _, args := range [][]string{nil, {}, {"--port", "9000"}} {
		var stderr bytes.Buffer
		handled, code := dispatchArgs(args, &stderr)
		if handled || code != 0 || stderr.Len() != 0 {
			t.Fatalf("dispatchArgs(%v) = %v, %d, %q; want unhandled", args, handled, code, stderr.String())
		}
	}

	var stderr bytes.Buffer
	handled, code := dispatchArgs([]string{"alpha", "one", "two"}, &stderr)
	if !handled || code != 0 || !reflect.DeepEqual(received, []string{"one", "two"}) {
		t.Fatalf("known command = handled %v code %d args %v", handled, code, received)
	}

	stderr.Reset()
	handled, code = dispatchArgs([]string{"zeta"}, &stderr)
	if !handled || code != 1 || !strings.Contains(stderr.String(), "kb zeta: broken") {
		t.Fatalf("failed command = handled %v code %d stderr %q", handled, code, stderr.String())
	}

	stderr.Reset()
	handled, code = dispatchArgs([]string{"missing"}, &stderr)
	if !handled || code != 2 || !strings.Contains(stderr.String(), `unknown command "missing"`) ||
		!strings.Contains(stderr.String(), "known: alpha, zeta") {
		t.Fatalf("unknown command = handled %v code %d stderr %q", handled, code, stderr.String())
	}
}

func TestRunMCPPassesResolvedFlags(t *testing.T) {
	original := mcpRun
	t.Cleanup(func() { mcpRun = original })

	var gotData, gotUser string
	mcpRun = func(data, user string) error {
		gotData, gotUser = data, user
		return nil
	}
	wantData := filepath.Join(t.TempDir(), "boards")
	if err := runMCP([]string{"--data", wantData, "--user", "Alice.Work"}); err != nil {
		t.Fatalf("runMCP: %v", err)
	}
	if gotData != wantData || gotUser != "Alice.Work" {
		t.Fatalf("mcp args = %q, %q; want %q, Alice.Work", gotData, gotUser, wantData)
	}

	mcpRun = func(string, string) error { return errors.New("serve failed") }
	if err := runMCP([]string{"--data", wantData}); err == nil || err.Error() != "serve failed" {
		t.Fatalf("runMCP error = %v, want serve failed", err)
	}
}

func TestVersionString(t *testing.T) {
	if got := versionString(nil, false); got != "kb (unknown build)" {
		t.Fatalf("no build info = %q", got)
	}

	settings := func(pairs ...[2]string) []debug.BuildSetting {
		out := make([]debug.BuildSetting, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, debug.BuildSetting{Key: p[0], Value: p[1]})
		}
		return out
	}

	cases := []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{
			name: "go install release tag",
			info: debug.BuildInfo{Main: debug.Module{Version: "v0.8.0"}},
			want: "kb v0.8.0",
		},
		{
			name: "tag-stamped build with vcs info",
			info: debug.BuildInfo{
				Main: debug.Module{Version: "v0.8.0"},
				Settings: settings(
					[2]string{"vcs.revision", "0123456789abcdef0123"},
					[2]string{"vcs.modified", "false"},
				),
			},
			want: "kb v0.8.0 (0123456789ab)",
		},
		{
			name: "dev build from a dirty checkout",
			info: debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: settings(
					[2]string{"vcs.revision", "abc123"},
					[2]string{"vcs.modified", "true"},
				),
			},
			want: "kb devel (abc123, modified)",
		},
		{
			name: "no vcs metadata at all",
			info: debug.BuildInfo{Main: debug.Module{Version: ""}},
			want: "kb devel",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionString(&tt.info, true); got != tt.want {
				t.Fatalf("versionString = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	originalRead := readBuildInfo
	originalOut := versionOut
	t.Cleanup(func() {
		readBuildInfo = originalRead
		versionOut = originalOut
	})
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true
	}
	var out bytes.Buffer
	versionOut = &out

	if err := runVersion(nil); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	if got := out.String(); got != "kb v1.2.3\n" {
		t.Fatalf("version output = %q", got)
	}
	if err := runVersion([]string{"extra"}); err == nil {
		t.Fatal("runVersion with arguments should refuse")
	}
	if err := runVersion([]string{"--nope"}); err == nil {
		t.Fatal("runVersion with an unknown flag should refuse")
	}

	out.Reset()
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v1.2.3"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, true
	}
	if err := runVersion([]string{"--json"}); err != nil {
		t.Fatalf("runVersion --json: %v", err)
	}
	var decoded struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
		Modified bool   `json:"modified"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("version --json output not JSON: %v\n%s", err, out.String())
	}
	if decoded.Version != "v1.2.3" || decoded.Revision != "0123456789ab" || !decoded.Modified {
		t.Fatalf("version --json = %+v", decoded)
	}

	out.Reset()
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	if err := runVersion([]string{"--json"}); err != nil {
		t.Fatalf("runVersion --json unknown build: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != `{
  "version": "unknown"
}` {
		t.Fatalf("unknown-build --json = %q", got)
	}
}

func TestResolveDefaultDataDir(t *testing.T) {
	t.Setenv("KB_DATA", "/custom/data")
	if got, err := resolveDefaultDataDir(); err != nil || got != "/custom/data" {
		t.Fatalf("KB_DATA resolution = %q, %v", got, err)
	}

	t.Setenv("KB_DATA", "")
	original := userHomeDir
	t.Cleanup(func() { userHomeDir = original })
	userHomeDir = func() (string, error) { return "/home/tester", nil }
	want := filepath.Join("/home/tester", ".local", "share", "kb")
	if got, err := resolveDefaultDataDir(); err != nil || got != want {
		t.Fatalf("home resolution = %q, %v; want %q", got, err, want)
	}
	if got := defaultDataDir(); got != want {
		t.Fatalf("defaultDataDir = %q, want %q", got, want)
	}

	wantErr := errors.New("no home")
	userHomeDir = func() (string, error) { return "", wantErr }
	if got, err := resolveDefaultDataDir(); got != "" || !errors.Is(err, wantErr) {
		t.Fatalf("home error = %q, %v; want no home", got, err)
	}
}
