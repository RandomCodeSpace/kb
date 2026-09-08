package cliapp

// SetActiveProject stores name as the project local surfaces default to when
// no flag or KB_PROJECT names one. It is the write half of ActiveProject and
// writes the same state.json `kb project use` writes, atomically, so a switch
// made from another surface (the web UI) and `kb project current` cannot
// disagree.
//
// It is exported for the same reason ActiveProject is: more than one surface
// resolves the active project, and a second implementation of the state file
// is a second chance to get its shape wrong.
func SetActiveProject(dataDir, name string) error {
	name, err := ValidateProjectName(name)
	if err != nil {
		return err
	}
	dir, err := resolveDataDir(dataDir)
	if err != nil {
		return err
	}
	state, err := loadCLIState(dir)
	if err != nil {
		return err
	}
	state.ActiveProject = name
	return saveCLIState(dir, state)
}
