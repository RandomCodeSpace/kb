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
