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

function environmentInteger(name) {
  const value = required(name);
  if (!/^[1-9][0-9]*$/.test(value)) throw new Error(`${name} must be a positive integer`);
  return safeInteger(Number(value), name);
}

function repositoryName(value, label) {
  if (typeof value !== 'string') throw new Error(`${label} is invalid`);
  const segments = value.split('/');
  if (segments.length !== 2 || segments.some((segment) => !/^[A-Za-z0-9_.-]+$/.test(segment))) {
    throw new Error(`${label} is invalid`);
  }
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
  const triggerRunId = environmentInteger('TRIGGER_RUN_ID');
  const triggerWorkflowId = environmentInteger('TRIGGER_WORKFLOW_ID');
  const triggerRunAttempt = environmentInteger('TRIGGER_RUN_ATTEMPT');
  const triggerHeadRepositoryId = environmentInteger('TRIGGER_HEAD_REPOSITORY_ID');
  const triggerHeadSha = required('TRIGGER_HEAD_SHA');
  if (!/^[0-9a-f]{40}$/.test(triggerHeadSha)) throw new Error('TRIGGER_HEAD_SHA must be a lowercase 40-character Git SHA');
  const trigger = event.workflow_run;
  if (!trigger || typeof trigger !== 'object') throw new Error('missing workflow_run payload');
  equal(event.repository?.full_name, repository, 'trigger repository');
  equal(trigger.repository?.full_name, repository, 'workflow repository');
  const candidateRepository = repositoryName(trigger.head_repository?.full_name, 'candidate repository');
  equal(trigger.name, WORKFLOW_NAME, 'producer workflow name');
  equal(trigger.path, WORKFLOW_PATH, 'producer workflow path');
  equal(trigger.event === 'pull_request' || trigger.event === 'push' || trigger.event === 'workflow_dispatch', true, 'producer event allowlist');
  equal(trigger.status, 'completed', 'producer status');
  equal(trigger.conclusion, 'success', 'producer conclusion');
  equal(safeInteger(trigger.id, 'workflow run id'), triggerRunId, 'trusted workflow run id');
  equal(safeInteger(trigger.workflow_id, 'workflow id'), triggerWorkflowId, 'trusted workflow id');
  equal(safeInteger(trigger.run_attempt, 'workflow run attempt'), triggerRunAttempt, 'trusted workflow run attempt');
  equal(safeInteger(trigger.head_repository.id, 'head repository id'), triggerHeadRepositoryId, 'trusted head repository id');
  equal(trigger.head_sha, triggerHeadSha, 'trusted head SHA');

  const encodedRepo = repository.split('/').map(encodeURIComponent).join('/');
  const run = await github(`/repos/${encodedRepo}/actions/runs/${triggerRunId}`);
  equal(run.id, triggerRunId, 'API run id');
  equal(run.workflow_id, triggerWorkflowId, 'API workflow id');
  equal(run.name, WORKFLOW_NAME, 'API workflow name');
  equal(run.path, WORKFLOW_PATH, 'API workflow path');
  equal(run.event, trigger.event, 'API event');
  equal(run.status, 'completed', 'API status');
  equal(run.conclusion, 'success', 'API conclusion');
  equal(run.head_repository?.full_name, candidateRepository, 'API head repository');
  equal(run.head_sha, triggerHeadSha, 'API head SHA');
  equal(run.run_attempt, triggerRunAttempt, 'API run attempt');
  const workflowFile = WORKFLOW_PATH.split('/').at(-1);
  const workflow = await github(`/repos/${encodedRepo}/actions/workflows/${encodeURIComponent(workflowFile)}`);
  equal(workflow.id, triggerWorkflowId, 'workflow-by-path id');
  equal(workflow.path, WORKFLOW_PATH, 'workflow-by-path path');
  equal(workflow.name, WORKFLOW_NAME, 'workflow-by-path name');

  const jobs = await github(`/repos/${encodedRepo}/actions/runs/${triggerRunId}/jobs?filter=latest&per_page=100`);
  const jobMatches = jobs.jobs.filter((job) => job.name === JOB_NAME && job.run_attempt === triggerRunAttempt);
  if (jobMatches.length !== 1) throw new Error(`expected exactly one ${JOB_NAME} job, found ${jobMatches.length}`);
  const job = jobMatches[0];
  safeInteger(job.id, 'producer job id');
  equal(job.run_id, triggerRunId, 'producer job run id');
  equal(job.status, 'completed', 'producer job status');
  equal(job.conclusion, 'success', 'producer job conclusion');
  equal(job.head_sha, triggerHeadSha, 'producer job head SHA');

  const artifacts = await github(`/repos/${encodedRepo}/actions/runs/${triggerRunId}/artifacts?per_page=100`);
  equal(artifacts.total_count, 1, 'artifact count');
  equal(artifacts.artifacts.length, 1, 'returned artifact count');
  const artifact = artifacts.artifacts[0];
  safeInteger(artifact.id, 'artifact id');
  equal(artifact.name, `candidate-head-coverage-${triggerHeadSha}`, 'artifact name');
  equal(artifact.expired, false, 'artifact expiration');
  if (!Number.isSafeInteger(artifact.size_in_bytes) || artifact.size_in_bytes < 1 || artifact.size_in_bytes > ARTIFACT_LIMIT) {
    throw new Error(`artifact size ${artifact.size_in_bytes} is outside 1..${ARTIFACT_LIMIT} bytes`);
  }
  equal(artifact.workflow_run?.id, triggerRunId, 'artifact workflow run id');
  equal(artifact.workflow_run?.head_sha, triggerHeadSha, 'artifact head SHA');
  equal(artifact.workflow_run?.head_repository_id, triggerHeadRepositoryId, 'artifact head repository id');

  const encodedCandidateRepo = candidateRepository.split('/').map(encodeURIComponent).join('/');
  const commit = await github(`/repos/${encodedCandidateRepo}/git/commits/${triggerHeadSha}`);
  equal(commit.sha, triggerHeadSha, 'candidate commit SHA');
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
    equal(pull.head?.sha, triggerHeadSha, 'pull request head SHA');
    pullRequest = pull.number;
    mode = 'pull_request';
    baseSha = pull.base?.sha;
    baseRef = pull.base?.ref;
    candidateRef = pull.head?.ref;
    const pullDetails = await github(`/repos/${encodedRepo}/pulls/${pull.number}`);
    equal(pullDetails.head?.sha, triggerHeadSha, 'pull request API head SHA');
    equal(pullDetails.head?.repo?.full_name, candidateRepository, 'pull request API head repository');
    equal(pullDetails.base?.repo?.full_name, repository, 'pull request API base repository');
    equal(pullDetails.head?.ref, candidateRef, 'pull request API head ref');
    equal(pullDetails.base?.ref, baseRef, 'pull request API base ref');
    workflowRef = `${repository}/${WORKFLOW_PATH}@refs/pull/${pull.number}/merge`;
    workflowSha = pullDetails.merge_commit_sha;
  } else {
    equal(candidateRepository, repository, 'non-pull-request candidate repository');
    const parents = commit.parents || [];
    baseSha = parents.length ? parents[0].sha : triggerHeadSha;
    baseRef = event.repository?.default_branch;
    mode = trigger.head_branch === event.repository?.default_branch ? 'main' : 'branch';
    workflowRef = `${repository}/${WORKFLOW_PATH}@refs/heads/${trigger.head_branch}`;
    workflowSha = triggerHeadSha;
  }
  if (!/^[0-9a-f]{40}$/.test(baseSha || '')) throw new Error('base SHA is invalid');
  for (const [label, value] of [['base ref', baseRef], ['candidate ref', candidateRef]]) {
    if (typeof value !== 'string' || !value || /[\r\n\0]/.test(value)) throw new Error(`${label} is invalid`);
  }
  if (!/^[0-9a-f]{40}$/.test(workflowSha || '')) throw new Error('workflow SHA is invalid');

  output({
    artifact_id: artifact.id,
    artifact_name: artifact.name,
    run_id: triggerRunId,
    run_attempt: triggerRunAttempt,
    job_id: job.id,
    event: trigger.event,
    mode,
    candidate_repository: candidateRepository,
    candidate_sha: triggerHeadSha,
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
