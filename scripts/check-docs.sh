#!/usr/bin/env sh
set -eu

repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
readme="$repo_root/README.md"
references="$(mktemp "${TMPDIR:-/tmp}/kb-doc-references.XXXXXX")"

cleanup() {
  rm -f -- "$references"
}
trap cleanup EXIT HUP INT TERM

fail() {
  echo "docs: $*" >&2
  exit 1
}

[ -f "$readme" ] || fail 'README.md is missing'

grep -Fi 'local-only' "$readme" >/dev/null || fail 'README must state that kb is local-only'
grep -F 'no web UI, JavaScript bundle, hosted account, or' "$readme" >/dev/null || \
  fail 'README must state that hosted and remote modes are absent'
grep -F 'never listens on a TCP port' "$readme" >/dev/null || \
  fail 'README must state that kb opens no inbound network listener'

badge_count="$(grep -c 'img.shields.io' "$readme" || true)"
[ "$badge_count" -gt 0 ] || fail 'README has no badges'
if grep 'img.shields.io' "$readme" | grep -Fv 'style=for-the-badge' >/dev/null; then
  fail 'every shields.io badge must use style=for-the-badge'
fi

grep -Eo '(]\([^[:space:])]+|(src|href)="[^"]+")' "$readme" >"$references" || true
while IFS= read -r reference; do
  case "$reference" in
    ']('* ) target="${reference#']('}" ;;
    src=\"* ) target="${reference#src=\"}"; target="${target%\"}" ;;
    href=\"* ) target="${reference#href=\"}"; target="${target%\"}" ;;
    * ) continue ;;
  esac
  target="${target%%#*}"
  case "$target" in
    ''|'#'*|http://*|https://*|mailto:* ) continue ;;
  esac
  [ -e "$repo_root/$target" ] || fail "missing local README target: $target"
  case "$target" in
    *.svg)
      grep -F '<svg' "$repo_root/$target" >/dev/null || fail "invalid SVG target: $target"
      ;;
  esac
done <"$references"

echo 'docs: pass'
