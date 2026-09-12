#!/usr/bin/env sh
set -eu

# Keep one scanner pin for Quality and both release paths. Text output preserves
# govulncheck's nonzero status for reachable vulnerabilities.
exec go run -buildvcs=false golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
