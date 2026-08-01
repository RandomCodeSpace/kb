#!/usr/bin/env bash
set -euo pipefail

repository=${1:?usage: validate-candidate-tree.sh REPOSITORY EXPECTED_SHA EXPECTED_TREE}
expected_sha=${2:?usage: validate-candidate-tree.sh REPOSITORY EXPECTED_SHA EXPECTED_TREE}
expected_tree=${3:?usage: validate-candidate-tree.sh REPOSITORY EXPECTED_SHA EXPECTED_TREE}

actual_sha=$(git -C "$repository" rev-parse HEAD)
actual_tree=$(git -C "$repository" show -s --format=%T HEAD)
[ "$actual_sha" = "$expected_sha" ] || { echo "candidate SHA mismatch: $actual_sha != $expected_sha" >&2; exit 1; }
[ "$actual_tree" = "$expected_tree" ] || { echo "candidate tree mismatch: $actual_tree != $expected_tree" >&2; exit 1; }

while IFS= read -r -d '' entry; do
  metadata=${entry%%	*}
  path=${entry#*	}
  mode=${metadata%% *}
  case "$mode" in
    120000) echo "candidate tracked symlink rejected: $path" >&2; exit 1 ;;
    160000) echo "candidate gitlink/submodule rejected: $path" >&2; exit 1 ;;
    100644|100755) ;;
    *) echo "candidate unexpected Git index mode $mode: $path" >&2; exit 1 ;;
  esac
done < <(git -C "$repository" ls-files -s -z)

while IFS= read -r -d '' path; do
  [ ! -L "$repository/$path" ] || { echo "candidate filesystem symlink rejected: $path" >&2; exit 1; }
done < <(git -C "$repository" ls-files -z)

status=$(git -C "$repository" status --porcelain=v1 --untracked-files=all)
[ -z "$status" ] || { printf 'candidate checkout is not clean:\n%s\n' "$status" >&2; exit 1; }
