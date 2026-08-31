#!/usr/bin/env sh
set -eu

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
target_repo="$(pwd -P)"

cd "$repo_root"
exec go run -buildvcs=false ./scripts/ci/impactcmd --repo "$target_repo" "$@"
