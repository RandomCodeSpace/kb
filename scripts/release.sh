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

for command_name in git go file sha256sum timeout script; do
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
artifact_dir=''

source_is_unchanged() {
  [[ $(git rev-parse HEAD) == "$source_commit" ]] &&
    [[ $(git rev-parse 'HEAD^{tree}') == "$source_tree" ]] &&
    [[ $(git write-tree) == "$source_index" ]] &&
    [[ -z $(git status --porcelain=v2 --untracked-files=all) ]]
}

cleanup() {
  local result=$?
  trap - EXIT HUP INT TERM

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

output_dir=$(mktemp -d "${TMPDIR:-/tmp}/kb-release.XXXXXX")
artifact_dir="$output_dir/artifacts"
mkdir "$artifact_dir"

existing_version_tags=$(git tag --points-at "$source_commit" --list 'v[0-9]*.[0-9]*.[0-9]*')
[[ -z $existing_version_tags ]] || \
  die "release source already has a version tag: ${existing_version_tags//$'\n'/, }"
previous_tag=$(git describe --tags --abbrev=0 \
  --match 'v[0-9]*.[0-9]*.[0-9]*' "$source_commit" 2>/dev/null) || \
  die 'release source has no reachable previous version tag'
previous_commit=$(git rev-parse "$previous_tag^{commit}") || \
  die "previous version tag does not resolve to a commit: $previous_tag"

impact_plan="$output_dir/impact.plan"
sh scripts/ci/impact.sh --base "$previous_tag" --head "$source_commit" \
  --format plan >"$impact_plan"

impact_schema=''
impact_base=''
impact_head=''
impact_compile_all=''
focused_quality=''
contract_race=''
migration_recovery=''
tui_performance=''
binary_release_contract=''
ci_contract=''
docs_contract=''
sonar=''
impact_owners=()
impact_compile_packages=()
contract_race_reasons=()
binary_release_reasons=()
ci_contract_reasons=()

while IFS=$'\t' read -r record first second extra; do
  [[ -z ${extra:-} ]] || die "malformed impact plan record: $record"
  case "$record" in
    schema_version) impact_schema=$first ;;
    base) impact_base=$first ;;
    head) impact_head=$first ;;
    compile_all) impact_compile_all=$first ;;
    check)
      case "$first" in
        focused_quality|contract_race|migration_recovery|tui_performance|binary_release_contract|ci_contract|docs_contract|sonar)
          printf -v "$first" '%s' "$second"
          ;;
        *) die "unknown impact check: $first" ;;
      esac
      ;;
    owner) impact_owners+=("$first") ;;
    compile_package) impact_compile_packages+=("$first") ;;
    reason)
      case "$first" in
        contract_race) contract_race_reasons+=("$second") ;;
        binary_release_contract) binary_release_reasons+=("$second") ;;
        ci_contract) ci_contract_reasons+=("$second") ;;
      esac
      ;;
    '') ;;
    *) die "unknown impact plan record: $record" ;;
  esac
done <"$impact_plan"

[[ $impact_schema == 1 ]] || die "unsupported impact plan schema: ${impact_schema:-missing}"
[[ $impact_base == "$previous_commit" ]] || die 'impact plan resolved the wrong previous release'
[[ $impact_head == "$source_commit" ]] || die 'impact plan resolved the wrong candidate'
[[ $impact_compile_all == true || $impact_compile_all == false ]] || \
  die 'impact plan omitted compile_all'
for check_name in focused_quality contract_race migration_recovery tui_performance \
  binary_release_contract ci_contract docs_contract sonar; do
  check_value=${!check_name}
  [[ $check_value == true || $check_value == false ]] || \
    die "impact plan omitted check: $check_name"
done
printf 'release: impact %s (%s) -> %s\n' "$previous_tag" "$previous_commit" "$source_commit"

array_contains() {
  local wanted=$1
  shift
  local value
  for value in "$@"; do
    [[ $value == "$wanted" ]] && return 0
  done
  return 1
}

