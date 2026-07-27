# webtui — Rich-Card Kanban Web App — Design Spec

Date: 2026-07-27
Status: Approved (concept validated through 6 interactive prototypes with user;
final shape locked on the rich-card status board — see session artifact history).

## Concept

Personal task tracker. Classic status columns (To Do / Doing / Done) as the
backbone — the user explicitly chose status labels as "the only correct way" —
with richness invested in the cards and the interactions:

- Rich cards: emoji, wrapping title, description snippet, inline expandable
  checklist (tick subtasks directly on the board), chunky progress bar,
  priority dot, relative due chip ("in 5d" / "overdue · 6d"), effort chip
  (S/M/L), plain tags, and GitLab-style scoped labels (`key::value` two-tone
  pills).
- Juice that earns its place: confetti on ship, mini-confetti on subtask tick,
  ticking the last subtask auto-ships the card, daily streak counter, drag with
  lift shadow + column highlight.
- Visual direction: "Playground" (user-selected from three pitched directions):
  paper ground #f3f6f2, ink #20242c, fat 2px ink borders, hard offset shadows,
  pastel column panels (peach/sky/mint), rounded system font stack.

## Architecture decision: client-side first, backend-ready

- v1 (this build): local-first SPA. Vite + React + TypeScript. All data in
  `localStorage`. Markdown import/export keeps boards portable plain text.
- v1.1 (later): tiny self-hosted sync service on the user's VPS (GET/PUT board
  as markdown files, token auth). The storage layer is an interface
  (`BoardStore`) from day one so the remote adapter drops in without UI change.
- Single user; last-write-wins; no auth in v1.

## Data model

```ts
type Status = 'todo' | 'doing' | 'done';
type Effort = 'S' | 'M' | 'L';
interface Check { text: string; done: boolean }
interface Task {
  id: string; emoji: string; title: string; desc: string;
  status: Status; prio: 1|2|3|4;
  due?: string;          // YYYY-MM-DD
  effort?: Effort;
  tags: string[];        // plain ("backend") or scoped ("type::bug")
  checks: Check[];
  createdAt: string; movedAt: string; // ISO timestamps
}
interface Board { title: string; tasks: Task[] }
```

## Markdown format (import/export)

```markdown
# Board Title

## To Do

- [ ] 🚚 Migrate CI runner !1 @2026-07-21 ~L #infra #env::prod
  Old runner image is EOL this month.
  - [ ] provision new runner
  - [ ] move secrets

## Doing

## Done

- [x] 🧱 Init repo
```

Tokens on the title line, parsed and stripped: `!1..!4` priority, `@YYYY-MM-DD`
due, `~S|~M|~L` effort, `#tag` (scoped labels are just tags containing `::`).
Leading pictographic character = emoji. Indented plain lines = description;
indented checkboxes = checklist. Round-trip must be lossless for well-formed
files. Timestamps are not serialized (reset on import — acceptable v1 loss).

## Behaviors

- Drag card between columns (pointer events, no dnd library): lift + tilt +
  deep shadow, target column highlights, drop moves card (updates `movedAt`).
- Checklist: expand via chevron; tick = mini confetti at the checkbox; when
  progress hits 100% and card is not Done, auto-move to Done after a beat.
- Move to Done (any path): confetti burst + daily streak increment
  (streak = shipped count for the current local date, persisted).
- Add/edit card via modal (emoji, title, desc, priority, due, effort, tags,
  checklist as one-item-per-line editor). Delete with confirm.
- Relative chips computed live: due ("today", "in Nd", "overdue · Nd") and age
  ("new", "Nd old" in To Do from createdAt; "Nh here"/"Nd here" in Doing from
  movedAt; "shipped" in Done).
- Import/export markdown via file download / file picker.
- Mobile: columns snap-scroll horizontally; desktop: three columns side by side.

## Testing

Vitest on the pure core: markdown round-trip + token parsing, urgency/age math
with fixed dates. UI verified by build + manual pass.

## Non-goals (v1)

Multi-board, sync backend, auth, offline service worker, keyboard shortcuts,
column customization. All deferred deliberately.

## Addendum (same day): identity + single-binary server

Pulls the v1.1 sync service forward and pins its shape ("kb"):

- **Single Go binary** (`go install github.com/RandomCodeSpace/kb@latest`)
  embedding the built SPA. `dist/` is committed so `go install` — which builds
  from module source — has the frontend to embed. Run:
  `kb --port 8080 --data ~/boards`.
- **API contract**: `GET /api/health` → 200 `{"ok":true}`;
  `GET /api/board` → 200 `text/markdown` or 404 if none saved;
  `PUT /api/board` (body `text/markdown`) → 204. Auth applies to `/api/board`
  only, never to `/api/health` or static files.
- **Auth modes** (chosen from env at startup, in precedence order):
  1. `KB_AZURE_TENANT_ID` + `KB_AZURE_CLIENT_ID` → require
     `Authorization: Bearer <Entra ID token>` (the SPA's ID token). Server-side
     validation only: RS256 signature vs tenant JWKS
     (`https://login.microsoftonline.com/<tenant>/discovery/v2.0/keys`),
     `iss == https://login.microsoftonline.com/<tenant>/v2.0`,
     `aud == client id`, `exp`/`nbf`. Identity = `email` claim, falling back
     to `preferred_username`. **No Microsoft Graph calls anywhere** — token
     decode/verify only.
  2. else `KB_TOKEN` → require `Authorization: Bearer <KB_TOKEN>`
     (constant-time compare, crypto/subtle). Identity = required non-empty
     `X-KB-User` header (a manual unique id).
  3. else open/localhost mode → identity = `X-KB-User` or `default`.
- **Frontend build-time pair**: `VITE_AZURE_TENANT_ID` /
  `VITE_AZURE_CLIENT_ID` baked in at `npm run build` to enable the MSAL SPA
  flow (PKCE; no client secret; only openid/profile/email).
- **Storage**: one board file per identity at `<dataDir>/<sanitize(user)>.md`.
  `sanitize` lowercases and replaces every char outside `[a-z0-9._@-]` with
  `-`, rejects empty names and names starting with `.`, and never allows path
  separators (no traversal).
- **Sync**: last-write-wins on the whole file; no merge, versioning, or
  conflict detection.
