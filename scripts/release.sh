#!/usr/bin/env bash
# Cut a kb release.
#
# This script validates and tags the exact clean source commit, builds the
# platform binaries at that tag, and publishes the GitHub release.
#
# Published tags are immutable: the Go module proxy and checksum database
# cache them on first fetch, and moving one breaks go install for everyone.
# A bad release is fixed by cutting the next patch version, never by retagging.
#
# Usage: scripts/release.sh vX.Y.Z [notes-file] [--dry-run]
# Requires: clean working tree, go, and an authenticated gh CLI.
# Run from a plain clone (or CI). In a linked git worktree the Go toolchain
# reads VCS state from the parent checkout and stamps the wrong version.
# --dry-run performs every build and check, creates the tag locally to prove
# the pipeline works, then deletes it and publishes nothing.
set -euo pipefail

dry_run=0
tag_created=0
args=()
for arg in "$@"; do
  if [[ $arg == --dry-run ]]; then dry_run=1; else args+=("$arg"); fi
done
version=${args[0]:?usage: scripts/release.sh vX.Y.Z [notes-file] [--dry-run]}
notes_file=${args[1]:-}

cleanup() {
  if [[ $dry_run == 1 && $tag_created == 1 ]]; then
    git tag -d "$version" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release: version must match vX.Y.Z" >&2
  exit 2
fi
cd "$(git rev-parse --show-toplevel)"
if [[ -n $(git status --porcelain --untracked-files=all) ]]; then
  echo "release: working tree not clean" >&2
  exit 2
fi
if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
  echo "release: tag $version already exists; cut the next version instead" >&2
  exit 2
fi
if [[ -n $notes_file && ! -f $notes_file ]]; then
  echo "release: notes file $notes_file not found" >&2
  exit 2
fi

go test ./...

# The annotated tag points directly at the source commit. Building while HEAD
# is tagged lets the Go toolchain stamp the release version into each binary.
git tag -a "$version" -m "$version"
tag_created=1

out=$(mktemp -d)
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  goos=${target%/*}
  goarch=${target#*/}
  bin="kb-$goos-$goarch"
  if [[ $goos == windows ]]; then
    bin+='.exe'
  fi
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
    go build -trimpath -ldflags='-s -w' -o "$out/$bin" .
done

if [[ $dry_run == 1 ]]; then
  git tag -d "$version" >/dev/null
  tag_created=0
  echo "release: dry run complete: $version built and validated, nothing published"
  ls -l "$out"
  exit 0
fi

git push origin "$version"

release_args=("$version" --title "$version")
if [[ -n $notes_file ]]; then
  release_args+=(--notes-file "$notes_file")
else
  release_args+=(--generate-notes)
fi
gh release create "${release_args[@]}" "$out"/kb-*

echo "release: $version published"
echo "verify: GOBIN=\$(mktemp -d) go install github.com/RandomCodeSpace/kb@$version"
