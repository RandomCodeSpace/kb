package cliapp

import (
	"strings"
	"testing"
)

func TestStableIDAddressing(t *testing.T) {
	dir := localEnv(t)
	for _, title := range []string{"Alpha", "Beta"} {
		if _, errS, code := runCmd(t, "add", title, "--data", dir); code != 0 {
			t.Fatalf("add %q failed: %s", title, errS)
		}
	}

	tasks := listJSON(t, "--data", dir)
	if len(tasks) != 2 || tasks[0].Seq != 1 || tasks[1].Seq != 2 {
		t.Fatalf("seq in --json = %+v, want 1 and 2", tasks)
	}

	// Both the bare number and the #-form address the same task.
	out, errS, code := runCmd(t, "update", "#2", "--title", "Beta v2", "--data", dir)
	if code != 0 || out != "updated #2 Beta v2\n" {
		t.Fatalf("update #2: code=%d out=%q stderr=%q", code, out, errS)
	}
	out, _, code = runCmd(t, "cancel", "2", "--data", dir)
	if code != 0 || out != "moved #2 -> cancelled\n" {
		t.Fatalf("cancel 2: code=%d out=%q", code, out)
	}

	// A number that never existed is not found, not treated as a UUID prefix.
	_, errS, code = runCmd(t, "done", "9", "--data", dir)
	if code != 1 || !strings.Contains(errS, "no task matches") {
		t.Fatalf("done 9: code=%d stderr=%q", code, errS)
	}
}
