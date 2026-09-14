package cliapp

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandHelpIsScopedAndDoesNotOpenStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unused")
	t.Setenv("KB_DATA", dir)
	for _, args := range [][]string{{"help", "project"}, {"help", "comment"}, {"comment", "--help"}} {
		var out, stderr bytes.Buffer
		if code := Run(args, &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "usage: kb") {
			t.Fatalf("%v: code=%d out=%q stderr=%q", args, code, out.String(), stderr.String())
		}
	}
	for name, help := range commandHelp {
		t.Run(name, func(t *testing.T) {
			var previous string
			for _, args := range [][]string{
				append([]string{"help"}, strings.Fields(name)...),
				append(strings.Fields(name), "--help"),
			} {
				var out, stderr bytes.Buffer
				if code := Run(args, &out, &stderr); code != 0 || stderr.Len() != 0 {
					t.Fatalf("%v: code=%d out=%q stderr=%q", args, code, out.String(), stderr.String())
				}
				for _, want := range []string{"usage: kb " + name, help.args, help.example, "-data", "-json", "Exit codes:"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("%v missing %q: %s", args, want, out.String())
					}
				}
				if strings.Contains(out.String(), "commands:") {
					t.Errorf("%v prints unrelated command reference", args)
				}
				if previous != "" && previous != out.String() {
					t.Errorf("help aliases differ for %s", name)
				}
				previous = out.String()
			}
		})
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("help opened storage: %v", err)
	}
}

func TestHelpRejectsArgumentsThatCouldMutate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unused")
	t.Setenv("KB_DATA", dir)
	for _, args := range [][]string{
		{"help", "unknown"}, {"help", "project", "create", "oops"},
		{"help", "add", "oops", "-p", "web"}, {"help", "rm", "1", "--yes"},
	} {
		var out, stderr bytes.Buffer
		if code := Run(args, &out, &stderr); code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "unknown help topic") {
			t.Errorf("%v: code=%d out=%q stderr=%q", args, code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("help opened storage: %v", err)
	}
}
