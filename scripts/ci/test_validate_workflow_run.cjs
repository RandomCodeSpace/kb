#!/usr/bin/env node

const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const { createServer } = require('node:http');
const fs = require('node:fs');
const { mkdirSync, mkdtempSync, readFileSync, writeFileSync } = fs;
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { spawn } = require('node:child_process');

const repository = 'RandomCodeSpace/kb';
const head = '1'.repeat(40);
const tree = '2'.repeat(40);
const base = '3'.repeat(40);
const merge = '4'.repeat(40);
const runId = 10;
const jobId = 20;
const artifactId = 30;
const temp = mkdtempSync(join(tmpdir(), 'workflow-run-'));
const eventPath = join(temp, 'event.json');
const outputPath = join(temp, 'output');
const requests = [];

const event = {
  repository: { full_name: repository, default_branch: 'main' },
  workflow_run: {
    id: runId, workflow_id: 5, run_attempt: 1, name: 'Regression and candidate coverage',
    path: '.github/workflows/quality.yml', event: 'pull_request', status: 'completed', conclusion: 'success',
    head_sha: head, head_branch: 'feature', repository: { full_name: repository },
    head_repository: { id: 99, full_name: repository },
  },
};
writeFileSync(eventPath, JSON.stringify(event));

const fixtures = new Map([
  ['/repos/RandomCodeSpace/kb/actions/workflows/quality.yml', {
    id: 5, path: '.github/workflows/quality.yml', name: 'Regression and candidate coverage', state: 'active',
  }],
  [`/repos/RandomCodeSpace/kb/actions/runs/${runId}`, {
    id: runId, workflow_id: 5, run_attempt: 1, name: event.workflow_run.name, path: event.workflow_run.path,
    event: 'pull_request', status: 'completed', conclusion: 'success', head_sha: head,
    head_repository: { full_name: repository },
    pull_requests: [{ number: 7, head: { sha: head, ref: 'feature', repo: { full_name: repository } }, base: { sha: base, ref: 'main' } }],
  }],
  [`/repos/RandomCodeSpace/kb/actions/runs/${runId}/jobs?filter=latest&per_page=100`, {
    jobs: [{ id: jobId, run_id: runId, run_attempt: 1, name: 'Candidate head coverage', status: 'completed', conclusion: 'success', head_sha: head }],
  }],
  [`/repos/RandomCodeSpace/kb/actions/runs/${runId}/artifacts?per_page=100`, {
    total_count: 1,
    artifacts: [{ id: artifactId, name: `candidate-head-coverage-${head}`, expired: false, size_in_bytes: 1000,
      workflow_run: { id: runId, head_sha: head, head_repository_id: 99 } }],
  }],
  [`/repos/RandomCodeSpace/kb/git/commits/${head}`, { sha: head, tree: { sha: tree }, parents: [{ sha: base }] }],
  ['/repos/RandomCodeSpace/kb/pulls/7', {
    head: { sha: head, ref: 'feature', repo: { full_name: repository } },
    base: { sha: base, ref: 'main', repo: { full_name: repository } },
    merge_commit_sha: merge,
  }],
]);

const server = createServer((request, response) => {
  requests.push(request.url);
  const fixture = fixtures.get(request.url);
  response.writeHead(fixture ? 200 : 404, { 'content-type': 'application/json' });
  response.end(JSON.stringify(fixture || { message: 'not found' }));
});

