#!/usr/bin/env node

// Repository-local replacement for the github-workflows skill's absent monitor.
// It deliberately wraps every GitHub CLI observation with an explicit repository.

const { execFileSync, spawnSync } = require('node:child_process');
const { readFileSync, readdirSync } = require('node:fs');
const { join } = require('node:path');

const HELP = `usage: node scripts/ci_monitor.cjs <command> [arguments]

commands:
  runs [--branch NAME] [--limit N]   list recent workflow runs
  watch RUN_ID                       watch a run and return its conclusion
  fail-fast RUN_ID                   watch a run with failure exit status
  log-failed RUN_ID                  print failed job logs
  test-summary RUN_ID                print job names and conclusions
  check-actions [FILE]               reject non-SHA action references
  grep RUN_ID --pattern REGEX        search complete run logs
  wait-for RUN_ID JOB --keyword TEXT wait until a job log contains text

global:
  --repo OWNER/REPO                  override repository detection
`;

function die(message) {
  console.error(`ci_monitor: ${message}`);
  process.exit(2);
}

function extractOption(args, name, fallback) {
  const index = args.indexOf(name);
  if (index < 0) return fallback;
  if (!args[index + 1] || args[index + 1].startsWith('--')) die(`${name} requires a value`);
  const value = args[index + 1];
  args.splice(index, 2);
  return value;
}

function repository(args) {
  const explicit = extractOption(args, '--repo', process.env.GITHUB_REPOSITORY);
  if (explicit) {
    if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(explicit)) die('invalid --repo value');
    return explicit;
  }
  let remote;
  try {
    remote = execFileSync('git', ['remote', 'get-url', 'origin'], { encoding: 'utf8' }).trim();
  } catch {
    die('cannot detect repository; pass --repo OWNER/REPO');
  }
  const match = remote.match(/(?:github\.com[:/])([^/]+)\/([^/]+?)(?:\.git)?$/);
  if (!match) die('origin is not a GitHub remote; pass --repo OWNER/REPO');
  return `${match[1]}/${match[2]}`;
}

function gh(repo, args, capture = false) {
  const executable = process.env.CI_MONITOR_GH || 'gh';
  const result = spawnSync(executable, [...args, '-R', repo], {
    encoding: 'utf8',
    stdio: capture ? ['ignore', 'pipe', 'pipe'] : 'inherit',
  });
  if (result.error) die(result.error.message);
  if (capture && result.stderr) process.stderr.write(result.stderr);
  if (result.status !== 0) process.exit(result.status ?? 1);
  return result.stdout || '';
}

function positiveInteger(value, label) {
  if (!/^[1-9][0-9]*$/.test(value || '') || !Number.isSafeInteger(Number(value))) die(`${label} must be a positive safe integer`);
  return value;
}

function checkActions(files) {
  function actionFiles(directory) {
    return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) return actionFiles(path);
      return /(?:action\.ya?ml)$/.test(entry.name) ? [path] : [];
    });
  }
  const selected = files.length ? files : [
    '.github/workflows/quality.yml',
    '.github/workflows/sonar-exact-revision.yml',
    ...actionFiles('.github/actions'),
  ];
  let failures = 0;
  for (const file of selected) {
    const text = readFileSync(file, 'utf8');
    for (const [lineIndex, line] of text.split('\n').entries()) {
      const match = line.match(/^\s*uses:\s*([^\s#]+)\s*/);
      if (!match || match[1].startsWith('./')) continue;
      const at = match[1].lastIndexOf('@');
      const ref = at >= 0 ? match[1].slice(at + 1) : '';
      if (!/^[0-9a-f]{40}$/.test(ref)) {
        console.error(`${file}:${lineIndex + 1}: action is not pinned to a full commit SHA: ${match[1]}`);
        failures += 1;
      }
    }
  }
  if (failures) process.exit(1);
  console.log(`checked ${selected.length} workflow file(s): all external actions use immutable SHAs`);
}

function main() {
  const args = process.argv.slice(2);
  if (!args.length || args.includes('--help') || args[0] === 'help') {
    process.stdout.write(HELP);
    return;
  }
  const repo = repository(args);
  const command = args.shift();
  switch (command) {
    case 'runs': {
      const branch = extractOption(args, '--branch');
      const limit = positiveInteger(extractOption(args, '--limit', '20'), '--limit');
      if (args.length) die(`unexpected arguments: ${args.join(' ')}`);
      gh(repo, ['run', 'list', '--limit', limit, ...(branch ? ['--branch', branch] : [])]);
      break;
    }
    case 'watch':
    case 'fail-fast': {
      const runId = positiveInteger(args.shift(), 'run id');
      if (args.length) die(`unexpected arguments: ${args.join(' ')}`);
      gh(repo, ['run', 'watch', runId, '--exit-status']);
      break;
    }
    case 'log-failed': {
      const runId = positiveInteger(args.shift(), 'run id');
      if (args.length) die(`unexpected arguments: ${args.join(' ')}`);
      gh(repo, ['run', 'view', runId, '--log-failed']);
      break;
    }
    case 'test-summary': {
      const runId = positiveInteger(args.shift(), 'run id');
      if (args.length) die(`unexpected arguments: ${args.join(' ')}`);
      gh(repo, ['run', 'view', runId, '--json', 'jobs', '--jq', '.jobs[] | [.name, .conclusion] | @tsv']);
      break;
    }
    case 'check-actions':
      checkActions(args);
      break;
    case 'grep': {
      const runId = positiveInteger(args.shift(), 'run id');
      const pattern = extractOption(args, '--pattern');
      if (!pattern || args.length) die('grep requires RUN_ID --pattern REGEX');
      let regex;
      try { regex = new RegExp(pattern); } catch (error) { die(`invalid regex: ${error.message}`); }
      const lines = gh(repo, ['run', 'view', runId, '--log'], true).split('\n').filter((line) => regex.test(line));
      process.stdout.write(lines.length ? `${lines.join('\n')}\n` : '');
      process.exitCode = lines.length ? 0 : 1;
      break;
    }
    case 'wait-for': {
      const runId = positiveInteger(args.shift(), 'run id');
      const job = args.shift();
      const keyword = extractOption(args, '--keyword');
      if (!job || !keyword || args.length) die('wait-for requires RUN_ID JOB --keyword TEXT');
      const jobs = JSON.parse(gh(repo, ['run', 'view', runId, '--json', 'jobs'], true)).jobs;
      const matches = jobs.filter((item) => item.name === job);
      if (matches.length !== 1) die(`expected one job named ${job}, found ${matches.length}`);
      const log = gh(repo, ['run', 'view', runId, '--job', String(matches[0].databaseId), '--log'], true);
      if (!log.includes(keyword)) process.exit(1);
      console.log(`found ${JSON.stringify(keyword)} in ${job}`);
      break;
    }
    default:
      die(`unknown command ${command}`);
  }
}

main();
