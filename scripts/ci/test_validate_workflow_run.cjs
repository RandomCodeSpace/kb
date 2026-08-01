#!/usr/bin/env node

const assert = require('node:assert/strict');
const { createServer } = require('node:http');
const { mkdtempSync, readFileSync, writeFileSync } = require('node:fs');
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
    server.close(() => console.log('workflow-run API fixture passed'));
  });
});