server.listen(0, '127.0.0.1', () => {
  const child = spawn(process.execPath, [join(__dirname, 'validate-workflow-run.cjs')], {
    env: {
      ...process.env,
      GITHUB_API_URL: `http://127.0.0.1:${server.address().port}`,
      GITHUB_EVENT_PATH: eventPath,
      GITHUB_OUTPUT: outputPath,
      GITHUB_REPOSITORY: repository,
      GITHUB_TOKEN: 'test-token',
      TRIGGER_RUN_ID: String(runId),
      TRIGGER_WORKFLOW_ID: '5',
      TRIGGER_RUN_ATTEMPT: '1',
      TRIGGER_HEAD_REPOSITORY_ID: '99',
      TRIGGER_HEAD_SHA: head,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  child.on('close', (code) => {
    assert.equal(code, 0, stderr);
    const output = readFileSync(outputPath, 'utf8');
    assert.match(output, new RegExp(`candidate_sha=${head}`));
    assert.match(output, /workflow_ref=RandomCodeSpace\/kb\/\.github\/workflows\/quality\.yml@refs\/pull\/7\/merge/);
    assert.match(output, new RegExp(`workflow_sha=${merge}`));
    assert.match(output, /pull_request=7/);
    const requestCount = requests.length;
    event.workflow_run.id = runId + 1;
    writeFileSync(eventPath, JSON.stringify(event));
    const hostile = spawn(process.execPath, [join(__dirname, 'validate-workflow-run.cjs')], {
      env: {
        ...process.env,
        GITHUB_API_URL: `http://127.0.0.1:${server.address().port}`,
        GITHUB_EVENT_PATH: eventPath,
        GITHUB_OUTPUT: outputPath,
        GITHUB_REPOSITORY: repository,
        GITHUB_TOKEN: 'test-token',
        TRIGGER_RUN_ID: String(runId),
        TRIGGER_WORKFLOW_ID: '5',
        TRIGGER_RUN_ATTEMPT: '1',
        TRIGGER_HEAD_REPOSITORY_ID: '99',
        TRIGGER_HEAD_SHA: head,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let hostileStderr = '';
    hostile.stderr.on('data', (chunk) => { hostileStderr += chunk; });
    hostile.on('close', async (hostileCode) => {
      assert.notEqual(hostileCode, 0);
      assert.match(hostileStderr, /trusted workflow run id mismatch/);
      assert.equal(requests.length, requestCount, 'mismatched event id must not reach the network');
      await testManifestDescriptorReads();
      await testSonarOriginPinning();
      server.close(() => console.log('CI JavaScript hostile fixtures passed'));
    });
  });
});

async function testManifestDescriptorReads() {
  const fixture = join(temp, 'manifest-fixture');
  mkdirSync(join(fixture, 'coverage'), { recursive: true });
  mkdirSync(join(fixture, 'src'), { recursive: true });
  const report = 'TN:\nSF:src/a.ts\nDA:1,1\nend_of_record\n';
  writeFileSync(join(fixture, 'coverage/lcov.info'), report);
  writeFileSync(join(fixture, 'src/a.ts'), 'export const a = 1;\n');
  const originalCwd = process.cwd();
  const originalReadFileSync = fs.readFileSync;
  try {
    fs.readFileSync = (target, ...args) => {
      if (target === 'coverage/lcov.info') throw new Error('report path reopened after validation');
      return originalReadFileSync(target, ...args);
    };
    delete require.cache[require.resolve('./create-coverage-manifest.cjs')];
    const { validateReport } = require('./create-coverage-manifest.cjs');
    process.chdir(fixture);
    const result = validateReport('frontend', 'coverage/lcov.info', 1024);
    assert.equal(result.size, Buffer.byteLength(report));
    assert.equal(result.sha256, createHash('sha256').update(report).digest('hex'));
  } finally {
    process.chdir(originalCwd);
    fs.readFileSync = originalReadFileSync;
    delete require.cache[require.resolve('./create-coverage-manifest.cjs')];
  }

  const originalFstatSync = fs.fstatSync;
  let fstatCalls = 0;
  try {
    fs.fstatSync = (descriptor) => {
      const stat = originalFstatSync(descriptor);
      fstatCalls += 1;
      if (fstatCalls === 2) return { ...stat, size: stat.size + 1, isFile: () => stat.isFile() };
      return stat;
    };
    delete require.cache[require.resolve('./create-coverage-manifest.cjs')];
    const { validateReport } = require('./create-coverage-manifest.cjs');
    process.chdir(fixture);
    assert.throws(() => validateReport('frontend', 'coverage/lcov.info', 1024), /changed size while being read/);
  } finally {
    process.chdir(originalCwd);
    fs.fstatSync = originalFstatSync;
    delete require.cache[require.resolve('./create-coverage-manifest.cjs')];
  }
}

async function testSonarOriginPinning() {
  const project = join(temp, 'sonar-project');
  mkdirSync(join(project, '.scannerwork'), { recursive: true });
  const reportPath = join(project, '.scannerwork/report-task.txt');
  process.env.PROJECT_BASE_DIR = project;
  process.env.CANDIDATE_SHA = head;
  process.env.SONAR_TOKEN = 'test-token';
  process.env.SONAR_PROJECT_KEY = 'RandomCodeSpace_kb';
  process.env.ANALYSIS_MODE = 'branch';
  process.env.CANDIDATE_REF = 'feature';
  const seen = [];
  global.fetch = async (url) => {
    const parsed = new URL(url);
    seen.push(parsed.href);
    const body = parsed.pathname === '/api/ce/task'
      ? { task: { status: 'SUCCESS', analysisId: 'analysis-1', componentKey: process.env.SONAR_PROJECT_KEY } }
      : { analyses: [{ key: 'analysis-1', revision: head }] };
    return { ok: true, json: async () => body };
  };
  const { main: verifySonarTask } = require('../../.github/actions/protected-sonar/verify-sonar-task.cjs');
  writeFileSync(reportPath, 'serverUrl=https://sonarcloud.io/untrusted/path\nceTaskId=task-1\n');
  await verifySonarTask();
  assert.equal(seen.length, 2);
  assert.ok(seen.every((url) => new URL(url).origin === 'https://sonarcloud.io'));

  const requestCount = seen.length;
  writeFileSync(reportPath, 'serverUrl=https://sonarcloud.io.example.invalid\nceTaskId=task-1\n');
  await assert.rejects(verifySonarTask(), /unexpected Sonar server URL/);
  assert.equal(seen.length, requestCount, 'untrusted report origin must not reach the network');
}
