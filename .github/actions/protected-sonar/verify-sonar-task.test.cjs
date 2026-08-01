#!/usr/bin/env node

const assert = require('node:assert/strict');
const { mkdirSync, mkdtempSync, writeFileSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join } = require('node:path');

const verifier = require('./verify-sonar-task.cjs');

const candidateSha = '0123456789abcdef0123456789abcdef01234567';
const projectKey = 'RandomCodeSpace_kb';
const project = mkdtempSync(join(tmpdir(), 'verify-sonar-task-'));
mkdirSync(join(project, '.scannerwork'));
writeFileSync(
  join(project, '.scannerwork/report-task.txt'),
  'serverUrl=https://sonarcloud.io\nceTaskId=task-1\n',
);

Object.assign(process.env, {
  ANALYSIS_MODE: 'pull_request',
  CANDIDATE_REF: 'feature',
  CANDIDATE_SHA: candidateSha,
  PROJECT_BASE_DIR: project,
  PULL_REQUEST: '18',
  SONAR_PROJECT_KEY: projectKey,
  SONAR_TOKEN: 'test-token',
});

function response(body) {
  return { ok: true, json: async () => body };
}

function ceTask(overrides = {}) {
  return {
    task: {
      analysisId: 'analysis-1',
      componentKey: projectKey,
      pullRequest: '18',
      scannerContext: [
        'Scanner properties:',
        `  - sonar.projectKey=${projectKey}`,
        `  - sonar.scm.revision=${candidateSha}`,
        '  - sonar.pullrequest.key=18',
        '',
      ].join('\n'),
      status: 'SUCCESS',
      ...overrides,
    },
  };
}

async function runTask(task) {
  const seen = [];
  global.fetch = async (url) => {
    seen.push(new URL(url));
    return response(task);
  };
  await verifier.main();
  assert.equal(seen.length, 1);
  assert.equal(seen[0].pathname, '/api/ce/task');
  assert.equal(seen[0].searchParams.get('id'), 'task-1');
  assert.equal(seen[0].searchParams.get('additionalFields'), 'scannerContext');
}

async function rejects(task, pattern) {
  global.fetch = async () => response(task);
  await assert.rejects(verifier.main(), pattern);
}

async function main() {
  const originalFetch = global.fetch;
  const originalLog = console.log;
  try {
    const messages = [];
    console.log = (message) => messages.push(message);

    await runTask(ceTask());
    assert.equal(messages.length, 1);
    assert.doesNotMatch(messages[0], /sonar\.projectKey|scannerContext/);

    await rejects(ceTask({ pullRequest: '19' }), /compute-engine pull request 19 != 18/);
    await rejects(
      ceTask({ scannerContext: 'sonar.scm.revision=ffffffffffffffffffffffffffffffffffffffff\n' }),
      /scanner revision .* !=/,
    );
    await rejects(
      ceTask({ scannerContext: `sonar.scm.revision=${candidateSha}\n  - sonar.scm.revision=${candidateSha}\n` }),
      /contains 2 revision properties/,
    );
    await rejects(
      ceTask({ scannerContext: `sonar.projectKey=${projectKey}\n` }),
      /contains 0 revision properties/,
    );
    await rejects(
      ceTask({ scannerContext: `sonar.scm.revision=${candidateSha}%0Asonar.scm.revision=${candidateSha}\n` }),
      /scanner revision is malformed/,
    );
    await rejects(
      ceTask({ scannerContext: `sonar.scm.revision=${candidateSha}\r\n` }),
      /scanner context is missing or malformed/,
    );
    for (const prefix of ['- ', ' - ', '\t-\t', '  -- ', '  -', '  + ', '  * ', '  - - ', '  ']) {
      await rejects(
        ceTask({ scannerContext: `${prefix}sonar.scm.revision=${candidateSha}\n` }),
        /contains 0 revision properties/,
      );
    }
    await rejects(
      ceTask({ scannerContext: `  - attacker.sonar.scm.revision=${candidateSha}\n` }),
      /contains 0 revision properties/,
    );
    await rejects(ceTask({ status: 'FAILED' }), /compute-engine task is not successful: FAILED/);

    process.env.ANALYSIS_MODE = 'branch';
    process.env.CANDIDATE_REF = 'fix/sonar-security-batch-1';
    const seen = [];
    global.fetch = async (url) => {
      const parsed = new URL(url);
      seen.push(parsed);
      return response(ceTask({ branch: 'fix/sonar-security-batch-1', pullRequest: undefined }));
    };
    await verifier.main();
    assert.equal(seen.length, 1);
    assert.equal(seen[0].searchParams.get('additionalFields'), 'scannerContext');
    await rejects(
      ceTask({ branch: 'fix/other-branch', pullRequest: undefined }),
      /compute-engine branch fix\/other-branch != fix\/sonar-security-batch-1/,
    );
    await rejects(
      ceTask({ branch: undefined, pullRequest: undefined }),
      /compute-engine branch missing != fix\/sonar-security-batch-1/,
    );

    originalLog('verify-sonar-task hostile tests passed');
  } finally {
    global.fetch = originalFetch;
    console.log = originalLog;
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
