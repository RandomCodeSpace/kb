#!/usr/bin/env bash
set -eu

base_sha=${1:?usage: guard-control-plane.sh BASE_SHA HEAD_SHA}
head_sha=${2:?usage: guard-control-plane.sh BASE_SHA HEAD_SHA}
mode=${3:-reject}
[ "$mode" = reject ] || [ "$mode" = --classify ] || {
  echo "invalid mode: $mode" >&2
  exit 2
}
changed=$(mktemp)
trap 'rm -f "$changed"' EXIT HUP INT TERM

git diff --no-renames --name-only -z "$base_sha" "$head_sha" > "$changed"

blocked=0
unprotected=0
while IFS= read -r -d '' path; do
  case "$path" in
    .github/workflows/*|.github/actions/*|.github/sonar/*|.github/CODEOWNERS|.github/dependabot.yml|sonar-project.properties|scripts/check-*|scripts/ci/*|scripts/ci_monitor.cjs|package.json|package-lock.json|npm-shrinkwrap.json|yarn.lock|pnpm-lock.yaml|bun.lock|bun.lockb|.npmrc|vite.config.*|vitest.config.*|go.mod|go.sum)
      printf 'protected control-plane path changed: %s\n' "$path" >&2
      blocked=1
      ;;
    *)
      unprotected=1
      ;;
  esac
done < "$changed"

if [ "$blocked" -ne 0 ]; then
  if [ "$mode" = --classify ]; then
    if [ "$unprotected" -ne 0 ]; then
      echo 'candidate rejected: maintenance PR mixes protected control-plane and non-control-plane paths' >&2
      exit 1
    fi
    echo 'maintenance=true'
    exit 0
  fi
  echo 'candidate rejected: protected control-plane changes require a separate maintenance PR' >&2
  exit 1
fi

[ "$mode" != --classify ] || echo 'maintenance=false'
