#!/usr/bin/env sh
set -eu

# Go 1.26.5 is an explicit hard cap. Only its standard-library findings are
# accepted; reachable dependency vulnerabilities and scanner failures still fail.
repo_root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
pinned_go="$(awk '$1 == "go" { print "go" $2; exit }' "$repo_root/go.mod")"
actual_go="$(go env GOVERSION)"
if [ "$pinned_go" != go1.26.5 ] || [ "$actual_go" != "$pinned_go" ]; then
  echo "vulnerability gate: requires pinned Go 1.26.5, got pin=$pinned_go runtime=$actual_go" >&2
  exit 1
fi
report="$(mktemp "${TMPDIR:-/tmp}/kb-vuln.XXXXXX")"
trap 'rm -f "$report"' EXIT HUP INT TERM
# JSON mode exits zero even when it finds vulnerabilities. A nonzero status is
# a scanner failure, not an accepted finding.
if go run -buildvcs=false golang.org/x/vuln/cmd/govulncheck@v1.8.0 -json ./... >"$report"; then
  :
else
  status=$?
  cat "$report"
  exit "$status"
fi
jq -e -s '
  ([.[] | .config? // empty] | length) == 1 and
  .[0].config.protocol_version == "v1.0.0" and
  .[0].config.go_version == "go1.26.5" and
  .[0].config.scan_level == "symbol" and .[0].config.scan_mode == "source" and
  all(.[] | .finding? // empty;
    (.osv | type == "string" and length > 0) and
    (.trace | type == "array" and length > 0) and
    (.trace[0].module | type == "string" and length > 0) and
    (.trace[0].function == null or (.trace[0].function | type == "string")))
' "$report" >/dev/null || {
  echo 'vulnerability gate: invalid or incompatible scanner output' >&2
  exit 1
}
# Preserve every finding and its trace, including uncalled vulnerabilities.
jq -c '.finding? // empty' "$report"
jq -r -s '
  [.[] | .finding? // empty | select((.trace[0].function // "") != "")]
  | unique_by([.osv, .trace[0].module, .trace[0].version])[]
  | if .trace[0].module == "stdlib" and .trace[0].version == "v1.26.5"
    then "ACCEPTED Go 1.26.5 standard-library risk: \(.osv) (fixed: \(.fixed_version // "unknown"))"
    else "REJECTED reachable vulnerability: \(.osv) in \(.trace[0].module)"
    end
' "$report"
jq -e -s '
  all(.[] | .finding? // empty | select((.trace[0].function // "") != "");
    .trace[0].module == "stdlib" and .trace[0].version == "v1.26.5")
' "$report" >/dev/null
echo 'vulnerability gate: no unaccepted reachable vulnerabilities'
