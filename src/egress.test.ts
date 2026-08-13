import { describe, expect, it } from 'vitest';

/**
 * The egress guard.
 *
 * kb is an on-device app with zero telemetry. The Content-Security-Policy the
 * server sends blocks the fetch directives at runtime; this test makes egress
 * hard to write in the first place, so nobody has to discover the problem by
 * reading a CSP violation in production.
 *
 * The two layers do not overlap completely, which is why this one has to be
 * wider than "the modules that call fetch":
 *
 * - `RTCPeerConnection` is not governed by `connect-src` in any browser (only
 *   Chromium implements `webrtc 'block'`), and a STUN/TURN URL is not http(s),
 *   so it would slip past a URL scan as well. It is in EGRESS_API below.
 * - `<link rel="preconnect">` / `dns-prefetch` are governed by *no* CSP
 *   directive anywhere. They leak a DNS lookup on every page load, so
 *   index.html and the stylesheet are scanned too, not just the modules.
 *
 * It fails when shipping source gains an absolute http(s) URL, an off-origin
 * resource reference or a network API that is not on the allowlists below.
 * The allowlists are meant to stay tiny: adding an entry is a deliberate act,
 * and each one carries the reason it is allowed.
 *
 * Test files are excluded — they never reach the bundle, and they need example
 * hosts to test against.
 */

/**
 * Absolute URLs that may appear in shipping source, as exact prefixes, with the
 * file they may appear in.
 */
const URL_ALLOWLIST: { file: string; url: string; why: string }[] = [
  {
    file: 'lib/auth.ts',
    url: 'https://login.microsoftonline.com/',
    why: 'the Entra sign-in authority — the one cross-origin host the SPA may reach, and only when Azure auth is configured',
  },
  {
    file: 'components/SettingsModal.tsx',
    url: 'https://api.openai.com/v1',
    why: 'placeholder text in the AI base URL field; the app never requests it',
  },
  {
    file: 'lib/inlineMd.ts',
    url: 'https://url',
    why: 'a link example in the module doc comment',
  },
];

/**
 * Files that may reach the network at all, and what they are allowed to talk
 * to. Everything here is same-origin; MSAL's own traffic is not in src/ and is
 * bounded by the CSP instead.
 */
const EGRESS_ALLOWLIST: { file: string; why: string }[] = [
  { file: 'lib/api.ts', why: 'the same-origin /api client' },
  { file: 'lib/auth.ts', why: 'GET /api/config, same-origin' },
];

/**
 * Ways a browser can originate a request. RTCPeerConnection is in here
 * because no CSP `connect-src` covers it: this guard is the only layer that
 * sees a data channel or a payload smuggled into a STUN hostname.
 */
