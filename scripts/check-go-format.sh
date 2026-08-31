#!/usr/bin/env sh
set -eu

list_file=''
output_file=''
status_file=''

cleanup() {
  [ -z "$list_file" ] || rm -f -- "$list_file"
  [ -z "$output_file" ] || rm -f -- "$output_file"
  [ -z "$status_file" ] || rm -f -- "$status_file"
}
trap cleanup EXIT HUP INT TERM

list_file="$(mktemp "${TMPDIR:-/tmp}/kb-go-format-list.XXXXXX")"
output_file="$(mktemp "${TMPDIR:-/tmp}/kb-go-format-output.XXXXXX")"
status_file="$(mktemp "${TMPDIR:-/tmp}/kb-go-format-status.XXXXXX")"

case "${1:-}" in
  --full)
    shift
    [ "$#" -eq 0 ] || {
      echo 'format: --full does not accept revisions' >&2
      exit 2
    }
    if git ls-files -z -- '*.go' >"$list_file"; then
      :
    else
      status=$?
      echo "format: could not enumerate tracked Go files" >&2
      exit "$status"
    fi
    ;;
  --changed)
    shift
    [ "$#" -eq 2 ] || {
      echo 'format: --changed requires BASE and HEAD revisions' >&2
      exit 2
    }
    base="$1"
    target="$2"
    if git diff --name-only -z --diff-filter=ACMR "$base" "$target" -- '*.go' >"$list_file"; then
      :
    else
      status=$?
      echo "format: could not enumerate changed Go files" >&2
      exit "$status"
    fi
    ;;
  *)
    echo 'format: usage: check-go-format.sh --full | --changed BASE HEAD' >&2
    exit 2
    ;;
esac

# The quoted body is intentionally evaluated by the child shell, not this one.
# shellcheck disable=SC2016
if xargs -0 -r sh -c '
  status_file=$1
  shift
  if gofmt -l -- "$@"; then
    exit 0
  else
    status=$?
  fi
  printf "%s\n" "$status" >"$status_file"
  exit 255
' sh "$status_file" <"$list_file" >"$output_file"; then
  :
else
  xargs_status=$?
  if [ -s "$status_file" ]; then
    status="$(cat "$status_file")"
    case "$status" in
      *[!0-9]*|'')
        echo "format: invalid recorded gofmt status" >&2
        exit "$xargs_status"
        ;;
      *)
        ;;
    esac
    echo "format: gofmt failed" >&2
    exit "$status"
  fi
  echo "format: xargs failed" >&2
  exit "$xargs_status"
fi

if [ -s "$output_file" ]; then
  echo "format: these tracked Go files are not gofmt-clean:" >&2
  cat "$output_file" >&2
  exit 1
fi
