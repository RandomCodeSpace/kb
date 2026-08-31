package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const schemaVersion = 1

type change struct {
	Status string   `json:"status"`
	Paths  []string `json:"paths"`
}

type goImpact struct {
	Owners          []string `json:"owners"`
	DirectImporters []string `json:"direct_importers"`
	CompilePackages []string `json:"compile_packages"`
	DeletedPackages []string `json:"deleted_packages"`
	ChangedFiles    []string `json:"changed_files"`
	CompileAll      bool     `json:"compile_all"`
	RepositoryTests bool     `json:"repository_tests"`
}

type checks struct {
	FocusedQuality        bool `json:"focused_quality"`
	ContractRace          bool `json:"contract_race"`
	MigrationRecovery     bool `json:"migration_recovery"`
	TUIPerformance        bool `json:"tui_performance"`
	BinaryReleaseContract bool `json:"binary_release_contract"`
	CIContract            bool `json:"ci_contract"`
	DocsContract          bool `json:"docs_contract"`
	Sonar                 bool `json:"sonar"`
}

type manifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Base          string              `json:"base"`
	Head          string              `json:"head"`
	Changes       []change            `json:"changes"`
	Go            goImpact            `json:"go"`
	Checks        checks              `json:"checks"`
	Reasons       map[string][]string `json:"reasons"`
	Unclassified  []string            `json:"unclassified"`
}

type packageInfo struct {
	ImportPath string
	Dir        string
	Imports    []string
}

func main() {
	var base string
	var outputFormat string
	var head string
	var repo string
	flag.StringVar(&base, "base", "", "base commit")
	flag.StringVar(&head, "head", "", "head commit")
	flag.StringVar(&outputFormat, "format", "pretty", "output format: pretty, compact, github, or plan")
	flag.StringVar(&repo, "repo", "", "repository working tree")
	flag.Parse()
	if base == "" || len(head) == 0 || flag.NArg() != 0 {
		fatalf("usage: impact.sh --base COMMIT --head COMMIT [--format pretty|compact|github|plan]")
	}

	if repo == "" {
		var err error
		repo, err = os.Getwd()
		if err != nil {
			fatalf("get working directory: %v", err)
		}
	}
	repo, err := gitOutputIn(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		fatalf("find repository root: %v", err)
	}
	repo = strings.TrimSpace(repo)
	baseSHA, err := resolveCommit(repo, base)
	if err != nil {
		fatalf("resolve base %q: %v", base, err)
	}
	headSHA, err := resolveCommit(repo, head)
	if err != nil {
		fatalf("resolve head %q: %v", head, err)
	}
	checkoutSHA, err := gitOutputIn(repo, "rev-parse", "HEAD")
	if err != nil {
		fatalf("resolve checkout HEAD: %v", err)
	}
	if strings.TrimSpace(checkoutSHA) != headSHA {
		fatalf("head %s is not the checked-out commit %s", headSHA, strings.TrimSpace(checkoutSHA))
	}

	changes, err := changedPaths(repo, baseSHA, headSHA)
	if err != nil {
		fatalf("read changed paths: %v", err)
	}
	packages, err := listPackages(repo)
	if err != nil {
		fatalf("list Go packages at head: %v", err)
	}
	modulePath, err := moduleAt(repo, headSHA)
	if err != nil {
		fatalf("read module path: %v", err)
	}

	result := classify(repo, baseSHA, headSHA, modulePath, changes, packages)
	if err := writeManifest(os.Stdout, result, outputFormat); err != nil {
		fatalf("write manifest: %v", err)
	}
	if len(result.Unclassified) != 0 {
		os.Exit(3)
	}
}

