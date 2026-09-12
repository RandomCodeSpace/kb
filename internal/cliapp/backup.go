package cliapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func (a *app) cmdBackup(args []string) int {
	fs, data := a.newFlagSet("backup")
	asJSON := fs.Bool("json", false, "print the backup destination as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("backup needs exactly one new destination directory"))
	}
	source, err := resolveDataDir(*data)
	if err != nil {
		return a.fail(err)
	}
	destination, err := filepath.Abs(pos[0])
	if err != nil {
		return a.fail(err)
	}
	if err := store.BackupDirectory(source, destination); err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, store.ErrBackupBusy) {
			err = describedError{errRefused, err.Error()}
		}
		return a.fail(err)
	}
	result := struct {
		Path                   string `json:"path"`
		RequiresExternalSecret bool   `json:"requiresExternalSecret"`
	}{Path: destination, RequiresExternalSecret: os.Getenv("KB_SECRET") != ""}
	if *asJSON {
		if err := writeSingleJSON(a.stdout, result); err != nil {
			return a.fail(err)
		}
		return 0
	}
	fmt.Fprintf(a.stdout, "backup created at %s\n", destination)
	if result.RequiresExternalSecret {
		fmt.Fprintln(a.stdout, "keep the original KB_SECRET separately; it is not saved in this backup")
	}
	return 0
}
