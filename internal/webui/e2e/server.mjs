// Run the actual embedded UI and API against a disposable board.
import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const dir = mkdtempSync(join(tmpdir(), 'kb-web-smoke-'));
const binary = join(dir, process.platform === 'win32' ? 'kb.exe' : 'kb');
process.on('exit', () => rmSync(dir, { recursive: true, force: true }));
const build = spawnSync('go', ['build', '-buildvcs=false', '-o', binary, '.'], {
  cwd: fileURLToPath(new URL('../../../', import.meta.url)),
  env: { ...process.env, GOTOOLCHAIN: 'go1.26.5' },
  stdio: 'inherit',
});
if (build.error) throw build.error;
if (build.status !== 0) process.exit(build.status || 1);
const server = spawn(binary, ['web', '--data', join(dir, 'data'), '--addr', '127.0.0.1:47173', '--no-open'], { stdio: 'inherit' });
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.kill(signal));
server.on('error', error => { throw error; });
server.on('exit', code => process.exit(code || 0));