func writeManifest(writer io.Writer, result manifest, outputFormat string) error {
	switch outputFormat {
	case "pretty":
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	case "compact":
		content, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(writer, "%s\n", content)
		return err
	case "github":
		content, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "base=%s\nhead=%s\nmanifest=%s\n",
			result.Base, result.Head, content); err != nil {
			return err
		}
		for _, item := range checkValues(result.Checks) {
			if _, err := fmt.Fprintf(writer, "%s=%t\n", item.name, item.enabled); err != nil {
				return err
			}
		}
		return nil
	case "plan":
		fields := []string{result.Base, result.Head}
		fields = append(fields, result.Go.Owners...)
		fields = append(fields, result.Go.CompilePackages...)
		for _, paths := range result.Reasons {
			fields = append(fields, paths...)
		}
		for _, field := range fields {
			if strings.ContainsAny(field, "\t\r\n") {
				return fmt.Errorf("plan field contains a tab or newline: %q", field)
			}
		}
		if _, err := fmt.Fprintf(writer, "schema_version\t%d\nbase\t%s\nhead\t%s\ncompile_all\t%t\n",
			result.SchemaVersion, result.Base, result.Head, result.Go.CompileAll); err != nil {
			return err
		}
		for _, item := range checkValues(result.Checks) {
			if _, err := fmt.Fprintf(writer, "check\t%s\t%t\n", item.name, item.enabled); err != nil {
				return err
			}
		}
		for _, owner := range result.Go.Owners {
			if _, err := fmt.Fprintf(writer, "owner\t%s\n", owner); err != nil {
				return err
			}
		}
		for _, pkg := range result.Go.CompilePackages {
			if _, err := fmt.Fprintf(writer, "compile_package\t%s\n", pkg); err != nil {
				return err
			}
		}
		reasonNames := make([]string, 0, len(result.Reasons))
		for name := range result.Reasons {
			reasonNames = append(reasonNames, name)
		}
		sort.Strings(reasonNames)
		for _, name := range reasonNames {
			for _, path := range result.Reasons[name] {
				if _, err := fmt.Fprintf(writer, "reason\t%s\t%s\n", name, path); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown output format %q", outputFormat)
	}
}

type checkValue struct {
	name    string
	enabled bool
}

func checkValues(values checks) []checkValue {
	return []checkValue{
		{name: "focused_quality", enabled: values.FocusedQuality},
		{name: "contract_race", enabled: values.ContractRace},
		{name: "migration_recovery", enabled: values.MigrationRecovery},
		{name: "tui_performance", enabled: values.TUIPerformance},
		{name: "binary_release_contract", enabled: values.BinaryReleaseContract},
		{name: "ci_contract", enabled: values.CIContract},
		{name: "docs_contract", enabled: values.DocsContract},
		{name: "sonar", enabled: values.Sonar},
	}
}

func classify(repo, base, head, modulePath string, changes []change, packages []packageInfo) manifest {
	result := manifest{
		SchemaVersion: schemaVersion,
		Base:          base,
		Head:          head,
		Changes:       changes,
		Reasons:       make(map[string][]string),
	}
	dirToPackage := make(map[string]string)
	imports := make(map[string][]string)
	allPackages := make([]string, 0, len(packages))
	for _, pkg := range packages {
		rel, err := filepath.Rel(repo, pkg.Dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		dirToPackage[rel] = pkg.ImportPath
		imports[pkg.ImportPath] = pkg.Imports
		allPackages = append(allPackages, pkg.ImportPath)
	}
	sort.Strings(allPackages)

	ownerSet := make(map[string]bool)
	deletedSet := make(map[string]bool)
	changedGoSet := make(map[string]bool)
	unclassifiedSet := make(map[string]bool)
	classified := make(map[string]bool)

	for _, item := range changes {
		for pathIndex, path := range item.Paths {
			isOldRenamePath := strings.HasPrefix(item.Status, "R") && pathIndex == 0
			isDeleted := item.Status == "D" || isOldRenamePath
			classifyPath(&result, path, classified)

			if path == "go.mod" || path == "go.sum" {
				result.Go.CompileAll = true
				addReason(result.Reasons, "focused_quality", path)
			}
			if strings.HasSuffix(path, ".go") && !strings.HasPrefix(path, "scripts/") {
				dir := filepath.ToSlash(filepath.Dir(path))
				if dir == "." {
					dir = ""
				}
				if pkg, ok := dirToPackage[dir]; ok {
					ownerSet[pkg] = true
				} else if isDeleted {
					deletedSet[modulePackage(modulePath, dir)] = true
					result.Go.CompileAll = true
				}
				if !isDeleted && pathExistsAt(repo, head, path) {
					changedGoSet[path] = true
				}
			}
			if strings.HasPrefix(path, "internal/ai/skills/") && strings.HasSuffix(path, ".md") {
				if pkg := dirToPackage["internal/ai"]; pkg != "" {
					ownerSet[pkg] = true
				}
			}
		}
	}

	for _, item := range changes {
		for _, path := range item.Paths {
			if !classified[path] {
				unclassifiedSet[path] = true
			}
		}
	}

	result.Go.Owners = sortedKeys(ownerSet)
	result.Go.DeletedPackages = sortedKeys(deletedSet)
	result.Go.ChangedFiles = sortedKeys(changedGoSet)
	result.Unclassified = sortedKeys(unclassifiedSet)
	if len(result.Go.ChangedFiles) == 0 {
		result.Checks.Sonar = false
		delete(result.Reasons, "sonar")
	}

	importerSet := make(map[string]bool)
	for _, owner := range result.Go.Owners {
		for pkg, pkgImports := range imports {
			if ownerSet[pkg] || contains(pkgImports, owner) {
				if !ownerSet[pkg] && contains(pkgImports, owner) {
					importerSet[pkg] = true
				}
			}
		}
	}
	result.Go.DirectImporters = sortedKeys(importerSet)
	if result.Go.CompileAll {
		result.Go.CompilePackages = allPackages
	} else {
		result.Go.CompilePackages = append([]string(nil), result.Go.DirectImporters...)
	}

	if len(result.Go.Owners) > 0 || len(result.Go.DeletedPackages) > 0 || result.Go.CompileAll {
		result.Checks.FocusedQuality = true
	}
	return result
}

func classifyPath(result *manifest, path string, classified map[string]bool) {
	mark := func(checkName string, target *bool) {
		*target = true
		classified[path] = true
		addReason(result.Reasons, checkName, path)
	}

	switch {
	case path == "go.mod" || path == "go.sum":
		mark("focused_quality", &result.Checks.FocusedQuality)
		mark("binary_release_contract", &result.Checks.BinaryReleaseContract)
	case strings.HasSuffix(path, ".go") && !strings.HasPrefix(path, "scripts/"):
		mark("focused_quality", &result.Checks.FocusedQuality)
		mark("sonar", &result.Checks.Sonar)
		if filepath.Dir(path) == "." {
			mark("binary_release_contract", &result.Checks.BinaryReleaseContract)
		}
	case strings.HasPrefix(path, "internal/ai/skills/") && strings.HasSuffix(path, ".md"):
		mark("focused_quality", &result.Checks.FocusedQuality)
		mark("binary_release_contract", &result.Checks.BinaryReleaseContract)
	case path == "scripts/check-go-coverage.sh" || path == "scripts/check-go-format.sh" ||
		path == "scripts/check-go-checkers.test.sh" || path == "scripts/check-docs.sh":
		mark("ci_contract", &result.Checks.CIContract)
	case strings.HasPrefix(path, "scripts/ci/") || strings.HasPrefix(path, ".github/workflows/"):
		mark("ci_contract", &result.Checks.CIContract)
		if path == ".github/workflows/release.yml" {
			mark("binary_release_contract", &result.Checks.BinaryReleaseContract)
		}
	case path == "scripts/release.sh" || path == "scripts/release.test.sh" ||
		path == "scripts/verify-release-artifacts.sh":
		mark("binary_release_contract", &result.Checks.BinaryReleaseContract)
		mark("ci_contract", &result.Checks.CIContract)
	case path == "scripts/ci_monitor.cjs" || path == "scripts/ci/test_ci_monitor.cjs":
		mark("ci_contract", &result.Checks.CIContract)
	case path == "README.md" || path == "LICENSE" || strings.HasPrefix(path, "docs/") || strings.HasPrefix(path, ".claude/"):
		mark("docs_contract", &result.Checks.DocsContract)
		if strings.HasPrefix(path, "docs/releases/") {
			mark("binary_release_contract", &result.Checks.BinaryReleaseContract)
		}
	case path == ".gitattributes" || path == ".gitignore" || path == ".github/CODEOWNERS":
		mark("ci_contract", &result.Checks.CIContract)
	}

	if matchesAny(path,
		"internal/store/migrate.go", "internal/store/migrate_test.go",
		"internal/store/store.go", "internal/store/store_test.go",
		"internal/store/schema.go", "internal/store/settings.go",
		"internal/store/settings_secret_test.go", "internal/tui/preferences.go",
		"internal/tui/preferences_test.go") {
		mark("migration_recovery", &result.Checks.MigrationRecovery)
	}
	if matchesAny(path,
		"internal/store/migrate.go", "internal/store/store.go", "internal/store/forge.go",
		"internal/store/guard.go", "internal/tui/watcher.go", "internal/tui/pointer_mailbox.go",
		"internal/tui/pointer_admission.go", "internal/tui/keyboard_admission.go") {
		mark("contract_race", &result.Checks.ContractRace)
	}
	performancePath := matchesAny(path,
		"internal/tui/render_plan.go", "internal/tui/render_geometry.go",
		"internal/tui/render_projection.go", "internal/tui/watcher.go",
		"internal/tui/keyboard_admission.go", "internal/tui/pointer_admission.go",
		"internal/tui/pointer_mailbox.go", "internal/tui/temporal.go", "internal/tui/run.go")
	if strings.HasPrefix(path, "internal/tui/performance_") {
		performancePath = true
	}
	if strings.HasPrefix(path, "internal/tui/pointer/") {
		performancePath = true
	}
	if performancePath {
		mark("tui_performance", &result.Checks.TUIPerformance)
	}
}

func changedPaths(repo, base, head string) ([]change, error) {
	cmd := exec.Command("git", "diff", "--name-status", "-z", "--find-renames", "--diff-filter=ACDMRTUXB", base, head, "--")
	cmd.Dir = repo
	output, err := cmd.Output()
	if err != nil {
		return nil, commandError(cmd, err)
	}
	fields := bytes.Split(output, []byte{0})
	if len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
		fields = fields[:len(fields)-1]
	}
	var result []change
	for index := 0; index < len(fields); {
		status := string(fields[index])
		index++
		pathCount := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			pathCount = 2
		}
		if len(fields)-index < pathCount {
			return nil, fmt.Errorf("truncated diff record for status %q", status)
		}
		paths := make([]string, pathCount)
		for pathIndex := range paths {
			paths[pathIndex] = string(fields[index])
			index++
		}
		result = append(result, change{Status: status, Paths: paths})
	}
	return result, nil
}

