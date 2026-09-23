# Vendored browser libraries

Copied unchanged from the upstream release builds; served as top-level static
files by `kb web`. Do not edit them in place. To upgrade, replace the file and
update this table.

| File            | Library   | Version | License                     | Source                                                                    |
| --------------- | --------- | ------- | --------------------------- | ------------------------------------------------------------------------- |
| `marked.min.js` | marked    | 15.0.12 | MIT                         | https://github.com/markedjs/marked/releases/tag/v15.0.12 (`marked.min.js`) |
| `purify.min.js` | DOMPurify | 3.2.6   | Apache-2.0 OR MPL-2.0       | https://github.com/cure53/DOMPurify/releases/tag/3.2.6 (`dist/purify.min.js`) |

`app.css` is not vendored: it is compiled from `../tailwind/app.css` by
`scripts/build-web-css.sh` (Tailwind CSS v4.1.13, MIT).

The fonts `inter-latin.woff2`, `inter-latin-ext.woff2`,
`jetbrains-mono-latin.woff2` and `jetbrains-mono-latin-ext.woff2` are the
variable-weight latin and latin-ext subsets of Inter and JetBrains Mono,
copied unchanged by `npm run build` from `@fontsource-variable/inter` and
`@fontsource-variable/jetbrains-mono` 5.3.0 (SIL Open Font License 1.1, pinned
in `../package-lock.json`). Their license text is in `app.LICENSE.txt`.

`app.js` is generated from `../src/main.jsx` by `npm run build` in
`internal/webui` (esbuild). Its dependencies (React, Radix UI primitives,
class-variance-authority, clsx, tailwind-merge, lucide-react) are pinned in
`../package-lock.json`; their license notices are in `app.LICENSE.txt`.

The components in `../src/components/ui/` are adapted from the
[shadcn/ui new-york-v4 registry](https://ui.shadcn.com/r/styles/new-york-v4/button.json)
(Button, Checkbox, Input, Textarea, Switch, Select, Popover, Dropdown Menu,
Toggle Group, Progress, Skeleton, Spinner, Kbd, Badge as `chip.jsx`, Field,
Dialog, Calendar as `date-picker.jsx`). These MIT-licensed sources were
retrieved on 2026-09-21 and 2026-09-22. Adaptations use JavaScript, kb's
existing semantic colors, type sizes and component classes, larger touch
targets, and the native `<dialog>` element for modals. The license is retained
in `../src/components/ui/LICENSE` and the generated bundle notices.