const EGRESS_API =
  /\bfetch\s*\(|XMLHttpRequest|sendBeacon|new\s+WebSocket|new\s+EventSource|importScripts|RTCPeerConnection/;

const ABSOLUTE_URL = /https?:\/\/[^\s'"`)\\]*/g;

/**
 * An href/src/url() pointing off-origin, absolute or protocol-relative. Only
 * applied to the markup and stylesheet, where `//` cannot be a comment: this
 * is what catches `<link rel="preconnect" href="//host">` and an @import of a
 * remote font, neither of which any CSP directive can stop.
 */
const OFF_ORIGIN_REF =
  /(?:href|src)\s*=\s*['"]\s*(?:[a-z][a-z0-9+.-]*:)?\/\/|url\(\s*['"]?\s*(?:[a-z][a-z0-9+.-]*:)?\/\//i;

/**
 * Everything that ships: the modules, the stylesheet they import and the HTML
 * shell Vite builds around them, as raw text keyed by path relative to src/
 * (index.html keeps its `../` so it is obvious it is the repo-root file).
 * Test files are dropped. Read through the bundler so the guard needs no Node
 * types and no new dependency.
 *
 * The extension list is deliberately wider than what exists today — a .js or
 * .mjs dropped into src/ would otherwise be unscanned.
 */
const SOURCES: Record<string, string> = Object.fromEntries(
  Object.entries({
    ...(import.meta.glob('./**/*.{ts,tsx,js,jsx,mjs,cjs,css}', {
      query: '?raw',
      import: 'default',
      eager: true,
    }) as Record<string, string>),
    ...(import.meta.glob('../index.html', {
      query: '?raw',
      import: 'default',
      eager: true,
    }) as Record<string, string>),
  })
    .map(([path, text]) => [path.replace(/^\.\//, ''), text] as const)
    .filter(([file]) => !/\.test\.tsx?$/.test(file)),
);

const FILES = Object.keys(SOURCES).sort();

/** The shipping files that are not modules, where `//` is never a comment. */
const MARKUP = FILES.filter((f) => /\.(css|html)$/.test(f));

describe('egress guard', () => {
  it('scans the whole shipping source tree', () => {
    // A broken glob would make every assertion below vacuously pass.
    expect(FILES).toContain('App.tsx');
    expect(FILES).toContain('lib/api.ts');
    // Not only the modules: the stylesheet can @import a remote font and the
    // HTML shell can preconnect, and neither is something CSP stops.
    expect(FILES).toContain('styles.css');
    expect(FILES).toContain('../index.html');
    expect(FILES.length).toBeGreaterThan(15);
    // Vitest replaces CSS modules with an empty string unless test.css is on
    // (vite.config.ts): the file would still be listed here while scanning as
    // nothing. Check the text actually arrived.
    expect(SOURCES['styles.css']).toContain('@media');
    expect(SOURCES['../index.html']).toContain('<div id="root">');
    for (const file of FILES) expect(SOURCES[file].length).toBeGreaterThan(0);
  });

  it('names no off-origin resource in the markup or the stylesheet', () => {
    const found: string[] = [];
    for (const file of MARKUP) {
      SOURCES[file].split('\n').forEach((line: string, i: number) => {
        if (OFF_ORIGIN_REF.test(line)) found.push(`${file}:${i + 1}: ${line.trim()}`);
      });
    }
    expect(found).toEqual([]);
  });

  // The patterns have to match the things they exist for; a regex that
  // matches nothing guards nothing.
  it('recognises the channels CSP does not cover', () => {
    expect(EGRESS_API.test('new RTCPeerConnection({iceServers})')).toBe(true);
    expect(
      OFF_ORIGIN_REF.test('<link rel="dns-prefetch" href="https://x.example">'),
    ).toBe(true);
    expect(OFF_ORIGIN_REF.test('<link rel="preconnect" href="//x.example">')).toBe(
      true,
    );
    expect(
      OFF_ORIGIN_REF.test("@import url('https://fonts.googleapis.com/css2')"),
    ).toBe(true);
    expect(OFF_ORIGIN_REF.test('src: url(./inter.woff2)')).toBe(false);
    expect(OFF_ORIGIN_REF.test('<script type="module" src="/src/main.tsx">')).toBe(
      false,
    );
  });

  it('has no absolute URL outside the allowlist', () => {
    const found: string[] = [];
    for (const file of FILES) {
      for (const m of SOURCES[file].match(ABSOLUTE_URL) ?? []) {
        const ok = URL_ALLOWLIST.some(
          (a) => a.file === file && m.startsWith(a.url),
        );
        if (!ok) found.push(`${file}: ${m}`);
      }
    }
    expect(found).toEqual([]);
  });

  it('reaches the network only from allowlisted modules', () => {
    const found: string[] = [];
    for (const file of FILES) {
      if (EGRESS_ALLOWLIST.some((a) => a.file === file)) continue;
      SOURCES[file].split('\n').forEach((line: string, i: number) => {
        if (EGRESS_API.test(line)) {
          found.push(`${file}:${i + 1}: ${line.trim()}`);
        }
      });
    }
    expect(found).toEqual([]);
  });

  it('never passes an absolute URL to a network call', () => {
    const found: string[] = [];
    for (const file of FILES) {
      SOURCES[file].split('\n').forEach((line: string, i: number) => {
        if (/\bfetch\s*\(\s*['"`]https?:/.test(line)) {
          found.push(`${file}:${i + 1}: ${line.trim()}`);
        }
      });
    }
    expect(found).toEqual([]);
  });

  it('keeps the allowlists small and honest', () => {
    // Growth here is the signal to re-read "What kb sends over the network".
    expect(URL_ALLOWLIST.length).toBeLessThanOrEqual(3);
    expect(EGRESS_ALLOWLIST.length).toBeLessThanOrEqual(2);
    for (const a of [...URL_ALLOWLIST, ...EGRESS_ALLOWLIST]) {
      expect(FILES).toContain(a.file);
      expect(a.why.length).toBeGreaterThan(10);
    }
  });
});