if [[ $focused_quality == true ]]; then
  sh scripts/check-go-format.sh --changed "$previous_commit" "$source_commit"
  if [[ ${#impact_owners[@]} -gt 0 ]]; then
    GO_COVERAGE_PROFILE="$output_dir/go-coverage.out" \
      sh scripts/check-go-coverage.sh --packages "${impact_owners[@]}"
    go vet -buildvcs=false "${impact_owners[@]}"
  fi
  if [[ ${#impact_compile_packages[@]} -gt 0 ]]; then
    go test -buildvcs=false -run '^$' "${impact_compile_packages[@]}"
  fi
else
  printf 'release: focused-quality not affected\n'
fi

if [[ $contract_race == true ]]; then
  race_store=0
  race_tui=0
  race_forge=0
  for reason in "${contract_race_reasons[@]}"; do
    case "$reason" in
      internal/store/*) race_store=1 ;;
      internal/tui/*) race_tui=1 ;;
      internal/forge/*) race_forge=1 ;;
    esac
  done
  [[ $race_store == 1 || $race_tui == 1 || $race_forge == 1 ]] || \
    die 'contract-race was selected without a mapped owning package'
  if [[ $race_store == 1 ]]; then
    go test -buildvcs=false -race ./internal/store \
      -run '^(TestCreateSecretConcurrentPublication|TestConcurrentOpenSerializesMigration|TestDoneGuardCannotRaceConcurrentBlock|TestImportBaselineCreateAndCASAreAtomicAcrossCallers)$' \
      -count=1
  fi
  if [[ $race_tui == 1 ]]; then
    go test -buildvcs=false -race ./internal/tui \
      -run '^(TestDataVersionWatcherDetectsAnotherConnection|TestRunCancelsInFlightWatcherBeforeClose|TestMoveSerializesWatcherRefreshBehindStoreWrite|TestAutoShipTransactionRefusesConcurrentCancellation|TestKeyboardAdmissionQueueIsConstantUnderBurst|TestRenderedPointerMailboxOverflowFailsClosed|TestModelCopiesShareRenderedPointerMailbox)$' \
      -count=1
  fi
  if [[ $race_forge == 1 ]]; then
    go test -buildvcs=false -race ./internal/forge \
      -run '^(TestAcceptDriftConcurrentCASIsIdempotent|TestAcceptDriftRejectsConcurrentDifferentBaseline)$' \
      -count=1
  fi
else
  printf 'release: contract-race not affected\n'
fi

if [[ $migration_recovery == true ]]; then
  if array_contains 'github.com/RandomCodeSpace/kb/internal/store' "${impact_owners[@]}"; then
    printf 'release: store migration/recovery covered by focused owner tests\n'
  else
    go test -buildvcs=false ./internal/store \
      -run '^(TestColdCopyRecoveryRoundTrip|TestLoadOrCreateSecret.*|TestOpenRejectsInvalidPathsAndSchemas|TestConnectionPragmas|TestConcurrentOpenSerializesMigration)$' \
      -count=1
  fi
  if array_contains 'github.com/RandomCodeSpace/kb/internal/tui' "${impact_owners[@]}"; then
    printf 'release: TUI preferences covered by focused owner tests\n'
  else
    go test -buildvcs=false ./internal/tui \
      -run '^(TestPreference.*|TestCancelledPreference.*)' -count=1
  fi
else
  printf 'release: migration-recovery not affected\n'
fi

if [[ $tui_performance == true ]]; then
  if ! array_contains 'github.com/RandomCodeSpace/kb/internal/tui' "${impact_owners[@]}"; then
    go test -buildvcs=false ./internal/tui \
      -run '^(TestPerformance.*|TestPointerResolverCostIsBoundedByVisibleHitSnapshot|TestNavigationArtifactAcceptanceRejectsMissingOrUnboundedReuse)$' \
      -count=1
  fi
  KB_PERF_ACCEPT=1 KB_PERF_CORPORA=17,120,500,1000 \
    KB_PERF_REPORT="$output_dir/tui-performance.json" GOMAXPROCS=8 \
    go test -buildvcs=false ./internal/tui \
      -run '^TestLargeBoardPerformanceHarness$' -count=1 -timeout=15m
else
  printf 'release: tui-performance not affected\n'
fi

if [[ $ci_contract == true ]]; then
  sh scripts/ci/test-impact.sh
  sh scripts/check-go-checkers.test.sh
  command -v node >/dev/null 2>&1 || die 'required command not found: node'
  node scripts/ci/test_ci_monitor.cjs
  node scripts/ci_monitor.cjs check-actions
  if array_contains 'scripts/check-docs.sh' "${ci_contract_reasons[@]}" && \
    [[ $docs_contract == false ]]; then
    sh scripts/check-docs.sh
  fi
else
  printf 'release: ci-contract not affected\n'
fi

if [[ $docs_contract == true ]]; then
  sh scripts/check-docs.sh
else
  printf 'release: docs-contract not affected\n'
fi

release_safeguards=0
for reason in "${binary_release_reasons[@]}"; do
  case "$reason" in
    scripts/release.sh|scripts/release.test.sh|scripts/verify-release-artifacts.sh|.github/workflows/release.yml)
      release_safeguards=1
      ;;
  esac
done
if [[ $release_safeguards == 1 ]]; then
  bash scripts/release.test.sh
fi

# The tag points directly at the reviewed source commit. No generated commit,
# detached checkout, reset, or tracked build output is involved.
git tag -a "$version" -m "$version" "$source_commit"
tag_created=1
[[ $(git cat-file -t "refs/tags/$version") == tag ]] || \
  die "$version is not an annotated tag"
[[ $(git rev-parse "$version^{}") == "$source_commit" ]] || \
  die "$version does not dereference to the source commit"

bash scripts/verify-release-artifacts.sh "$version" "$source_commit" "$artifact_dir"

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
  "$artifact_dir"/kb-linux-amd64 \
  "$artifact_dir"/kb-linux-arm64 \
  "$artifact_dir"/kb-darwin-amd64 \
  "$artifact_dir"/kb-darwin-arm64 \
  "$artifact_dir"/kb-windows-amd64.exe \
  "$artifact_dir"/SHA256SUMS

printf 'release: %s published from %s\n' "$version" "$source_commit"
printf 'verify: gh release verify %s --repo %s\n' "$version" "$module_path"
