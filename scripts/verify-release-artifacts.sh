#!/usr/bin/env bash
# Build and verify the five release binaries for one already-tagged commit.
set -euo pipefail

die() {
  printf 'release artifacts: %s\n' "$*" >&2
  exit 2
}

[[ $# -eq 3 ]] || \
  die 'usage: scripts/verify-release-artifacts.sh vX.Y.Z SOURCE_COMMIT OUTPUT_DIR'
version=$1
source_commit=$2
output_dir=$3
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || \
  die 'version must match vX.Y.Z'

for command_name in git go file sha256sum timeout script; do
  command -v "$command_name" >/dev/null 2>&1 || \
    die "required command not found: $command_name"
done

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || \
  die 'not inside a git working tree'
cd "$repo_root"

resolved_commit=$(git rev-parse --verify "$source_commit^{commit}" 2>/dev/null) || \
  die "source commit does not resolve: $source_commit"
[[ $resolved_commit == "$source_commit" ]] || \
  die "source commit must be the full commit ID: $source_commit"
[[ $(git rev-parse HEAD) == "$source_commit" ]] || \
  die "source commit $source_commit is not checked out"
[[ $(git cat-file -t "refs/tags/$version" 2>/dev/null) == tag ]] || \
  die "$version is not an annotated tag"
[[ $(git rev-parse "$version^{}") == "$source_commit" ]] || \
  die "$version does not dereference to the source commit"
[[ -d $output_dir ]] || die "output directory does not exist: $output_dir"
[[ -z $(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit) ]] || \
  die "output directory is not empty: $output_dir"

module_path=$(go list -m -f '{{.Path}}')
expected_go_version=$(awk '$1 == "go" { print "go" $2; exit }' go.mod)
[[ -n $module_path && -n $expected_go_version ]] || \
  die 'could not read module or Go version from go.mod'
[[ $(go env GOVERSION) == "$expected_go_version" ]] || \
  die "release builds require $expected_go_version exactly; select it with GOTOOLCHAIN=$expected_go_version"

targets=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
  windows/amd64
)

metadata_value() {
  local artifact=$1
  local key=$2
  go version -m "$artifact" | awk -F '\t' -v wanted="$key" '
    $2 == "build" && index($3, wanted "=") == 1 {
      print substr($3, length(wanted) + 2)
      exit
    }
  '
}

verify_artifact() {
  local artifact=$1
  local goos=$2
  local goarch=$3
  local description metadata recorded_module recorded_version recorded_go

  description=$(file -b "$artifact")
  case "$goos/$goarch" in
    linux/amd64)  [[ $description == *'ELF 64-bit LSB'*x86-64* ]] ;;
    linux/arm64)  [[ $description == *'ELF 64-bit LSB'*ARM\ aarch64* ]] ;;
    darwin/amd64) [[ $description == *'Mach-O 64-bit x86_64'* ]] ;;
    darwin/arm64) [[ $description == *'Mach-O 64-bit arm64'* ]] ;;
    windows/amd64) [[ $description == *'PE32+'*x86-64* ]] ;;
    *) return 1 ;;
  esac || die "unexpected file format for $(basename "$artifact"): $description"

  metadata=$(go version -m "$artifact") || \
    die "cannot read Go metadata from $(basename "$artifact")"
  recorded_go=$(printf '%s\n' "$metadata" | awk 'NR == 1 { print $NF }')
  recorded_module=$(printf '%s\n' "$metadata" | awk -F '\t' '$2 == "mod" { print $3; exit }')
  recorded_version=$(printf '%s\n' "$metadata" | awk -F '\t' '$2 == "mod" { print $4; exit }')

  [[ $recorded_go == "$expected_go_version" ]] || \
    die "$(basename "$artifact") uses $recorded_go, want $expected_go_version"
  [[ $recorded_module == "$module_path" ]] || \
    die "$(basename "$artifact") module is $recorded_module, want $module_path"
  [[ $recorded_version == "$version" ]] || \
    die "$(basename "$artifact") version is $recorded_version, want $version"
  [[ $(metadata_value "$artifact" vcs.revision) == "$source_commit" ]] || \
    die "$(basename "$artifact") has the wrong source revision"
  [[ $(metadata_value "$artifact" GOOS) == "$goos" ]] || \
    die "$(basename "$artifact") has the wrong GOOS"
  [[ $(metadata_value "$artifact" GOARCH) == "$goarch" ]] || \
    die "$(basename "$artifact") has the wrong GOARCH"
  [[ $(metadata_value "$artifact" vcs.modified) == false ]] || \
    die "$(basename "$artifact") is stamped as modified"
}

for target in "${targets[@]}"; do
  goos=${target%/*}
  goarch=${target#*/}
  artifact="$output_dir/kb-$goos-$goarch"
  [[ $goos == windows ]] && artifact+='.exe'
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
    go build -trimpath -ldflags='-s -w' -o "$artifact" .
  verify_artifact "$artifact" "$goos" "$goarch"
done

(
  cd "$output_dir"
  sha256sum kb-* >SHA256SUMS
  sha256sum -c SHA256SUMS
)

run_native_smokes() {
  local native="$output_dir/kb-linux-amd64"
  local smoke="$output_dir/smoke"
  local tui_command
  mkdir -p "$smoke"

  [[ $(go env GOOS) == linux && $(go env GOARCH) == amd64 ]] || \
    die 'native release smokes require linux/amd64'
  [[ $($native version) == "kb $version (${source_commit:0:12})" ]] || \
    die 'native version smoke returned the wrong version'

  KB_DATA="$smoke/default-data" "$native" </dev/null >"$smoke/default.txt"
  grep -F 'usage: kb' "$smoke/default.txt" >/dev/null || \
    die 'bare non-TTY smoke did not print root help'
  [[ ! -e $smoke/default-data ]] || \
    die 'bare non-TTY smoke opened the data directory'
  "$native" --help >"$smoke/root-help.txt"
  "$native" help >"$smoke/cli-help.txt"
  if grep -F 'kb serve' "$smoke/root-help.txt" >/dev/null; then
    die 'root help smoke still advertises removed hosting'
  fi
  grep -F '  add "title"' "$smoke/cli-help.txt" >/dev/null || \
    die 'CLI help smoke omitted kb add'

  "$native" project use release-smoke --data "$smoke/cli-data" \
    >"$smoke/project-use.txt"
  "$native" add 'Release smoke' --data "$smoke/cli-data" \
    >"$smoke/add.txt"
  "$native" list --data "$smoke/cli-data" --json \
    >"$smoke/list.json"
  grep -F 'Release smoke' "$smoke/list.json" >/dev/null || \
    die 'local CLI smoke did not round-trip its task'
  grep -F 'project::release-smoke' "$smoke/list.json" >/dev/null || \
    die 'local CLI smoke did not stamp the active project'

  printf -v tui_command '%q tui --data %q' \
    "$native" "$smoke/tui-data"
  if ! printf 'q' | TERM=xterm-256color timeout 10s \
    script --quiet --return --command "$tui_command" "$smoke/tui.typescript" \
    >"$smoke/tui.txt" 2>&1; then
    die 'TUI launch/quit smoke failed'
  fi
}

run_native_smokes
printf 'release artifacts: %s verified from %s\n' "$version" "$source_commit"
