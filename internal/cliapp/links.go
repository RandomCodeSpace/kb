package cliapp

import (
	"errors"
	"fmt"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// cmdLink records a blocks edge: kb link <a> blocks <b> ("a blocks b") or
// kb link <a> blocked-by <b> ("b blocks a"). Local-only, like comments.
func (a *app) cmdLink(args []string) int {
	fs, data := a.newFlagSet("link")
	jsonF := fs.Bool("json", false, "print both linked tasks as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 3 {
		return a.usageErr(errors.New("link needs <id> blocks|blocked-by <id> arguments"))
	}
	blockerRef, blockedRef := pos[0], pos[2]
	switch pos[1] {
	case "blocks":
	case "blocked-by":
		blockerRef, blockedRef = pos[2], pos[0]
	default:
		return a.usageErr(fmt.Errorf("unknown relation %q (want blocks or blocked-by)", pos[1]))
	}
	return a.withLocal(*data, func(be *localBackend) error {
		blocker, blocked, err := be.link(blockerRef, blockedRef)
		if err != nil {
			return err
		}
		if *jsonF {
			return writeSingleJSON(a.stdout, []taskJSON{
				itemJSON(item{ref: blocker.ID, task: blocker}),
				itemJSON(item{ref: blocked.ID, task: blocked}),
			})
		}
		fmt.Fprintf(a.stdout, "linked: #%d blocks #%d\n", blocker.Seq, blocked.Seq)
		return nil
	})
}

// cmdUnlink removes the edge between two tasks, whichever way it points.
func (a *app) cmdUnlink(args []string) int {
	fs, data := a.newFlagSet("unlink")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 2 {
		return a.usageErr(errors.New("unlink needs exactly two <id> arguments"))
	}
	return a.withLocal(*data, func(be *localBackend) error {
		if err := be.unlink(pos[0], pos[1]); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("no link between %q and %q", pos[0], pos[1])
			}
			return friendlyIDErr(err, pos[0])
		}
		fmt.Fprintf(a.stdout, "unlinked %s and %s\n", pos[0], pos[1])
		return nil
	})
}
