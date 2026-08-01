#!/bin/sh
set -eu

test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
git archive HEAD | tar -x -C "$test_dir"

node - "$test_dir/package.json" <<'NODE'
const { readFileSync, writeFileSync } = require('node:fs');
const path = process.argv[2];
const value = JSON.parse(readFileSync(path, 'utf8'));
value.scripts.postinstall = 'node -e "require(\'node:fs\').writeFileSync(\'postinstall-ran\', \'bad\')"';
writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
NODE

(
  cd "$test_dir"
  npm ci --ignore-scripts
  test ! -e postinstall-ran
  npm test
  npm run build
)
