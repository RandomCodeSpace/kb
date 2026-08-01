#!/usr/bin/env node

const assert = require('node:assert/strict');
const { chmodSync, mkdtempSync, readFileSync, writeFileSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const { spawnSync } = require('node:child_process');

const root = resolve(__dirname, '../..');
const monitor = join(root, 'scripts/ci_monitor.cjs');
const temp = mkdtempSync(join(tmpdir(), 'ci-monitor-'));
const log = join(temp, 'args');
const fake = join(temp, 'gh');
writeFileSync(fake, '#!/bin/sh\nprintf "%s\\n" "$@" > "$CI_MONITOR_LOG"\n');
chmodSync(fake, 0o700);

function run(args, extraEnv = {}) {
  return spawnSync(process.execPath, [monitor, ...args], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, CI_MONITOR_GH: fake, CI_MONITOR_LOG: log, ...extraEnv },
  });
}

let result = run(['--help']);
assert.equal(result.status, 0);
assert.match(result.stdout, /runs \[--branch NAME\]/);

result = run(['runs', '--repo', 'RandomCodeSpace/kb', '--branch', 'main', '--limit', '5']);
assert.equal(result.status, 0, result.stderr);
assert.deepEqual(readFileSync(log, 'utf8').trim().split('\n'), ['run', 'list', '--limit', '5', '--branch', 'main', '-R', 'RandomCodeSpace/kb']);

result = run(['runs', '--repo', 'RandomCodeSpace/kb'], { CI_MONITOR_GH: 'gh' });
assert.equal(result.status, 2);
assert.match(result.stderr, /CI_MONITOR_GH must be an absolute path/);

result = run(['runs'], { CI_MONITOR_GIT: 'git', GITHUB_REPOSITORY: '' });
assert.equal(result.status, 2);
assert.match(result.stderr, /CI_MONITOR_GIT must be an absolute path/);

result = run(['check-actions']);
assert.equal(result.status, 0, result.stderr);
assert.match(result.stdout, /immutable SHAs/);

result = run(['watch', 'not-a-run', '--repo', 'RandomCodeSpace/kb']);
assert.equal(result.status, 2);

console.log('ci_monitor tests passed');
