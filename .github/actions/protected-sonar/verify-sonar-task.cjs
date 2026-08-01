#!/usr/bin/env node

const { readFileSync } = require('node:fs');

const SONAR_ORIGIN = 'https://sonarcloud.io';

function required(name) {
  const value = process.env[name];
  if (!value) throw new Error(`missing required environment variable ${name}`);
  return value;
}

function properties(path) {
  const result = new Map();
  for (const line of readFileSync(path, 'utf8').split('\n')) {
    if (!line || line.startsWith('#')) continue;
    const split = line.indexOf('=');
    if (split < 1) throw new Error(`malformed report-task line: ${line}`);
    result.set(line.slice(0, split), line.slice(split + 1));
  }
  return result;
}

async function sonar(url) {
  const token = required('SONAR_TOKEN');
  const response = await fetch(url, {
    headers: { Authorization: `Basic ${Buffer.from(`${token}:`).toString('base64')}` },
  });
  if (!response.ok) throw new Error(`Sonar API returned ${response.status} for ${new URL(url).pathname}`);
  return response.json();
}

function scannerRevision(scannerContext) {
  if (typeof scannerContext !== 'string' || /[\0\r]/.test(scannerContext)) {
    throw new Error('compute-engine scanner context is missing or malformed');
  }
  const property = /^(?:  - )?sonar\.scm\.revision=(.*)$/;
  const revisions = scannerContext
    .split('\n')
    .map((line) => line.match(property))
    .filter(Boolean)
    .map((match) => match[1]);
  if (revisions.length !== 1) {
    throw new Error(`compute-engine scanner context contains ${revisions.length} revision properties`);
  }
  if (!/^[0-9a-f]{40}$/.test(revisions[0])) {
    throw new Error('compute-engine scanner revision is malformed');
  }
  return revisions[0];
}

async function main() {
  const candidateSha = required('CANDIDATE_SHA');
  if (!/^[0-9a-f]{40}$/.test(candidateSha)) throw new Error('candidate SHA is invalid');
  const report = properties(`${required('PROJECT_BASE_DIR')}/.scannerwork/report-task.txt`);
  const serverUrl = new URL(report.get('serverUrl'));
  if (serverUrl.origin !== SONAR_ORIGIN || serverUrl.username || serverUrl.password) {
    throw new Error('unexpected Sonar server URL');
  }
  const ceTaskId = report.get('ceTaskId');
  if (!/^[A-Za-z0-9_-]+$/.test(ceTaskId || '')) throw new Error('invalid compute-engine task id');
  const analysisMode = required('ANALYSIS_MODE');
  if (!['pull_request', 'branch', 'main'].includes(analysisMode)) {
    throw new Error(`unsupported analysis mode ${analysisMode}`);
  }
  const taskQuery = new URL('/api/ce/task', SONAR_ORIGIN);
  taskQuery.searchParams.set('id', ceTaskId);
  taskQuery.searchParams.set('additionalFields', 'scannerContext');
  const task = await sonar(taskQuery);
  if (task.task?.status !== 'SUCCESS' || !task.task.analysisId) {
    throw new Error(`compute-engine task is not successful: ${task.task?.status || 'missing'}`);
  }
  if (task.task.componentKey !== required('SONAR_PROJECT_KEY')) {
    throw new Error(`compute-engine component ${task.task.componentKey} != ${required('SONAR_PROJECT_KEY')}`);
  }

  if (analysisMode === 'pull_request') {
    const pullRequest = required('PULL_REQUEST');
    if (!/^[1-9][0-9]*$/.test(pullRequest)) throw new Error('pull request number is invalid');
    if (String(task.task.pullRequest) !== pullRequest) {
      throw new Error(`compute-engine pull request ${task.task.pullRequest || 'missing'} != ${pullRequest}`);
    }
  } else {
    if (task.task.pullRequest !== undefined && task.task.pullRequest !== null) {
      throw new Error(`unexpected compute-engine pull request ${task.task.pullRequest}`);
    }
    const candidateRef = required('CANDIDATE_REF');
    if (analysisMode === 'branch' && task.task.branch !== candidateRef) {
      throw new Error(`compute-engine branch ${task.task.branch || 'missing'} != ${candidateRef}`);
    }
    if (analysisMode === 'main' && task.task.branch !== undefined && task.task.branch !== null && task.task.branch !== candidateRef) {
      throw new Error(`compute-engine branch ${task.task.branch} != ${candidateRef}`);
    }
  }

  const revision = scannerRevision(task.task.scannerContext);
  if (revision !== candidateSha) throw new Error(`compute-engine scanner revision ${revision} != ${candidateSha}`);
  console.log(`verified Sonar task ${ceTaskId}, analysis ${task.task.analysisId}, revision ${revision}`);
}

if (require.main === module) {
  main().catch((error) => {
    console.error(`verify-sonar-task: ${error.message}`);
    process.exitCode = 1;
  });
}

module.exports = { main, scannerRevision };
