#!/usr/bin/env node

const { appendFileSync, readFileSync } = require('node:fs');

const WORKFLOW_NAME = 'Regression and candidate coverage';
const WORKFLOW_PATH = '.github/workflows/quality.yml';
const JOB_NAME = 'Candidate head coverage';
const ARTIFACT_LIMIT = 10 * 1024 * 1024;

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`missing required environment variable ${name}`);
  return value;
}

function equal(actual, expected, label) {
  if (actual !== expected) throw new Error(`${label} mismatch: ${JSON.stringify(actual)} != ${JSON.stringify(expected)}`);
}

function safeInteger(value, label) {
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${label} must be a positive safe integer`);
  return value;
}

async function github(path) {
  const apiUrl = process.env.GITHUB_API_URL || 'https://api.github.com';
  const response = await fetch(`${apiUrl}${path}`, {
    headers: {
      Accept: 'application/vnd.github+json',
      Authorization: `Bearer ${required('GITHUB_TOKEN')}`,
      'X-GitHub-Api-Version': '2022-11-28',
    },
  });
  if (!response.ok) throw new Error(`GitHub API ${path} returned ${response.status}`);
  return response.json();
}

function output(values) {
  const path = required('GITHUB_OUTPUT');
  for (const [key, value] of Object.entries(values)) {
    if (String(value).includes('\n')) throw new Error(`unsafe newline in output ${key}`);
    appendFileSync(path, `${key}=${value}\n`);
  }
}

async function main() {
  const event = JSON.parse(readFileSync(required('GITHUB_EVENT_PATH'), 'utf8'));
  const repository = required('GITHUB_REPOSITORY');
  const trigger = event.workflow_run;
  if (!trigger || typeof trigger !== 'object') throw new Error('missing workflow_run payload');
  equal(event.repository?.full_name, repository, 'trigger repository');
  equal(trigger.repository?.full_name, repository, 'workflow repository');
  equal(trigger.head_repository?.full_name, repository, 'candidate repository');
  equal(trigger.name, WORKFLOW_NAME, 'producer workflow name');
  equal(trigger.path, WORKFLOW_PATH, 'producer workflow path');
  equal(trigger.event === 'pull_request' || trigger.event === 'push' || trigger.event === 'workflow_dispatch', true, 'producer event allowlist');
  equal(trigger.status, 'completed', 'producer status');
  equal(trigger.conclusion, 'success', 'producer conclusion');
  safeInteger(trigger.id, 'workflow run id');
  safeInteger(trigger.workflow_id, 'workflow id');
  safeInteger(trigger.run_attempt, 'workflow run attempt');
  safeInteger(trigger.head_repository.id, 'head repository id');

  const encodedRepo = repository.split('/').map(encodeURIComponent).join('/');
  const run = await github(`/repos/${encodedRepo}/actions/runs/${trigger.id}`);
  equal(run.id, trigger.id, 'API run id');
  equal(run.workflow_id, trigger.workflow_id, 'API workflow id');
  equal(run.name, WORKFLOW_NAME, 'API workflow name');
  equal(run.path, WORKFLOW_PATH, 'API workflow path');
  equal(run.event, trigger.event, 'API event');
  equal(run.status, 'completed', 'API status');
  equal(run.conclusion, 'success', 'API conclusion');
  equal(run.head_repository?.full_name, repository, 'API head repository');
  equal(run.head_sha, trigger.head_sha, 'API head SHA');
  equal(run.run_attempt, trigger.run_attempt, 'API run attempt');
  const workflowFile = WORKFLOW_PATH.split('/').at(-1);
  const workflow = await github(`/repos/${encodedRepo}/actions/workflows/${encodeURIComponent(workflowFile)}`);
  equal(workflow.id, trigger.workflow_id, 'workflow-by-path id');
  equal(workflow.path, WORKFLOW_PATH, 'workflow-by-path path');
  equal(workflow.name, WORKFLOW_NAME, 'workflow-by-path name');

  const jobs = await github(`/repos/${encodedRepo}/actions/runs/${trigger.id}/jobs?filter=latest&per_page=100`);
  const jobMatches = jobs.jobs.filter((job) => job.name === JOB_NAME && job.run_attempt === trigger.run_attempt);
  if (jobMatches.length !== 1) throw new Error(`expected exactly one ${JOB_NAME} job, found ${jobMatches.length}`);
  const job = jobMatches[0];
  safeInteger(job.id, 'producer job id');
  equal(job.run_id, trigger.id, 'producer job run id');
  equal(job.status, 'completed', 'producer job status');
  equal(job.conclusion, 'success', 'producer job conclusion');
  equal(job.head_sha, trigger.head_sha, 'producer job head SHA');

  const artifacts = await github(`/repos/${encodedRepo}/actions/runs/${trigger.id}/artifacts?per_page=100`);
  equal(artifacts.total_count, 1, 'artifact count');
  equal(artifacts.artifacts.length, 1, 'returned artifact count');
  const artifact = artifacts.artifacts[0];
  safeInteger(artifact.id, 'artifact id');
  equal(artifact.name, `candidate-head-coverage-${trigger.head_sha}`, 'artifact name');
  equal(artifact.expired, false, 'artifact expiration');
  if (!Number.isSafeInteger(artifact.size_in_bytes) || artifact.size_in_bytes < 1 || artifact.size_in_bytes > ARTIFACT_LIMIT) {
    throw new Error(`artifact size ${artifact.size_in_bytes} is outside 1..${ARTIFACT_LIMIT} bytes`);
  }
  equal(artifact.workflow_run?.id, trigger.id, 'artifact workflow run id');
  equal(artifact.workflow_run?.head_sha, trigger.head_sha, 'artifact head SHA');
  equal(artifact.workflow_run?.head_repository_id, trigger.head_repository.id, 'artifact head repository id');

  const commit = await github(`/repos/${encodedRepo}/git/commits/${trigger.head_sha}`);
  equal(commit.sha, trigger.head_sha, 'candidate commit SHA');
  const candidateTree = commit.tree?.sha;
  if (!/^[0-9a-f]{40}$/.test(candidateTree || '')) throw new Error('candidate tree is invalid');

  let mode;
  let pullRequest = 0;
  let baseSha;
  let baseRef;
  let candidateRef = trigger.head_branch;
  let workflowRef;
  let workflowSha;
  if (trigger.event === 'pull_request') {
    const pullRequests = run.pull_requests || [];
    if (pullRequests.length !== 1) throw new Error(`expected exactly one triggering pull request, found ${pullRequests.length}`);
    const pull = pullRequests[0];
    safeInteger(pull.number, 'pull request number');
    equal(pull.head?.sha, trigger.head_sha, 'pull request head SHA');
    pullRequest = pull.number;
    mode = 'pull_request';
    const pullDetails = await github(`/repos/${encodedRepo}/pulls/${pull.number}`);
    equal(pullDetails.head?.sha, trigger.head_sha, 'pull request API head SHA');
    equal(pullDetails.head?.repo?.full_name, repository, 'pull request API head repository');
    equal(pullDetails.base?.repo?.full_name, repository, 'pull request API base repository');
    baseSha = pullDetails.base?.sha;
    baseRef = pullDetails.base?.ref;
    candidateRef = pullDetails.head?.ref;
    workflowRef = `${repository}/${WORKFLOW_PATH}@refs/pull/${pull.number}/merge`;
    workflowSha = pullDetails.merge_commit_sha;
  } else {
    const parents = commit.parents || [];
    baseSha = parents.length ? parents[0].sha : trigger.head_sha;
    baseRef = event.repository?.default_branch;
    mode = trigger.head_branch === event.repository?.default_branch ? 'main' : 'branch';
    workflowRef = `${repository}/${WORKFLOW_PATH}@refs/heads/${trigger.head_branch}`;
    workflowSha = trigger.head_sha;
  }
  if (!/^[0-9a-f]{40}$/.test(baseSha || '')) throw new Error('base SHA is invalid');
  for (const [label, value] of [['base ref', baseRef], ['candidate ref', candidateRef]]) {
    if (typeof value !== 'string' || !value || /[\r\n\0]/.test(value)) throw new Error(`${label} is invalid`);
  }
  if (!/^[0-9a-f]{40}$/.test(workflowSha || '')) throw new Error('workflow SHA is invalid');

  output({
    artifact_id: artifact.id,
    artifact_name: artifact.name,
    run_id: trigger.id,
    run_attempt: trigger.run_attempt,
    job_id: job.id,
    event: trigger.event,
    mode,
    candidate_repository: repository,
    candidate_sha: trigger.head_sha,
    candidate_tree: candidateTree,
    candidate_ref: candidateRef,
    workflow_ref: workflowRef,
    workflow_sha: workflowSha,
    pull_request: pullRequest,
    base_sha: baseSha,
    base_ref: baseRef,
  });
}

main().catch((error) => {
  console.error(`validate-workflow-run: ${error.message}`);
  process.exitCode = 1;
});
