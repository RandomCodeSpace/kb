#!/usr/bin/env sh
set -eu

repository=${1:?usage: fetch-verified-base.sh OWNER/REPOSITORY SHA}
sha=${2:?usage: fetch-verified-base.sh OWNER/REPOSITORY SHA}

owner=${repository%%/*}
name=${repository#*/}
if [ "$owner" = "$repository" ] || [ -z "$owner" ] || [ -z "$name" ] || [ "$name" != "${name#*/}" ]; then
  echo 'invalid base repository' >&2
  exit 2
fi
case "$owner$name" in
  *[!A-Za-z0-9_.-]*)
    echo 'invalid base repository' >&2
    exit 2
    ;;
esac
[ "${#sha}" -eq 40 ] || {
  echo 'invalid base SHA' >&2
  exit 2
}
case "$sha" in
  *[!0-9a-f]*)
    echo 'invalid base SHA' >&2
    exit 2
    ;;
esac

base_url=${FETCH_BASE_URL:-https://github.com/$repository.git}
git fetch --no-tags "$base_url" "$sha"
test "$(git rev-parse FETCH_HEAD)" = "$sha"
git cat-file -e "$sha^{commit}"