func listPackages(repo string) ([]packageInfo, error) {
	cmd := exec.Command("go", "list", "-e", "-buildvcs=false", "-json", "./...")
	cmd.Dir = repo
	output, err := cmd.Output()
	if err != nil {
		return nil, commandError(cmd, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []packageInfo
	for {
		var pkg packageInfo
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if pkg.ImportPath != "" && pkg.Dir != "" {
			packages = append(packages, pkg)
		}
	}
	return packages, nil
}

func moduleAt(repo, head string) (string, error) {
	content, err := gitOutputIn(repo, "show", head+":go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", errors.New("go.mod has no module directive")
}

func resolveCommit(repo, revision string) (string, error) {
	output, err := gitOutputIn(repo, "rev-parse", "--verify", revision+"^{commit}")
	return strings.TrimSpace(output), err
}

func pathExistsAt(repo, revision, path string) bool {
	cmd := exec.Command("git", "cat-file", "-e", revision+":"+path)
	cmd.Dir = repo
	return cmd.Run() == nil
}

func modulePackage(modulePath, dir string) string {
	if dir == "" {
		return modulePath
	}
	return modulePath + "/" + dir
}

func matchesAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func addReason(reasons map[string][]string, name, path string) {
	for _, existing := range reasons[name] {
		if existing == path {
			return
		}
	}
	reasons[name] = append(reasons[name], path)
	sort.Strings(reasons[name])
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func gitOutputIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", commandError(cmd, err)
	}
	return string(output), nil
}

func commandError(cmd *exec.Cmd, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := strings.TrimSpace(string(exitErr.Stderr))
		if message != "" {
			return fmt.Errorf("%s: %w", message, err)
		}
	}
	return err
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "impact: "+format+"\n", args...)
	os.Exit(2)
}
