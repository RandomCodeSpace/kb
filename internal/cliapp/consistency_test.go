package cliapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestTaskJSONIncludesZeroFields(t *testing.T) {
	var out bytes.Buffer
	if err := writeJSONItem(&out, item{}); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"id": `""`, "seq": "0", "emoji": `""`, "title": `""`, "desc": `""`,
		"status": `""`, "blocked": "false", "prio": "0", "due": `""`, "effort": `""`,
		"tags": "[]", "checks": "[]", "position": "0", "createdAt": `""`, "movedAt": `""`, "updatedAt": `""`,
	} {
		if string(fields[key]) != want {
			t.Errorf("%s = %s, want %s", key, fields[key], want)
		}
	}
}

func TestRestoreRequiresCancelledWithoutMutation(t *testing.T) {
	for _, status := range []board.Status{board.StatusTodo, board.StatusDoing, board.StatusDone} {
		t.Run(string(status), func(t *testing.T) {
			dir := localEnv(t)
			if _, stderr, code := runCmd(t, "add", "Keep state", "--status", string(status), "--data", dir); code != 0 {
				t.Fatal(stderr)
			}
			before := readLocalSnapshot(t, dir)
			out, stderr, code := runCmd(t, "restore", "1", "--json", "--data", dir)
			if code != 4 || out != "" || !strings.Contains(stderr, "restore requires a cancelled task") {
				t.Fatalf("code=%d out=%q stderr=%q", code, out, stderr)
			}
			if after := readLocalSnapshot(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatalf("restore changed task: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestLinkErrorsIdentifyMissingOperand(t *testing.T) {
	dir := localEnv(t)
	for _, title := range []string{"first", "second"} {
		if _, stderr, code := runCmd(t, "add", title, "--data", dir); code != 0 {
			t.Fatal(stderr)
		}
	}
	for _, args := range [][]string{
		{"link", "9", "blocks", "1"}, {"link", "1", "blocks", "9"},
		{"link", "1", "blocked-by", "9"}, {"unlink", "9", "1"}, {"unlink", "1", "9"},
	} {
		out, stderr, code := runCmd(t, append(args, "--data", dir)...)
		if code != 3 || out != "" || stderr != "kb: no task matches id \"9\"\n" {
			t.Errorf("%v: code=%d out=%q stderr=%q", args, code, out, stderr)
		}
	}
	if _, stderr, code := runCmd(t, "unlink", "1", "2", "--data", dir); code != 3 || stderr != "kb: no link between \"1\" and \"2\"\n" {
		t.Fatalf("missing edge: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "link", "1", "blocks", "1", "--data", dir); code != 4 || !strings.Contains(stderr, "cannot block itself") {
		t.Fatalf("self edge: code=%d stderr=%q", code, stderr)
	}
}

func TestCommentAuthorFlagDoesNotChangeBoardNamespace(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir); code != 0 {
		t.Fatal(stderr)
	}
	for _, author := range []string{"Alice Example", "default"} {
		args := []string{"comment", "add", "1", "A note", "--json", "--data", dir}
		if author != "default" {
			args = append(args, "--author", author)
		}
		out, stderr, code := runCmd(t, args...)
		if code != 0 {
			t.Fatal(stderr)
		}
		var c commentJSON
		if err := json.Unmarshal([]byte(out), &c); err != nil {
			t.Fatal(err)
		}
		if c.Author != author || c.TaskSeq != 1 {
			t.Fatalf("comment=%+v, want author=%q task=1", c, author)
		}
	}
}

func TestFailureClassesKeepOperationalErrorsDistinct(t *testing.T) {
	var out bytes.Buffer
	a := app{stderr: &out}
	if code := a.fail(fmt.Errorf("disk unavailable")); code != 1 {
		t.Fatalf("runtime code=%d", code)
	}
	if code := a.usageErr(fmt.Errorf("invalid flag")); code != 2 {
		t.Fatalf("usage code=%d", code)
	}
}

func TestViewJSONIncludesEmptyEnrichment(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Plain task", "--data", dir); code != 0 {
		t.Fatal(stderr)
	}
	out, stderr, code := runCmd(t, "view", "1", "--json", "--data", dir)
	if code != 0 {
		t.Fatal(stderr)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"checks", "blocks", "blockedBy", "comments"} {
		if string(fields[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, fields[key])
		}
	}
}
