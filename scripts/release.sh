#!/usr/bin/env bash
# Cut a kb release.
#
# go install builds from the module zip the proxy captures at the tag, so the
# tag must contain the built SPA. This script builds dist/, commits it on a
# detached release commit (the branch itself stays free of build artifacts),
# tags that commit, builds the platform binaries from the identical content,
# and publishes the GitHub release with the binaries attached.
#
# Published tags are immutable: the Go module proxy and checksum database
# cache them on first fetch, and moving one breaks go install for everyone.
# A bad release is fixed by cutting the next patch version, never by retagging.
#
# Usage: scripts/release.sh vX.Y.Z [notes-file]
# Requires: clean working tree, npm, go, and an authenticated gh CLI.
set -euo pipefail

version=${1:?usage: scripts/release.sh vX.Y.Z [notes-file]}
notes_file=${2:-}

if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release: version must match vX.Y.Z" >&2
  exit 2
fi
cd "$(git rev-parse --show-toplevel)"
if ! git diff --quiet || ! git diff --cached --quiet; then
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

base=$(git rev-parse HEAD)

npm ci
npm run build
if [[ ! -f dist/index.html ]]; then
  echo "release: dist/index.html missing after the frontend build" >&2
  exit 1
fi
go test ./...

# The release commit lives only under the tag; the working branch is restored
# immediately after. dist/ stays on disk (reset never deletes untracked
# files), so the binaries below are built from exactly the tagged content.
git add -f dist
git commit --quiet -m "release: $version"
git tag -a "$version" -m "$version"
git reset --quiet --hard "$base"

git push origin "$version"

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

args=("$version" --title "$version")
if [[ -n $notes_file ]]; then
  args+=(--notes-file "$notes_file")
else
  args+=(--generate-notes)
fi
gh release create "${args[@]}" "$out"/kb-*

echo "release: $version published"
echo "verify: GOBIN=\$(mktemp -d) go install github.com/RandomCodeSpace/kb@$version"
