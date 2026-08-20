#!/usr/bin/env bash
# Build, verify, tag, and publish one immutable kb release.
#
# Usage: scripts/release.sh vX.Y.Z notes-file [--dry-run]
#
# Run this from a clean plain clone on linux/amd64. A dry run performs the
# complete local build and smoke suite, then removes its temporary annotated
# tag. It does not contact GitHub or mutate the remote repository.
#
# A published tag is permanent. If publication fails after the tag is pushed,
# fix the problem in the next patch release. Never move or delete the tag.
set -euo pipefail

die() {
  printf 'release: %s\n' "$*" >&2
  exit 2
}

dry_run=0
positional=()
for argument in "$@"; do
  case "$argument" in
    --dry-run) dry_run=1 ;;
    *) positional+=("$argument") ;;
  esac
done

[[ ${#positional[@]} -eq 2 ]] || \
  die 'usage: scripts/release.sh vX.Y.Z notes-file [--dry-run]'
version=${positional[0]}
notes_file=${positional[1]}
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || \
  die 'version must match vX.Y.Z'

for command_name in git go file sha256sum curl timeout script; do
  command -v "$command_name" >/dev/null 2>&1 || \
    die "required command not found: $command_name"
done
if [[ $dry_run == 0 ]]; then
  command -v gh >/dev/null 2>&1 || die 'required command not found: gh'
fi

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || \
  die 'not inside a git working tree'
cd "$repo_root"

git_dir=$(cd "$(git rev-parse --git-dir)" && pwd -P)
common_dir=$(cd "$(git rev-parse --git-common-dir)" && pwd -P)
[[ $git_dir == "$common_dir" ]] || \
  die 'linked git worktrees are not release sources; use a clean plain clone'

status_before=$(git status --porcelain=v2 --untracked-files=all)
[[ -z $status_before ]] || \
  die 'working tree, index, or untracked-file set is not clean'
[[ -f $notes_file && -r $notes_file && -s $notes_file ]] || \
  die "release notes must be a readable, non-empty regular file: $notes_file"

source_commit=$(git rev-parse HEAD)
source_tree=$(git rev-parse 'HEAD^{tree}')
source_index=$(git write-tree)
[[ $source_tree == "$source_index" ]] || die 'index does not match HEAD'

module_path=$(go list -m -f '{{.Path}}')
expected_go_version=$(awk '$1 == "go" { print "go" $2; exit }' go.mod)
[[ -n $module_path && -n $expected_go_version ]] || \
  die 'could not read module or Go version from go.mod'
[[ $(go env GOVERSION) == "$expected_go_version" ]] || \
  die "release builds require $expected_go_version exactly; select it with GOTOOLCHAIN=$expected_go_version"

require_current_origin_main() {
  local remote_main
  remote_main=$(git ls-remote origin refs/heads/main) || \
    die 'could not inspect main on origin'
  remote_main=${remote_main%%[[:space:]]*}
  [[ -n $remote_main ]] || die 'origin has no main branch'
  [[ $source_commit == "$remote_main" ]] || \
    die "release source $source_commit is not current origin/main $remote_main"
}

require_publish_access() {
  local expected_repo=${module_path#github.com/}
  local authenticated_repo
  [[ $expected_repo != "$module_path" ]] || \
    die "release module is not hosted on GitHub: $module_path"
  gh auth status --hostname github.com >/dev/null 2>&1 || \
    die 'gh is not authenticated to github.com'
  authenticated_repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner) || \
    die 'gh cannot access the repository selected by origin'
  [[ $authenticated_repo == "$expected_repo" ]] || \
    die "origin selects $authenticated_repo, want $expected_repo"
}

if git show-ref --verify --quiet "refs/tags/$version"; then
  die "local tag $version already exists; cut the next version instead"
fi

# A fresh workflow checkout has every remote tag locally. The publish path
# also queries origin immediately before creating anything, closing the race
# between the checkout and this script. Dry runs remain deliberately offline.
if [[ $dry_run == 0 ]]; then
  require_publish_access
  require_current_origin_main

  remote_tags=$(git ls-remote --tags origin \
    "refs/tags/$version" "refs/tags/$version^{}") || \
    die 'could not inspect tags on origin'
  [[ -z $remote_tags ]] || \
    die "remote tag $version already exists; cut the next version instead"
fi

tag_created=0
tag_pushed=0
output_dir=''
serve_pid=''

source_is_unchanged() {
  [[ $(git rev-parse HEAD) == "$source_commit" ]] &&
    [[ $(git rev-parse 'HEAD^{tree}') == "$source_tree" ]] &&
    [[ $(git write-tree) == "$source_index" ]] &&
    [[ -z $(git status --porcelain=v2 --untracked-files=all) ]]
}

cleanup() {
  local result=$?
  trap - EXIT HUP INT TERM

  if [[ -n $serve_pid ]]; then
    kill "$serve_pid" >/dev/null 2>&1 || true
    wait "$serve_pid" >/dev/null 2>&1 || true
  fi
  if [[ $tag_created == 1 && $tag_pushed == 0 ]]; then
    git tag -d "$version" >/dev/null 2>&1 || result=1
    tag_created=0
  fi
  if [[ -n $output_dir && -d $output_dir ]]; then
    case $output_dir in
      "${TMPDIR:-/tmp}"/kb-release.*) rm -rf -- "$output_dir" ;;
      *) printf 'release: refusing to remove unexpected path: %s\n' "$output_dir" >&2; result=1 ;;
    esac
  fi
  if ! source_is_unchanged; then
    printf 'release: HEAD, tree, index, or status changed during release\n' >&2
    result=1
  fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

go test ./...

# The tag points directly at the reviewed source commit. No generated commit,
# detached checkout, reset, or tracked build output is involved.
git tag -a "$version" -m "$version" "$source_commit"
tag_created=1
[[ $(git cat-file -t "refs/tags/$version") == tag ]] || \
  die "$version is not an annotated tag"
[[ $(git rev-parse "$version^{}") == "$source_commit" ]] || \
  die "$version does not dereference to the source commit"

output_dir=$(mktemp -d "${TMPDIR:-/tmp}/kb-release.XXXXXX")
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
  local tui_command port health_url
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
  grep -F 'serve      run the optional HTTP API server' "$smoke/root-help.txt" >/dev/null || \
    die 'root help smoke omitted kb serve'
  grep -F '  add "title"' "$smoke/cli-help.txt" >/dev/null || \
    die 'CLI help smoke omitted kb add'

  "$native" add 'Release smoke' --data "$smoke/cli-data" \
    >"$smoke/add.txt"
  "$native" list --data "$smoke/cli-data" --json \
    >"$smoke/list.json"
  grep -F 'Release smoke' "$smoke/list.json" >/dev/null || \
    die 'local CLI smoke did not round-trip its task'

  printf -v tui_command '%q tui --data %q' \
    "$native" "$smoke/tui-data"
  if ! printf 'q' | TERM=xterm-256color timeout 10s \
    script --quiet --return --command "$tui_command" "$smoke/tui.typescript" \
    >"$smoke/tui.txt" 2>&1; then
    die 'TUI launch/quit smoke failed'
  fi

  port=$((30000 + (RANDOM % 20000)))
  health_url="http://127.0.0.1:$port/api/health"
  KB_BIND=127.0.0.1 "$native" serve --port "$port" \
    --data "$smoke/serve-data" >"$smoke/serve.txt" 2>&1 &
  serve_pid=$!
  for _ in {1..100}; do
    if curl --fail --silent --show-error --connect-timeout 0.2 --max-time 0.5 \
      "$health_url" >"$smoke/health.json"; then
      break
    fi
    if ! kill -0 "$serve_pid" 2>/dev/null; then
      die 'serve smoke exited before becoming healthy'
    fi
    sleep 0.05
  done
  grep -F '"ok":true' "$smoke/health.json" >/dev/null || \
    die 'serve smoke did not return a healthy API response'
  kill "$serve_pid"
  wait "$serve_pid" >/dev/null 2>&1 || true
  serve_pid=''
}

run_native_smokes

if [[ $dry_run == 1 ]]; then
  git tag -d "$version" >/dev/null
  tag_created=0
  source_is_unchanged || die 'dry run changed HEAD, tree, index, or status'
  printf 'release: dry run complete: %s verified locally; nothing published\n' "$version"
  exit 0
fi

require_publish_access
require_current_origin_main
git push origin "refs/tags/$version:refs/tags/$version"
tag_pushed=1

gh release create "$version" \
  --verify-tag \
  --title "$version" \
  --notes-file "$notes_file" \
  "$output_dir"/kb-linux-amd64 \
  "$output_dir"/kb-linux-arm64 \
  "$output_dir"/kb-darwin-amd64 \
  "$output_dir"/kb-darwin-arm64 \
  "$output_dir"/kb-windows-amd64.exe \
  "$output_dir"/SHA256SUMS

printf 'release: %s published from %s\n' "$version" "$source_commit"
printf 'verify: gh release verify %s --repo %s\n' "$version" "$module_path"
