//go:build ignore

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveNodeBinaryPinsSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "node-first")
	second := filepath.Join(directory, "node-second")
	link := filepath.Join(directory, "node")
	for path, content := range map[string]string{first: "first", second: "second"} {
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Base(first), link); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveNodeBinary(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != first {
		t.Fatalf("resolved path = %q, want %q", resolved, first)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(second), link); err != nil {
		t.Fatal(err)
	}
	if resolved != first {
		t.Fatalf("retarget changed pinned path to %q", resolved)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" {
		t.Fatalf("pinned path reads %q after retarget, want first target", content)
	}
	retargeted, err := resolveNodeBinary(link)
	if err != nil {
		t.Fatal(err)
	}
	if retargeted != second {
		t.Fatalf("new resolution = %q, want %q", retargeted, second)
	}
}

func TestResolveNodeBinaryRejectsUnsafePaths(t *testing.T) {
	if _, err := resolveNodeBinary("node"); err == nil {
		t.Fatal("relative Node path was accepted")
	}
	notExecutable := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(notExecutable, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveNodeBinary(notExecutable); err == nil {
		t.Fatal("non-executable Node path was accepted")
	}
}
