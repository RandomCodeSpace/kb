#!/bin/sh
set -eu

event_name=${GITHUB_EVENT_NAME:?GITHUB_EVENT_NAME is required}
event_path=${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH is required}
head_sha=${GITHUB_SHA:?GITHUB_SHA is required}
default_branch=${GITHUB_DEFAULT_BRANCH:?GITHUB_DEFAULT_BRANCH is required}
empty_tree=4b825dc642cb6eb9a060e54bf8d69288fbee4904

validate_sha() {
  case "$1" in
    *[!0-9a-f]*|'') echo "invalid $2 SHA: $1" >&2; exit 1 ;;
  esac
  [ "${#1}" -eq 40 ] || { echo "invalid $2 SHA length" >&2; exit 1; }
}

case "$event_name" in
  pull_request)
    base_sha=$(jq -er '.pull_request.base.sha' "$event_path")
    head_sha=$(jq -er '.pull_request.head.sha' "$event_path")
    ;;
  push)
    before=$(jq -er '.before' "$event_path")
    validate_sha "$before" before
    case "$before" in
      0000000000000000000000000000000000000000)
        git fetch --no-tags origin "$default_branch"
        base_sha=$(git merge-base "$head_sha" "origin/$default_branch" 2>/dev/null || true)
        [ -n "$base_sha" ] || base_sha=$empty_tree
        [ "$(git rev-list --parents -n 1 "$head_sha" | wc -w)" -ne 1 ] || base_sha=$empty_tree
        ;;
      *)
        git fetch --no-tags origin "$before"
        base_sha=$before
        ;;
    esac
    ;;
  workflow_dispatch)
    git fetch --no-tags origin "$default_branch"
    base_sha=$(git merge-base "$head_sha" "origin/$default_branch" 2>/dev/null || true)
    [ -n "$base_sha" ] || base_sha=$empty_tree
    ;;
  *)
    echo "unsupported event: $event_name" >&2
    exit 1
    ;;
esac

validate_sha "$head_sha" head
validate_sha "$base_sha" base
[ "$base_sha" = "$empty_tree" ] || git cat-file -e "$base_sha^{commit}"
git cat-file -e "$head_sha^{commit}"

status=0
git diff --check "$base_sha" "$head_sha" || status=$?

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo '### Event-aware diff evidence'
    echo
    echo "- Event: \`$event_name\`"
    echo "- Base: \`$base_sha\`"
    echo "- Head: \`$head_sha\`"
    echo "- Command: \`git diff --check $base_sha $head_sha\`"
    echo "- Exit: \`$status\`"
  } >> "$GITHUB_STEP_SUMMARY"
fi

printf 'event=%s\nbase=%s\nhead=%s\nexit=%s\n' "$event_name" "$base_sha" "$head_sha" "$status"
[ -z "${GITHUB_OUTPUT:-}" ] || {
  printf 'event=%s\nbase=%s\nhead=%s\n' "$event_name" "$base_sha" "$head_sha" >> "$GITHUB_OUTPUT"
}
exit "$status"
