package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
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
