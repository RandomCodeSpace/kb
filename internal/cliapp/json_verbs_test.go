package cliapp

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeTask decodes the single-object JSON a mutation verb emits.
func decodeTask(t *testing.T, out string) jsonTask {
	t.Helper()
	var task jsonTask
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("mutation --json output not a JSON object: %v\n%s", err, out)
	}
	return task
}

func TestMutationVerbsEmitJSON(t *testing.T) {
	dir := localEnv(t)

	out, errS, code := runCmd(t, "add", "Ship it", "--tag", "release", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("add --json failed: %s", errS)
	}
	added := decodeTask(t, out)
	if added.Seq != 1 || added.Title != "Ship it" || added.Status != "todo" || added.ID == "" {
		t.Fatalf("add --json = %+v", added)
	}

	out, _, code = runCmd(t, "update", "1", "--prio", "1", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("update --json failed")
	}
	if got := decodeTask(t, out); got.Prio != 1 || got.Seq != 1 {
		t.Fatalf("update --json = %+v", got)
	}

	out, _, code = runCmd(t, "move", "1", "doing", "--json", "--data", dir)
	if code != 0 || decodeTask(t, out).Status != "doing" {
		t.Fatalf("move --json: code=%d out=%s", code, out)
	}
	out, _, code = runCmd(t, "done", "1", "--json", "--data", dir)
	if code != 0 || decodeTask(t, out).Status != "done" {
		t.Fatalf("done --json: code=%d out=%s", code, out)
	}
	out, _, code = runCmd(t, "cancel", "1", "--json", "--data", dir)
	if code != 0 || decodeTask(t, out).Status != "cancelled" {
		t.Fatalf("cancel --json: code=%d out=%s", code, out)
	}
	out, _, code = runCmd(t, "restore", "1", "--json", "--data", dir)
	if code != 0 || decodeTask(t, out).Status != "todo" {
		t.Fatalf("restore --json: code=%d out=%s", code, out)
	}

	// rm --json still demands --yes; with it, the deleted task comes back.
	_, errS, code = runCmd(t, "rm", "1", "--json", "--data", dir)
	if code != 1 || !strings.Contains(errS, "--yes") {
		t.Fatalf("rm --json without --yes: code=%d stderr=%q", code, errS)
	}
	out, _, code = runCmd(t, "rm", "1", "--yes", "--json", "--data", dir)
	if code != 0 || decodeTask(t, out).Title != "Ship it" {
		t.Fatalf("rm --yes --json: code=%d out=%s", code, out)
	}
}
