#!/usr/bin/env node

const { readFileSync } = require('node:fs');

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

async function main() {
  const candidateSha = required('CANDIDATE_SHA');
  if (!/^[0-9a-f]{40}$/.test(candidateSha)) throw new Error('candidate SHA is invalid');
  const report = properties(`${required('PROJECT_BASE_DIR')}/.scannerwork/report-task.txt`);
  const serverUrl = new URL(report.get('serverUrl'));
  if (serverUrl.protocol !== 'https:' || serverUrl.hostname !== 'sonarcloud.io' || serverUrl.username || serverUrl.password) {
    throw new Error('unexpected Sonar server URL');
  }
  const ceTaskId = report.get('ceTaskId');
  if (!/^[A-Za-z0-9_-]+$/.test(ceTaskId || '')) throw new Error('invalid compute-engine task id');
  const task = await sonar(new URL(`/api/ce/task?id=${encodeURIComponent(ceTaskId)}`, serverUrl));
  if (task.task?.status !== 'SUCCESS' || !task.task.analysisId) {
    throw new Error(`compute-engine task is not successful: ${task.task?.status || 'missing'}`);
  }
  if (task.task.componentKey !== required('SONAR_PROJECT_KEY')) {
    throw new Error(`compute-engine component ${task.task.componentKey} != ${required('SONAR_PROJECT_KEY')}`);
  }

  const query = new URL('/api/project_analyses/search', serverUrl);
  query.searchParams.set('project', required('SONAR_PROJECT_KEY'));
  query.searchParams.set('pageSize', '100');
  if (required('ANALYSIS_MODE') === 'pull_request') query.searchParams.set('pullRequest', required('PULL_REQUEST'));
  if (required('ANALYSIS_MODE') === 'branch') query.searchParams.set('branch', required('CANDIDATE_REF'));
  const analyses = await sonar(query);
  const analysis = analyses.analyses?.find((item) => item.key === task.task.analysisId);
  if (!analysis) throw new Error(`analysis ${task.task.analysisId} not returned by project analysis API`);
  if (analysis.revision !== candidateSha) throw new Error(`analysis revision ${analysis.revision} != ${candidateSha}`);
  console.log(`verified Sonar task ${ceTaskId}, analysis ${analysis.key}, revision ${analysis.revision}`);
}

main().catch((error) => {
  console.error(`verify-sonar-task: ${error.message}`);
  process.exitCode = 1;
});
