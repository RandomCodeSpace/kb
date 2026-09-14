package cliapp

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// Flags come from each command's actual FlagSet. Only positional syntax and
// behavior that flags cannot describe live here.
var commandHelp = map[string]struct {
	args, notes, example string
}{
	"project create": {"<name>", "Save an empty project without a task. Repeating creation succeeds and leaves the project unchanged.\nNames are case-sensitive; no whitespace, leading '#', or '::'. Surrounding whitespace is trimmed.\nJSON: {\"project\":\"website\"}. Always pass -p on subsequent add commands.", "kb project create website --json"},
	"project list":   {"", "List saved projects alphabetically, including empty ones. Counts include cancelled tasks.\nJSON: an array of objects with project and tasks fields; [] when empty.", "kb project list --json"},
	"add":            {`"title" -p <name>`, "A title and project are required. --project <name> or --tag project::<name> also names the project.\nAn unknown project is created implicitly. Defaults: status todo, priority low.\nRepeat --tag and --check for multiple values. Prefix checklist text with \"x \" for a completed item.\nJSON: one task. Use its seq or id in later commands.", `kb add "Build landing page" -p website --prio high --check "Draft copy" --json`},
	"list":           {"", "Without -p, list all projects. Cancelled tasks are hidden unless --all or --status cancelled is given.\nRepeat --tag to require every supplied label. JSON: an array of tasks; [] when empty.", "kb list -p website --json"},
	"view":           {"<id>", "Show a task, its dependencies, and comments. JSON: one task plus blocks, blockedBy, and comments arrays.", "kb view 12 --json"},
	"update":         {"<id> <field flags>", "At least one field flag is required. Omitted fields stay unchanged. -p moves the task to another project.\n--tag replaces ordinary labels while preserving the project; --check replaces the entire checklist.\nRepeat --check for every item to keep; prefix completed items with \"x \". --check \"\" clears it.\nJSON: the updated task.", `kb update 12 --title "Publish landing page" --prio high --json`},
	"move":           {"<id> <status>", "Status: todo, doing, done, or cancelled. Finishing with open work requires --force.\nJSON: the moved task.", "kb move 12 doing --json"},
	"done":           {"<id>", "Move a task to done. Open checklist items, blocked flags, and open blockers require --force.\nJSON: the finished task.", "kb done 12 --json"},
	"cancel":         {"<id>", "Move a task to cancelled. Undo with kb restore <id>. JSON: the cancelled task.", "kb cancel 12 --json"},
	"restore":        {"<id>", "Move a cancelled task back to todo. JSON: the restored task.", "kb restore 12 --json"},
	"rm":             {"<id> --yes", "Permanently delete a task. No undo. Use cancel for reversible removal. JSON: the deleted task.", "kb rm 12 --yes --json"},
	"backup":         {"<dir>", "Copy the data directory to a new destination. Close every running kb process first.\nJSON: an object with path and requiresExternalSecret fields.", "kb backup ./kb-backup --json"},
	"users":          {"", "List local board owners and task counts. JSON: an array of objects with user and tasks fields.", "kb users --json"},
	"link":           {"<a> blocks|blocked-by <b>", "Create a task dependency. JSON: two tasks, blocker first.", "kb link 12 blocks 13 --json"},
	"unlink":         {"<a> <b>", "Remove the dependency between two tasks. JSON: {\"removed\":true}.", "kb unlink 12 13 --json"},
	"comment add":    {`<id> "text"`, "Append a nonempty comment. JSON: one comment with its id, task, taskId, author, body, and createdAt.", `kb comment add 12 "Copy is ready" --author agent --json`},
	"comment list":   {"<id>", "List comments oldest first. JSON: an array of comments; [] when empty.", "kb comment list 12 --json"},
	"comment rm":     {"<cid> --yes", "Permanently delete a comment. Comment IDs accept c7 or 7. JSON: the deleted comment.", "kb comment rm c7 --yes --json"},
}

func (a *app) cmdHelp(args []string) int {
	name := strings.Join(args, " ")
	switch name {
	case "":
		fmt.Fprint(a.stdout, usageText)
		return 0
	case "project":
		fmt.Fprint(a.stdout, projectUsage)
		return 0
	case "comment":
		fmt.Fprintln(a.stdout, "usage: kb comment <add|list|rm> [args] [flags]\n\n  kb comment add <id> \"text\"\n  kb comment list <id>\n  kb comment rm <cid> --yes\n\nUse kb help comment <command> for flags and examples.")
		return 0
	}
	if _, ok := commandHelp[name]; !ok {
		return a.usageErr(fmt.Errorf("unknown help topic %q; use kb help for commands", name))
	}
	// Only registered topics reach Run. User-supplied flags or extra arguments
	// cannot turn a help request into a mutation.
	return Run(append(strings.Fields(name), "--help"), a.stdout, a.stderr)
}

func (a *app) writeCommandHelp(name string, fs *flag.FlagSet) {
	h := commandHelp[name]
	fmt.Fprintf(a.stdout, "usage: kb %s [flags]\n\n%s\n\nflags:\n", strings.TrimSpace(name+" "+h.args), h.notes)
	fs.SetOutput(a.stdout)
	fs.PrintDefaults()
	fs.SetOutput(io.Discard)
	fmt.Fprintf(a.stdout, "\nexample:\n  %s\n\nFlags may appear before or after arguments, after the command name.\nTask IDs accept a sequence number, quoted '#12', or a unique UUID prefix.\nJSON goes to stdout; errors go to stderr. Exit codes: 0 success/help,\n1 operational failure, 2 invalid arguments, 3 missing entity, 4 conflict/refusal.\nUse kb help for the full reference.\n", h.example)
}
