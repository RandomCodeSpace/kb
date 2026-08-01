#!/usr/bin/env node

const { createHash } = require('node:crypto');
const { lstatSync, readFileSync, writeFileSync } = require('node:fs');
const { execFileSync } = require('node:child_process');
const { posix: pathPosix } = require('node:path');

const REPORTS = [
  ['frontend', 'coverage/lcov.info', 5 * 1024 * 1024],
  ['go', 'coverage/go.out', 5 * 1024 * 1024],
];

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`missing required environment variable ${name}`);
  return value;
}

function sha256(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function validateRelativeSource(path, label) {
  if (!path || path.includes('\\') || path.includes('\0') || path.includes('\r') || path.includes('\n') || pathPosix.isAbsolute(path)) {
    throw new Error(`${label} has an unsafe source path`);
  }
  const normalized = pathPosix.normalize(path);
  if (normalized !== path || normalized === '..' || normalized.startsWith('../')) {
    throw new Error(`${label} source path is not normalized repository-relative path: ${path}`);
  }
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) {
    throw new Error(`${label} source is not a regular non-symlink file: ${path}`);
  }
}

function validateReport(kind, path, limit) {
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) {
    throw new Error(`${path} must be a regular, non-symlink file`);
  }
  if (stat.size === 0 || stat.size > limit) {
    throw new Error(`${path} size ${stat.size} is outside 1..${limit} bytes`);
  }
  const sample = readFileSync(path, 'utf8');
  if (sample.includes('\0') || sample.includes('\r')) throw new Error(`${path} contains forbidden control bytes`);
  if (kind === 'frontend') {
    const sources = sample.split('\n').filter((line) => line.startsWith('SF:')).map((line) => line.slice(3));
    if (!sources.length || !sample.includes('\nDA:')) throw new Error(`${path} is not an LCOV report`);
    for (const source of sources) validateRelativeSource(source, path);
  }
  if (kind === 'go') {
    if (!/^mode: (set|count|atomic)\n/.test(sample)) throw new Error(`${path} is not a Go coverage profile`);
    const modulePath = execFileSync('go', ['list', '-m'], { encoding: 'utf8' }).trim();
    const lines = sample.trimEnd().split('\n').slice(1);
    if (!lines.length) throw new Error(`${path} has no coverage entries`);
    for (const line of lines) {
      const match = line.match(/^(.+):[0-9]+\.[0-9]+,[0-9]+\.[0-9]+ [0-9]+ [0-9]+$/);
      if (!match || !match[1].startsWith(`${modulePath}/`)) throw new Error(`${path} has a malformed coverage entry`);
      validateRelativeSource(match[1].slice(modulePath.length + 1), path);
    }
  }
  return { path, size: stat.size, sha256: sha256(path) };
}

function git(...args) {
  return execFileSync('git', args, { encoding: 'utf8' }).trim();
}

function maintenanceState(baseSha, candidateSha) {
  const output = execFileSync('bash', ['scripts/ci/guard-control-plane.sh', baseSha, candidateSha, '--classify'], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  if (output === 'maintenance=true') return true;
  if (output === 'maintenance=false') return false;
  throw new Error(`unexpected control-plane classification: ${output}`);
}

function securityBase(candidateSha, baseRef, pullRequest, eventBaseSha) {
  if (pullRequest > 0) return eventBaseSha;
  try {
    return validateSha(git('merge-base', candidateSha, `origin/${baseRef}`), 'security base');
  } catch {
    return '4b825dc642cb6eb9a060e54bf8d69288fbee4904';
  }
}

function integer(name, allowZero = false) {
  const value = required(name);
  if (!/^[0-9]+$/.test(value) || (!allowZero && value === '0')) {
    throw new Error(`${name} must be ${allowZero ? 'a non-negative' : 'a positive'} integer`);
  }
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error(`${name} must be a safe integer`);
  return parsed;
}

function validateSha(value, name) {
  if (!/^[0-9a-f]{40}$/.test(value)) throw new Error(`${name} must be a lowercase 40-character Git SHA`);
  return value;
}

function validateText(value, name) {
  if (!value || /[\r\n\0]/.test(value)) throw new Error(`${name} is invalid`);
  return value;
}

function main() {
  const candidateSha = validateSha(required('CANDIDATE_SHA'), 'CANDIDATE_SHA');
  const actualSha = git('rev-parse', 'HEAD');
  if (actualSha !== candidateSha) throw new Error(`checked-out SHA ${actualSha} != ${candidateSha}`);
  if (git('status', '--porcelain=v1', '--untracked-files=all')) {
    throw new Error('coverage generation changed tracked or non-ignored candidate files');
  }
  const pullRequest = integer('PULL_REQUEST_NUMBER', true);
  const workflowSha = validateSha(required('GITHUB_WORKFLOW_SHA'), 'GITHUB_WORKFLOW_SHA');
  const baseSha = validateSha(required('BASE_SHA'), 'BASE_SHA');
  const baseRef = validateText(required('BASE_REF'), 'BASE_REF');

  const manifest = {
    schema_version: 1,
    repository: validateText(required('GITHUB_REPOSITORY'), 'GITHUB_REPOSITORY'),
    producer: {
      workflow: validateText(required('GITHUB_WORKFLOW'), 'GITHUB_WORKFLOW'),
      workflow_ref: validateText(required('GITHUB_WORKFLOW_REF'), 'GITHUB_WORKFLOW_REF'),
      workflow_sha: workflowSha,
      test_revision_sha: workflowSha,
      run_id: integer('GITHUB_RUN_ID'),
      run_attempt: integer('GITHUB_RUN_ATTEMPT'),
      job: validateText(required('GITHUB_JOB'), 'GITHUB_JOB'),
      event: validateText(required('GITHUB_EVENT_NAME'), 'GITHUB_EVENT_NAME'),
    },
    candidate: {
      repository: validateText(required('CANDIDATE_REPOSITORY'), 'CANDIDATE_REPOSITORY'),
      sha: candidateSha,
      tree: validateSha(git('show', '-s', '--format=%T', 'HEAD'), 'candidate tree'),
      ref: validateText(required('CANDIDATE_REF'), 'CANDIDATE_REF'),
      pull_request: pullRequest,
      base_sha: baseSha,
      base_ref: baseRef,
      security_base_sha: securityBase(candidateSha, baseRef, pullRequest, baseSha),
      maintenance: pullRequest > 0 ? maintenanceState(baseSha, candidateSha) : false,
    },
    reports: Object.fromEntries(REPORTS.map(([kind, path, limit]) => [kind, validateReport(kind, path, limit)])),
    tools: {
      node: process.version,
      npm: execFileSync('npm', ['--version'], { encoding: 'utf8' }).trim(),
      go: execFileSync('go', ['version'], { encoding: 'utf8' }).trim(),
      go_module: execFileSync('go', ['list', '-m'], { encoding: 'utf8' }).trim(),
    },
  };

  writeFileSync('coverage/manifest.json', `${JSON.stringify(manifest, null, 2)}\n`, { flag: 'wx' });
}

try {
  main();
} catch (error) {
  console.error(`create-coverage-manifest: ${error.message}`);
  process.exitCode = 1;
}
