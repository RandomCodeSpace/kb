# Web UI

The board is a React application built from `src/` with shadcn/ui components
and Tailwind v4. The Go binary embeds the generated assets in `static/`; users
do not need Node.js.

From this directory, using Node.js 22.13 or newer:

```sh
npm ci --ignore-scripts
npm run lint
npm run build
npm run build:check
```

## Layout

- `src/main.jsx` mounts `src/app.jsx` into `#app` in `static/index.html`.
- `src/store.js` holds the board state and settings. Views read it through
  `useStore()`; logic modules call `notify()` after changing it.
- `src/lib/` holds the application logic: API access and live updates
  (`api.js`), filters and search (`filters.js`), board navigation, drag and
  drop, selection and bulk actions (`board.js`), the detail view (`detail.js`),
  the editor (`edit.js`), settings (`settings.js`), the ADR split and forge
  import wizards (`split.js`, `import.js`), the command palette and keyboard
  map (`palette.js`, `keys.js`), markdown rendering (`markdown.js`).
- `src/components/ui/` holds the shadcn/ui components (see `static/VENDOR.md`
  for provenance). `src/components/` holds the views: header, filters, board,
  bulk bar, detail, editor, search, palette, display options, help, settings,
  wizards, toasts.
- `components.json` and `jsconfig.json` configure shadcn/ui imports (`@/`).

## Build

`npm run build` bundles `src/main.jsx` into `static/app.js` with esbuild,
copies the self-hosted Inter and JetBrains Mono font files into `static/`,
writes dependency license notices to `static/app.LICENSE.txt`, then runs the
pinned Tailwind compiler (`scripts/build-web-css.sh`) over `tailwind/app.css`,
`static/index.html` and `src/` to produce `static/app.css`. `npm run
build:check` verifies that the committed outputs match the sources. Commit
generated outputs with their sources. All browser assets are served locally by
the Go static handler, which serves top-level files only.

## Lint

`eslint.config.mjs` enables `@shadcn/lint` for `src/`: component restyling is
limited to layout classes, colors come from the theme, classes must be static
and known to Tailwind, and inline styles and arbitrary values are rejected.
Component definitions under `src/components/ui/` may own structural values,
variants and the one dynamic geometry a Progress indicator needs. Views set
hues and tones through data attributes (`data-hue`, `data-tone`, `data-h`)
that `tailwind/app.css` maps to custom properties, and geometry that must be
measured (drop slots, ghosts, the board grid) is written through refs.
[Rule documentation](https://github.com/shadcn-ui/lint/blob/main/docs/rules.md).

## Tests

The web CI job runs lint, checks generated assets, then runs the browser
tests in `e2e/` against the real binary on a disposable database:

```sh
npm ci --prefix e2e
npm test --prefix e2e
```

`board.spec.js` covers creation, moves and live updates, `display.spec.js`
the display options, and `features.spec.js` drag and drop, keyboard moves,
selection, search and filters, the palette, the composer, task details,
cancelling with a reason and settings. The first run may require
`npx playwright install chromium` from `e2e/`.
