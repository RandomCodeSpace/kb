import { build } from 'esbuild';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('.', import.meta.url));
const result = await build({
  absWorkingDir: root,
  entryPoints: ['src/main.jsx'],
  outfile: 'static/app.js',
  bundle: true,
  format: 'esm',
  target: 'es2022',
  jsx: 'automatic',
  tsconfig: 'jsconfig.json',
  define: { 'process.env.NODE_ENV': '"production"' },
  minify: true,
  legalComments: 'eof',
  metafile: true,
  write: false,
  banner: { js: '/* Generated from src/main.jsx by npm run build; do not edit. License notices: app.LICENSE.txt. */' },
});

// Self-hosted fonts: the latin and latin-ext subsets of variable Inter and
// JetBrains Mono, served as top-level files beside app.css so the page makes
// no external requests. tailwind/app.css declares the matching @font-face rules.
const fonts = ['inter', 'jetbrains-mono'].flatMap(family => ['latin', 'latin-ext'].map(subset => ({
  from: new URL(`node_modules/@fontsource-variable/${family}/files/${family}-${subset}-wght-normal.woff2`, import.meta.url),
  path: fileURLToPath(new URL(`static/${family}-${subset}.woff2`, import.meta.url)),
})));

const packages = new Set(Object.keys(result.metafile.inputs)
  .map(path => path.match(/^node_modules\/((?:@[^/]+\/)?[^/]+)\//)?.[1]).filter(Boolean)
  .concat(['@fontsource-variable/inter', '@fontsource-variable/jetbrains-mono']));
const notices = ['shadcn/ui\n' + await readFile(new URL('src/components/ui/LICENSE', import.meta.url), 'utf8')];
for (const name of [...packages].sort()) {
  const directory = new URL(`node_modules/${name}/`, import.meta.url);
  const files = (await readdir(directory)).filter(file => /^licen[cs]e(?:\.|$)/i.test(file)).sort();
  if (files.length) {
    for (const file of files) notices.push(`${name}\n${await readFile(new URL(file, directory), 'utf8')}`);
    continue;
  }
  // A package that ships no license text still declares its license in package.json.
  const pkg = JSON.parse(await readFile(new URL('package.json', directory), 'utf8'));
  const author = typeof pkg.author === 'string' ? pkg.author : pkg.author?.name;
  if (typeof pkg.license !== 'string' || !author) throw new Error(`Missing license for bundled dependency ${name}`);
  notices.push(`${name} ${pkg.version}\n${pkg.license} License\nCopyright (c) ${author}`);
}
const license = notices.join('\n\n');
const outputs = [...result.outputFiles, {
  path: fileURLToPath(new URL('static/app.LICENSE.txt', import.meta.url)),
  text: license,
  contents: license,
}];

for (const font of fonts) {
  const contents = await readFile(font.from);
  outputs.push({ path: font.path, text: contents, contents });
}

for (const file of outputs) {
  if (process.argv.includes('--check')) {
    const committed = await readFile(file.path, typeof file.text === 'string' ? 'utf8' : undefined).catch(() => null);
    const same = typeof file.text === 'string' ? committed === file.text : committed !== null && Buffer.compare(committed, file.text) === 0;
    if (!same) throw new Error(`${file.path} is stale; run npm run build in internal/webui`);
  } else {
    await writeFile(file.path, file.contents);
  }
}
console.log(`web-js: ${process.argv.includes('--check') ? 'verified' : 'built'} app.js (${result.outputFiles[0].contents.length} bytes)`);
