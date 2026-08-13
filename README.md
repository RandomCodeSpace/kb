# kb

A personal kanban board that stays plain text.

**kb** is a rich-card kanban web app (PWA-style single-page app) served by a
single-binary Go server. The frontend is Vite + React + TypeScript; the server
embeds the built SPA, stores boards in a single SQLite database, and speaks a
tiny markdown wire API — your whole board still round-trips as one
human-readable `.md` document. The same binary is also a task CLI and an MCP
server for coding agents.

## Features

- **Status columns** — To Do / Doing / Done / Cancelled (soft delete: cards can
  be restored).
- **Rich cards** — emoji, wrapping title, description snippet, inline
  expandable checklist (tick subtasks directly on the board), chunky progress
  bar, priority dot, relative due chip ("in 5d" / "overdue · 6d"), effort chip
  (S/M/L), blocked flag, plain tags, and GitLab-style scoped labels
  (`key::value` two-tone pills).
- **Juice that earns its place** — confetti on ship, mini-confetti on subtask
  tick, ticking the last subtask auto-ships the card, daily streak counter,
  drag with lift shadow and column highlight (pointer events, no dnd library).
- **Plain-text native** — markdown is the wire and export format.
  Import/export from the UI; round-trip is lossless for well-formed files.
- **Single binary server** — the Go server embeds the SPA and stores all
  boards in one SQLite file (`modernc.org/sqlite`, pure Go, WAL mode, no
  CGO). Existing per-user `.md` boards are imported automatically on first
  start.
- **CLI** — `kb add/list/update/move/done/cancel/restore/rm` against the local
  database, or against a remote kb server with `KB_SERVER` (see [CLI](#cli)).
- **MCP server** — `kb mcp` exposes five task tools and two context tools over
  stdio for agent harnesses (see [MCP server](#mcp-server)).
- **Forge integrations** — import AI-transformed issues from GitLab or GitHub,
  public or enterprise (see [Integrations](#integrations)).
- **AI assist** — draft a card from a prompt via any OpenAI-compatible
  endpoint you configure; the server proxies, your API key never reaches the
  browser (see [AI assist and settings](#ai-assist-and-settings)).
- **Label registry** — tags are remembered per user and suggested in the
  story modal (see [Labels](#labels)).
- **Multi-user by identity, not by accounts** — each identity maps to its own
  board. Identity comes from Azure Entra ID or a manual unique id (see
  [Identity modes](#identity-modes)).

## Quick start

### Development (frontend only)

```bash
npm i && npm run dev
```

### Production (single binary)

```bash
go install github.com/RandomCodeSpace/kb@latest
kb --port 8080 --data ~/boards
```

Release tags from v0.7.3 onward include the built web UI, so `go install`
produces the complete binary. Earlier tags (v0.7.2 and below) contain no UI
when installed this way — the API, CLI, and MCP server work, but the web
board answers 404; use the prebuilt binaries from the GitHub release page
for those versions instead. Releases are cut with `scripts/release.sh`,
which commits the built UI on the release tag itself.

A bare `kb` (or `kb` with only flags) serves the embedded SPA and the API on
the given port. Data lives in a SQLite database under the data directory
(default `~/.local/share/kb`, or `KB_DATA` / `--data`).

```bash
kb add "Ship the release" --prio 1   # CLI against the same database
kb list
kb mcp                               # MCP server on stdio for agents
```

> To build kb from source, run `npm run build` to generate the embedded SPA
> into `dist/`, then `CGO_ENABLED=0 go build .` for the binary. Release
> archives include the pre-built `dist/` for reproducible builds.

## Storage

Boards live in a single SQLite database at `<data>/kb.db`
(`modernc.org/sqlite` — pure Go, CGO off, WAL mode). Per-user scoping is on
every row: `tasks` (UUID id, emoji, title, desc, status, prio, due, effort,
position, tags, checklist, timestamps), `labels` (label registry with
last-used stamps), and `settings` (AI configuration).

Markdown remains the **legacy wire and export format**, not the storage format.
First-party browser sync can wrap the same markdown in a JSON identity envelope:

- `GET/PUT /api/board` still accept `text/markdown`; UI import/export is
  unchanged. A markdown `PUT` without `If-Match` retains the legacy
  unconditional replacement behavior.
- `Accept: application/json` on `GET` returns the markdown with canonical task
  ids. `Content-Type: application/json` on `PUT` sends those ids back so
  renames, moves, and duplicate titles retain identity.
- A Go markdown codec (same grammar as the frontend's `src/lib/markdown.ts`,
  shared golden test vectors) converts at the boundary.

**Automatic one-time import**: on first start, any existing `<data>/<user>.md`
board files are parsed and imported into the database — once per user, and
only for users with no tasks in the database yet. The original `.md` files
are left untouched, and a user is never reimported (even after exporting or
deleting tasks).

Why SQLite: the SPA, the CLI, and MCP agents share one database-backed board
revision. Task mutations, title changes, and full-board replacements advance
it. WAL permits concurrent access; conditional full-board writes reject a
stale revision instead of erasing an intervening task-level write.

The data directory also holds `secret` — an auto-generated encryption key for
stored AI API keys and forge PATs (see [Integrations](#integrations) and
[AI assist and settings](#ai-assist-and-settings)). `kb.db`, `kb.db-wal`, and
`kb.db-shm` are created or tightened to mode `0600`. A generated secret is
fully written, synced, and closed in a same-directory `0600` temporary file
before an atomic no-overwrite publication; concurrent creators all use the
complete winning file. An existing short or invalid secret is reported and
left unchanged.

## API

| Method | Path              | Auth | Behavior                                                              |
| ------ | ----------------- | ---- | --------------------------------------------------------------------- |
| GET    | `/api/health`     | none | `200` JSON `{"ok":true}`                                              |
| GET    | `/api/config`     | none | `200` JSON `{"azure_client_id","azure_tenant_id"}` (see Entra below)  |
| GET    | `/api/board`      | yes  | `200` `text/markdown`, or JSON `{"board","task_ids"}` when requested; `404` if none exists; every result carries an `ETag` |
| PUT    | `/api/board`      | yes  | `text/markdown` or JSON `{"board","task_ids"}`; `204`, negotiated `200` `{"task_ids":[…]}`, or `409` for a stale `If-Match` |
| GET    | `/api/labels`     | yes  | `200` JSON array of your labels, most recently used first             |
| GET    | `/api/similar`    | yes  | Similar-card advisory for query text; cheap stubs only                |
| POST   | `/api/tombstones` | yes  | Record why a card was killed: `{"task_id","reason"}`; responds `204`  |
| GET    | `/api/settings`   | yes  | `200` JSON `{"ai_base_url","ai_model","has_key"}` (never the key)     |
| PUT    | `/api/settings`   | yes  | JSON patch `{"ai_base_url","ai_model","ai_key"}`; responds `204`      |
| GET    | `/api/integrations` | yes | List configured forge sources; returns `has_token`, never the PAT   |
| PUT, DELETE | `/api/integrations/{name}` | yes | Create/update or remove one scoped forge source             |
| POST   | `/api/integrations/{name}/test` | yes | Test source access; returns an opaque result               |
| POST   | `/api/import/preview` | yes | Fetch and transform configured forge issues for review             |
| POST   | `/api/import/links` | yes | Record selected cards' canonical import provenance                  |
| POST   | `/api/import/drift` | yes | Compare one imported card against its upstream issue, on request    |
| POST   | `/api/import/drift/accept` | yes | Advance that card's baseline after you review the change     |
| POST   | `/api/ai/test`    | yes  | 1-token ping of the configured endpoint; `{"ok":true}` or error       |
| POST   | `/api/ai/story`   | yes  | Prompt in, structured card draft out (see AI assist)                  |
| POST   | `/api/ai/stories` | yes  | ADR or configured forge issue in, `{"stories":[…]}` out (see AI assist) |
| GET    | `/*`              | none | Embedded SPA static files                                             |

Auth applies to every `/api/*` route except `/api/health` and `/api/config`
(the SPA has to read its sign-in configuration before there is anyone to
authenticate); static files are always open.

**Content types are enforced.** A request with a body must declare it:
`PUT /api/board` accepts `text/markdown` or `application/json`;
`application/json` is required everywhere else. Anything else — including the
`text/plain` and form types a cross-site request can send without a preflight —
is rejected with `415`. This is deliberate: it is what stops another site's
page from driving your API.

**Concurrent writes.** `GET /api/board` returns an opaque, revision-backed
`ETag`. Every task or board-title mutation and every full-board replacement
advances that per-user database revision, including writes by the SPA, CLI,
MCP, or an older binary. Revisions are monotonic, not necessarily contiguous;
clients must treat the token as opaque.

Send the token back as `If-Match` on `PUT`. If the board changed underneath
the client, the server answers `409` with the current `ETag`; refetch, merge,
and retry. The `404` for a board that does not exist carries a token too, so a
first write can be conditional. `If-Match: *` succeeds only when a board
exists at the replacement transaction; concurrent edits do not invalidate the
wildcard, while a concurrent deletion returns `409`. A pre-upgrade content-hash
token receives the same `409` and current revision token, after which a normal
refetch/retry works.

The SPA no longer takes this path for ordinary work: it reads and writes
through the per-task endpoints and keeps no board of its own. It uses
`PUT /api/board` only for the markdown file import, deliberately
unconditionally — that action's whole point is "discard everything now on the
board". Remote CLI mutations surface `409` without an unsafe unconditional
retry.

Only the compatibility path — a `text/markdown` `PUT` with no `If-Match` — is
unconditional and last-writer-wins. It can silently delete intervening work.
The JSON path uses a database compare-and-swap even during request handling,
but clients must still send the `ETag` from their own read to protect the whole
read/edit/write interval.

**Canonical-id JSON negotiation.** A literal `Accept: application/json` on
`GET /api/board` returns:

```json
{"board":"# kb\n...","task_ids":["4bc95ae6-3bf7-4bc2-a578-37af48a5ca85"]}
```

The ids follow the card order in the serialized markdown. To preserve those
ids across renames, moves, and duplicate titles, send
`Content-Type: application/json` on `PUT` with exactly the same two fields:

```json
{"board":"# kb\n...","task_ids":["4bc95ae6-3bf7-4bc2-a578-37af48a5ca85",null]}
```

Each string names an existing task owned by the caller; `null` asks the server
to create a new task. The array length and order must match the cards in the
markdown. Malformed, duplicate, unknown, or foreign ids receive the same
non-disclosing `400`. A stale `If-Match` receives `409` before id validation.
Any request containing `null` must also carry a lowercase UUID
`Idempotency-Key`. The server stores a user-scoped receipt in the replacement
transaction. Replaying the same key and exact JSON bytes returns the original
IDs and `ETag` with `Idempotency-Replayed: true`; reusing the key with a
different body returns the same non-disclosing `400`. A new key on markdown or
on JSON without a `null` ID is rejected with that same `400`; the key cannot be
used as an unrelated request label.

By default a successful `PUT` answers `204` with no body. A literal
`Accept: application/json` instead negotiates `200` and
`{"task_ids":[…]}` for the committed cards; wildcard `Accept: */*` and
`Accept: application/json;q=0` retain `204`. Both success forms carry the
committed revision `ETag`.

**The board lives on the server.** The SPA holds no copy of it. It reads
`GET /api/tasks` on startup and after reconnecting, writes every change through
the per-task endpoints, and reads the list back once after each write, so the
display reflects what the server has even when the CLI or MCP is writing at the
same time. Task ids come from the server; the browser never mints one. There is
no offline mode: with no server there is no board, and the page says so.

A refused write shows the server's own words — a rejected field, or the
completion guard refusing a move to Done — and is followed by the same refetch,
so the card snaps back to where the server left it. Drag and keyboard moves send
the destination status and the slot within that column; the confirmation dialog
for shipping an unfinished card sends `force` once the person has answered it.

**Metadata that follows a write.** A cancellation reason is recorded after the
card reaches Cancelled, because the server only accepts a tombstone for a
cancelled task. If that second request fails the card stays cancelled with no
reason and the UI says so. Import provenance is best-effort in the same way: a
failure is reported and never rolls back the cards that were created. Restoring
a cancelled card drops its tombstone server-side; deleting one cascades to its
comments and links.

## The board format

The markdown wire/export format. Example:

```markdown
# Board Title

## To Do

- [ ] 🚚 Migrate CI runner !1 @2026-07-21 ~L #infra #env::prod
  Old runner image is EOL this month.
  - [ ] provision new runner
  - [ ] move secrets

## Doing

- [ ] 🔌 Wire up SSO %blocked #auth

## Done

- [x] 🧱 Init repo

## Cancelled

- [ ] 🧪 Spike a second renderer
```

Tokens on the title line, parsed and stripped: `!1..!4` priority, `@YYYY-MM-DD`
due date, `~S|~M|~L` effort, `%blocked` (the card is flagged blocked), `#tag`
(scoped labels are just tags containing `::`). A leading pictographic character
is the card emoji. Indented plain lines form the description; indented
checkboxes form the checklist. Timestamps are not serialized (they reset on
import).

A title word that would otherwise be read as a token is escaped with one
leading backslash, which the parser strips: write `\%blocked` for a card whose
title really says `%blocked`, and likewise `\!1`, `\~S`, `\@2026-07-21`,
`\#hash`, or a word that starts with `\` itself.

Sections are `## To Do`, `## Doing`, `## Done` and `## Cancelled`, matched by
name (case-insensitively) and, for unrecognized headings, by position in that
order. `## Cancelled` comes after `## Done` and is written only when it has
cards, so a legacy three-section board round-trips byte-for-byte. Cancelled is
the soft-delete column (`kb cancel` / `kb restore`, and the SPA's delete
button); it is excluded from the shipped streak, progress totals and the
default `kb list`.

## Decision graveyard

Killing a card still means moving it to the existing Cancelled soft-delete
column; it is not a new kind of deletion. A tombstone is an optional reason
attached to that killed card. When similar work is proposed later, kb can show
the prior decision as an advisory warning. The warning is dismissible and never
blocks creating or editing a card.

Tombstone reasons are stored as plaintext in the local database. Anyone who can
read that database file can read the reasons, so do not put secrets or other
sensitive information in them.

## CLI

The same `kb` binary is the task CLI. A bare `kb` serves; a subcommand runs
the CLI. Exit codes: `0` ok, `1` runtime error, `2` usage error.

```text
kb add "title"            add a task
kb list                   list tasks
kb update <id>            patch a task (only provided flags change)
kb move <id> <status>     move a task to todo, doing, done, or cancelled
kb done <id>              shorthand for: move <id> done
kb cancel <id>            soft delete: move a task to cancelled, undo with restore
kb restore <id>           move a cancelled task back to todo
kb rm <id>                hard delete, no undo (requires --yes)
kb help                   show help
```

Common flags on every command: `--user name` (board owner, default
`default`) and `--data dir` (default `$KB_DATA` or `~/.local/share/kb`).

Card flags on `add` and `update`: `--desc`, `--status
todo|doing|done|cancelled`, `--prio 1-4`, `--due YYYY-MM-DD`, `--effort
S|M|L`, `--emoji`, `--tag` (repeatable), `--check` (repeatable),
`--blocked` / `--no-blocked`, and `--title` (update only; `add` takes the
title as its argument). `list` takes `--status`, `--all` (cancelled tasks are
hidden by default) and `--json` (full tasks as JSON).

Finishing a task that still has open checklist items, or one flagged blocked,
needs `--force` on `done`, `move <id> done` and `update <id> --status done`.
Without it kb prints what is still open and exits non-zero — it never prompts.

```bash
kb add "Migrate CI runner" --prio 1 --due 2026-08-01 --effort L \
  --tag infra --tag env::prod --check "provision new runner" --check "move secrets"
# added 8c1f4b02 Migrate CI runner

kb list
# ID        STATUS  PRIO  BLOCKED  TITLE              TAGS
# 8c1f4b02  todo    1     -        Migrate CI runner  infra,env::prod

kb update 8c1f --desc "Old runner image is EOL this month." --effort M
kb update 8c1f --blocked          # waiting on something; --no-blocked clears it
kb move 8c1f doing
kb done 8c1f --force              # --force: ship with checklist items still open
kb cancel 8c1f                    # soft delete; kb restore 8c1f undoes it
kb list --all                     # cancelled tasks are hidden without this
kb rm 8c1f --yes                  # hard delete; without --yes it previews and refuses
```

In local mode, tasks are addressed by UUID — any unique prefix works
(`kb move 8c1f doing`); ambiguous prefixes are rejected with a hint.

### Remote mode

Set `KB_SERVER` and the CLI talks HTTP to a running kb server (the markdown
wire API) instead of opening the database:

```bash
export KB_SERVER=https://kb.example.com
export KB_SERVER_TOKEN=<the server's KB_TOKEN>   # omit against an open server
kb list --user ak
# ID  STATUS  PRIO  TITLE              TAGS
# i1  todo    1     Migrate CI runner  infra,env::prod
kb done i1
```

`KB_SERVER_TOKEN` is sent as `Authorization: Bearer ...` and `--user` as
`X-KB-User`, so remote mode pairs with token-mode or open servers (Entra
servers expect short-lived Entra tokens, which don't fit a static env var).

The markdown wire format carries no task ids, so **remote task ids are
ephemeral listing indexes** — `i1`, `i2`, ... in listing order (To Do, Doing,
Done, Cancelled; top to bottom within a column). They are valid only against
the board as currently listed: re-run `kb list` after the board changes before
addressing tasks. A bare number (`kb done 1`) is accepted as shorthand.

## MCP server

`kb mcp` serves the board as an MCP server (name `kb`) over stdio, built on
the official `github.com/modelcontextprotocol/go-sdk`. It opens the local
database directly — no kb server needs to be running.

```bash
kb mcp [--data DIR] [--user NAME]   # defaults: $KB_DATA or ~/.local/share/kb; $KB_USER or "default"
```

Tools:

| Tool              | Purpose                                                                          |
| ----------------- | -------------------------------------------------------------------------------- |
| `list_tasks`      | List tasks (optionally one column), ordered by column then position               |
| `add_task`        | Add a task; only `title` is required (`blocked` optional)                        |
| `update_task`     | Patch fields of a task by id (unique id prefix accepted)                         |
| `move_task`       | Move a task to `todo`, `doing`, `done`, or `cancelled`                           |
| `delete_task`     | Delete a task by id; soft by default (see below)                                 |
| `search_similar`  | Use before creating a card to search card text, tags, and import history         |
| `duplicate_check` | Use before creating a proposed card to check its text and exact provenance link  |

`move_task` to `done` fails with an error naming the open checklist items, or
reporting the blocked flag, unless `force: true` is passed.

`delete_task` takes `soft: boolean`, **default `true`**: the task moves to the
`cancelled` column and can be moved back, so an agent cannot destroy work by
accident. Pass `soft: false` for the permanent row delete.

Harness configuration, Claude-style JSON (Claude Code `.mcp.json`, Claude
Desktop `claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "kb": {
      "command": "kb",
      "args": ["mcp", "--user", "ak"],
      "env": { "KB_DATA": "/home/you/.local/share/kb" }
    }
  }
}
```

Codex-style TOML (`~/.codex/config.toml`):

```toml
[mcp_servers.kb]
command = "kb"
args = ["mcp", "--user", "ak"]
```

## Integrations

Open **Settings → Integrations** to add named GitLab or GitHub sources. Both
public hosts (`gitlab.com`, `github.com`) and self-managed GitLab or GitHub
Enterprise hosts are supported; set each source's base URL to the host and any
installation path prefix. Public repositories can be used without a personal
access token (PAT); private content requires a token with access to the source.

Forge PATs are write-only. The server seals them with AES-256-GCM at rest,
returns only `has_token` to the browser, and sends them to the forge only in
the provider's authentication header (`PRIVATE-TOKEN` for GitLab,
`Authorization: Bearer` for GitHub) — never in a URL, query string, or AI
request. Changing a source to another origin without entering a replacement
PAT clears the saved token.

Forge requests reject loopback, link-local, private, and unspecified addresses
by default. For a source on an enterprise LAN, set
`KB_FORGE_ALLOW_PRIVATE=gitlab.example.com`; use a comma-separated hostname
allowlist for more than one source. `1` or `*` allows every configured forge
host to resolve privately and should be reserved for controlled environments.
This setting belongs only to forge traffic. `KB_AI_ALLOW_PRIVATE` remains the
separate, unchanged opt-in for the configured AI endpoint.

### Importing issues

Issue import also requires [AI assist](#ai-assist-and-settings). Choose
**Import issues**, select a configured source, and enter an issue, project or
repository, board, or milestone URL. A GitLab board URL resolves to that
project's open issues; a named GitHub source also accepts `owner/repo`. kb
fetches at most 20 open issues per preview, asks the configured model to turn
them into card proposals, and shows exact-link and similar-card hints. Review
and edit the proposals, choose a destination column, then press **Add
selected**.

**Imports are one-shot AI transformations, not clones or mirrors; there is no
background sync.** Later changes on the forge do not update cards, and card
changes do not update forge issues. Import again when you want a new snapshot.

### Checking imported issues

Use **Check upstream** on an imported card when you want to compare it with the
current forge issue.
*a drift check is a one-time comparison you ask for; kb never syncs, never polls, and never writes to your forge.*

The first check only records the issue's current state as the baseline. Any
drift between import and that first check is not detected; later checks compare
against the recorded baseline.

Drift baselines keep the issue title and a bounded body excerpt as plaintext in
the local database. Anyone who can read that database file can read the
excerpt, including text captured from a private issue.

## AI assist and settings

The story modal can draft a card from a prompt. `POST /api/ai/story` takes
`{"mode":"create"|"update","prompt":"...","task":{...}}` (`task` is the
current card JSON in update mode) and returns a structured draft —
`{"title","desc","prio","due","effort","tags","checks"}` — that prefills the
modal. **You always confirm before anything is saved.** The server calls the
model, clamps every field into the card contract, and never forwards your API
key to the browser.

The **Split ADR** header button (shown only when AI is configured) turns either
an architecture decision record or one configured forge issue into a set of
stories. `POST /api/ai/stories` accepts exactly one input mode:
`{"adr":"<markdown>","max":<optional>}` or
`{"url":"<issue URL>","source":"<configured name>","max":<optional>}`. In
either mode `max` defaults to 8 and is clamped to 1..20. In URL mode the
server fetches the issue and its bounded discussion, assembles at most 64 KiB
of context, and adds the canonical `link::` provenance tag to every returned
story. The response is `{"stories":[…]}` plus top-level `link`/`url` in URL
mode; each story has the same shape `/api/ai/story` returns. A pasted ADR over
64 KiB is rejected (`413`), source text is never stored as an ADR, and every
returned story goes through the same field clamping. The SPA lists the
proposals with checkboxes and a destination column; nothing reaches the board
until you press **Add selected**.

Configure it on the gear screen (or via `PUT /api/settings`): an
OpenAI-compatible base URL, a model name, and an API key. The base URL is
accepted with or without `/v1`; the server appends `/v1/chat/completions`.
A test-connection button (`POST /api/ai/test`) runs a 1-token ping. It accepts
an optional body `{"ai_base_url","ai_model","ai_key"}` and tests those values
instead of the stored ones, so the gear screen can check a key *before* you
save it; a key sent this way is used for that one request and never persisted,
and leaving the key field blank tests the stored key against the form's URL and
model — but only while that URL keeps the stored scheme and host. Testing a
*different* origin with a blank key is refused ("enter the API key to test a
different endpoint"), for the same reason a save re-points: the stored key
never follows an endpoint you moved. With no body it tests the saved settings.
Examples:

| Provider                  | Base URL                     | Model example       | Key      |
| ------------------------- | ---------------------------- | ------------------- | -------- |
| OpenAI                    | `https://api.openai.com`     | `gpt-4o-mini`       | required |
| ollama.com (cloud shim)   | `https://ollama.com`         | `qwen3-coder:480b`  | required |
| Local Ollama              | `http://localhost:11434`     | `llama3.1`          | none     |

**Key handling**: the API key is write-only and masked in the UI —
`GET /api/settings` returns only `has_key`, never the key. At rest it is
encrypted with AES-256-GCM. The encryption secret is `KB_SECRET` if that env
var is set; otherwise a 32-byte key is generated once at `<data>/secret`
(mode `0600`). Sending `"ai_key": ""` clears the stored key. Changing the
base URL to a different scheme or host without re-sending the key also
clears it (the PUT answers `{"key_cleared":true}`) — a stored credential
never follows a re-pointed endpoint.

**Private endpoints**: the AI client refuses to connect to loopback,
link-local, or private addresses (checked on the resolved IP). For a local
model server such as Ollama, start kb with `KB_AI_ALLOW_PRIVATE=1`.

## Labels

Every task write (SPA, CLI, MCP, import) upserts the task's tags into a
per-user label registry with a last-used stamp. `GET /api/labels` returns
your labels most-recently-used first and feeds the combobox in the story
modal — existing labels are suggested, free text is always allowed. Scoped
labels (`key::value`, e.g. `env::prod`) render as two-tone pills.

## Debug overlay

A performance and capability readout. Turn it on in the app: **⚙ Settings →
Show debug overlay**. The choice is persisted in localStorage (`kb.debug.v1`),
so the overlay stays as you left it across reloads. Untick the same box, or
press the `×` on the overlay itself, to turn it off.

A URL parameter does the same thing for support on a machine you cannot click
through — it overrides the stored flag for that load, and persists it:

```
http://localhost:8080/?debug=1     # on
http://localhost:8080/?debug=0     # off
```

When active, the overlay displays:
- **FPS meter** — rolling average frame rate over the last ~60 frames, updated
  ≤4 times per second so the readout is readable.
- **Renderer capabilities** — shows whether `navigator.gpu` (WebGPU) is
  available and whether a WebGL2 context can be created, each marked as
  available/unavailable. Note: the board is DOM-rendered and confetti uses
  canvas 2D; this is a capability survey only.
- **Frame-rate cap selector** — choose 60 / 90 / 120 FPS or uncapped. The
  selected cap throttles the app's animation frame work by skipping frames,
  and can only *limit* the display refresh rate, never raise it.

The overlay is keyboard-dismissible, does not intercept pointer events over the
board, and carries zero performance cost when disabled. The ⚙ button is always
in the header, including when no server is reachable — the overlay is a local
display preference, and needing it is likeliest exactly then.

## Native controls kb does not draw

Everything in the board is styled to one visual system, with one deliberate
exception: parts of a few native controls belong to the browser or the OS and
cannot be reached from CSS. kb styles the *closed* control and stops there.
The calendar is the one replacement kb does draw: the native panel sat in the
middle of the card form looking like a different product, so the ▦ button
beside the due field opens kb's own popover (month grid, weeks starting
Monday; arrows move a day or a week, PageUp/PageDown a month, Enter picks,
Escape closes). The segmented date *input* stays the browser's — typing and
arrow-key editing keep the native grammar. What remains browser-drawn:

- **The `<select>` option list.** The closed select carries kb's border, paper
  and drop arrow; the list that drops out of it is drawn by the browser.
  `color-scheme: light` is what keeps it legible under a dark OS theme.
- **The file chooser.** The "Choose file" button on the import input is styled
  (`::file-selector-button`); the dialog it opens is the operating system's.
- **Number spinners.** The `Max stories` field in the ADR modal keeps the
  browser's own increment/decrement buttons.
- **The Entra sign-in popup**, which is a Microsoft page — see "What kb sends
  over the network" below.

Both light and dark OS themes are covered: kb pins `color-scheme: light` so
the UA-drawn parts stay legible on paper rather than inverting into a dark
slab under a dark system theme.

## Accessibility

**Standard targeted: WCAG 2.2 Level AA.** This section records what was
measured, how, and what is still open. It is not a conformance claim — kb has
not been audited against 2.2 AA in full, and the gaps below are real.

### Moving a card with the keyboard

Every card is a focusable object (`tabindex="0"`, `role="group"`,
`aria-roledescription="draggable card"`) whose accessible name is its title,
its column, its slot in that column, and its state — for example
"Fix login, Doing, 2 of 4, blocked, 1 of 3 checklist items done". Each card is
described by a hint that spells the key model out, so it is discoverable
without reading this file.

| Key | When | What it does |
| --- | --- | --- |
| <kbd>Tab</kbd> / <kbd>Shift</kbd>+<kbd>Tab</kbd> | anywhere | Move between cards and controls in reading order |
| <kbd>Space</kbd> | card focused, not lifted | Pick the card up |
| <kbd>Enter</kbd> | card focused, not lifted | Open the card for editing |
| <kbd>↑</kbd> / <kbd>↓</kbd> | card lifted | Move it up or down within its column |
| <kbd>←</kbd> / <kbd>→</kbd> | card lifted | Move it to the previous or next column |
| <kbd>Enter</kbd> or <kbd>Space</kbd> | card lifted | Drop it where it now sits |
| <kbd>Esc</kbd> | card lifted | Cancel and put it back where it was picked up |
| <kbd>Tab</kbd> | card lifted | Move focus on — which cancels the move (see below) |
| <kbd>Space</kbd> / <kbd>Enter</kbd> | checklist item focused | Tick or untick it |
| <kbd>Esc</kbd> | modal open | Close the modal and return focus to what opened it |

Nothing is committed while a card is lifted: the board is previewed locally, so
<kbd>Esc</kbd> restores the original order exactly and no save happens per
arrow press. A pointer drag started mid-lift drops the lift rather than running
two moves of the same card at once.

A lift lives only while the lifted card itself has focus. Every key of the
model is on the card, so focus anywhere else — <kbd>Tab</kbd> to the next card,
to a link inside this one, or to a header button that opens a dialog — would
leave a preview on screen that nothing could commit or cancel, while every
column and card name reported a position the board had not saved. Focus leaving
the card therefore cancels the move and says so, exactly as <kbd>Esc</kbd>
would. Every step of a cross-column move re-parents the card in the DOM; focus
is put back on it after each one, including when several arrow presses arrive
in the same tick.

Every pick-up, every arrow step, every drop and every cancel is announced
through one polite, atomic live region — "Fix login moved to Doing, position 2
of 4", "Move cancelled. Fix login is back in To Do, position 1 of 3." Sync
state changes and async errors reach the same region. It is rendered outside
the app shell on purpose: the shell goes `inert` while a dialog is open, which
takes its whole subtree out of the accessibility tree, and a save failure or an
expired session while a card is open is exactly when the region has to be
heard.

The pointer drag is unchanged and still authoritative for mouse and touch: the
grip (`touch-action: none`) is what a touch drag starts from, while the card
body keeps `pan-y` so a finger can still scroll the column.

### What was verified, and how

Measured in headless Chrome against the built binary, in-page via
`getComputedStyle` / `getBoundingClientRect`, at viewport widths of 320, 375,
1280 and 1920 CSS px:

- **1.4.3 Contrast (Minimum)** — every text/background pair that renders was
  computed, including the generated label colours. All pass. The tightest are
  ink `#20242c` on the green chip `#3f9d58` at **4.57:1** and on the blue chip
  `#4f8ef7` at **4.85:1** (both need 4.5:1); secondary text `--dim` `#63697a`
  measures **5.48:1** on the white card and **5.03:1** on the paper. Body text
  on paper is 14.28:1.
- **1.4.11 Non-text Contrast** — the focus ring (ink) measures **15.55:1** on
  the card, **14.28:1** on paper and **12.81–13.26:1** on the three column
  pastels; the checked-box fill and the progress fill measure **3.40:1** on
  white. The progress track is white-on-white and is made perceivable by its
  2px ink border at 15.55:1, not by fill contrast.
- **2.1.1 / 2.1.2 Keyboard** — every interactive element on the board, in the
  card modal, in Settings and in the debug overlay was focused in turn: all
  reachable, all operable, no trap. Modals trap focus deliberately (nothing
  outside is focusable) and return focus to their opener on <kbd>Esc</kbd>.
  That includes the ship warning raised by a keyboard drop, whose opener is a
  card the move has already re-rendered into another column: the restore finds
  the card again by its task id rather than focusing a node that is no longer
  in the document. Measured on all three exits (Cancel, <kbd>Esc</kbd>, "Ship
  anyway"): focus lands on the card, not on `<body>`.
- **2.4.7 Focus Visible** — every focusable element carries a 3px ink outline
  at `:focus-visible`, measured with real <kbd>Tab</kbd> presses on the board,
  the modals, the sign-in form and the debug overlay. Four fields (the modal
  inputs, the sign-in id and token fields, the debug frame cap) suppress the
  outline on plain `:focus` to draw a hard shadow instead, and each restores
  the ring at `:focus-visible` — the restore has to sit *after* the rule that
  removed it, because the two have equal specificity. Measuring with a
  programmatic `.focus()` after a click will show no ring, which is correct
  `:focus-visible` behaviour and not a defect.
- **2.4.11 Focus Not Obscured (Minimum)** — the debug overlay is an opaque
  fixed panel in the bottom-right corner, and it does rest over board content:
  with nine cards in Done, four of them overlap its resting rectangle
  (measured at 1280×720). It steps aside to the top of the viewport whenever
  the focused element would be under its resting place, which is what keeps the
  criterion satisfied — the focused element is never entirely hidden. While it
  is stepped aside it covers the header's Export, Import and Sign out buttons
  instead; those are not the focused element at that moment, so Minimum still
  passes, but this is a small panel that overlaps real controls in both
  positions, not one that stays out of the way.
- **2.5.7 Dragging Movements** — satisfied by the keyboard move above; the
  pointer drag is no longer the single path.
- **2.5.8 Target Size (Minimum)** — with one exception below, no interactive
  target measures under 24×24 CSS px at 320, 375, 1280 and 1920px. Small glyphs
  reach it through an absolute `::after` hit area rather than by growing: the
  chevron is a 20×20 box, the add button a 24×24 chip and the drag grip a 10×18
  glyph, and all three measure **40×40** by walking `elementFromPoint` outwards
  from their centre at each of those widths. The hit areas are sized outright
  (`--hit: 40px`, centred) rather than by a negative `inset`, which resolves
  against the *padding* box and quietly lost 2px per side on the two bordered
  buttons. The "Blocked" checkbox is browser-drawn at 16×16 and reaches the
  minimum through its `<label>`, which is pinned to `min-height: var(--tap)`
  (24px) — exactly at the threshold, so it is the first thing to re-check if
  that label changes.
  The exception: a link inside a card description measures about 65×14, since
  its height is the line box of the sentence around it. That is the SC's own
  *Inline* exception (a target in a sentence, sized by the text flow), so it
  conforms — but it is a target under 24×24 and an audit that enumerates every
  target will find it.
- **1.4.4 Resize Text / 1.4.10 Reflow / 1.4.12 Text Spacing** — at 320px there
  is no horizontal page scroll and nothing overflows outside a scroll
  container; the same holds with text scaled to 200% (measured by doubling the
  seven `--fs-*` tokens, since kb sizes text in px) and with the 1.4.12 spacing
  overrides applied. Two things that make that true are worth naming, because
  both failed before: the footer hint is in the shell's flow rather than fixed
  over the board — its height is text and grows with it, and no fixed board
  padding could keep pace — and the card's title row wraps, because the emoji,
  the age chip and the grip do not shrink and a column clips horizontally. At
  200% text on a 320px screen the shell is taller than the viewport and the
  page scrolls vertically, which is the intended reflow behaviour; the board
  keeps a 200px floor rather than being squeezed to a sliver. The viewport meta
  deliberately sets no `maximum-scale` and no `user-scalable=no`, so pinch zoom
  works; a test guards that.
- **Long values are not lost to a hover-only tooltip.** The header shows the
  signed-in id in a box measured in `ch`, so it grows with the text size rather
  than truncating at 200%; a genuinely long address still ellipses there, and
  Settings prints the id in full — reachable by keyboard and by touch, which a
  `title` tooltip is not.
- **2.3.3 Animation from Interactions** — under
  `prefers-reduced-motion: reduce` all eight transitions and animations
  measured zero. Confetti is additionally suppressed in JS.
- **1.3.1 Info and Relationships** — `header`/`main` landmarks, `h1` → `h2`
  with no skipped level, each column a labelled `region` containing a real
  `ul`/`li`, form controls associated by `<label for>`.
- **3.3.1 / 3.3.3 Error Identification** — field errors set `aria-invalid` and
  point at a `role="alert"` message through `aria-describedby`.
- **Colour is never the only carrier** — priority is a text label *and* a
  distinct shape (rotated square / square / circle / pill), blocked is a
  "⛔ blocked" chip, the sync dot is an `img` with a name.

### Known not to conform, or not yet checked

- **No screen reader has been run against kb.** Everything above is measured
  from the DOM and computed styles. Names, roles and live-region text are
  correct by construction and by inspection of the accessibility tree, but
  NVDA, JAWS and VoiceOver behaviour is untested — live-region verbosity during
  a fast sequence of arrow presses is the likeliest rough edge.
- **The checklist is a group of `role="checkbox"` items, not a list.** This
  satisfies 4.1.2 but is a weaker structure than `ul`/`li` would be.
- **Browser-drawn controls are not fully in scope.** The date picker panel, the
  `<select>` option list, the file chooser and the number spinners are the
  browser's; their contrast and target size are whatever the browser does. See
  "Native controls kb does not draw".
- **1.4.13 Content on Hover or Focus** was not evaluated. kb uses `title`
  tooltips in a few places, and browser-drawn tooltips are not dismissible
  without moving the pointer.
- **2.4.1 Bypass Blocks** — there is no skip link. The board has a shallow tab
  order and no repeated navigation block, so the criterion is arguably not
  applicable, but this has not been confirmed against a long board.
- **1.2.x (time-based media)** and **3.1.2 (language of parts)** are not
  applicable: kb ships no audio or video, and its interface is English-only.
  User content in another language is not marked up as such.
- **Contrast headroom is thin.** The green chip at 4.57:1 clears 4.5:1 by
  0.07. Any darkening of the ink or lightening of that green fails it, so the
  palette is not free to drift.

## Identity modes

The server picks its mode from environment variables at startup:

1. **Azure Entra ID** — set `KB_AZURE_TENANT_ID` and `KB_AZURE_CLIENT_ID`.
   Requests to `/api/*` must carry `Authorization: Bearer <Entra ID token>`
   (the SPA's ID token). The server validates the token itself — RS256
   signature against the tenant's JWKS, issuer, audience, and expiry — and
   takes the user identity from the token's immutable `oid` claim (tokens
   without `oid` are rejected; `email`/`preferred_username` are mutable and
   reassignable, so they are display-only and never select the board).
   **Token decode/verify only: the server never calls Microsoft Graph or any
   other Microsoft API.**
2. **Shared token** — else, if `KB_TOKEN` is set, requests must carry
   `Authorization: Bearer <KB_TOKEN>` (compared in constant time). The user
   identity is the required, non-empty `X-KB-User` header — a manual unique id
   you choose (e.g. your email or a handle).
   In the browser this token is held in `sessionStorage`, never
   `localStorage`: it authorizes *every* user's board, so it must not outlive
   the browser session. Your identity does persist, so a new session starts
   signed in but without a token — the board still works from local storage,
   the header shows a **Reconnect** button, and a *Session expired* dialog
   asks for the token. Reconnecting verifies it against the server before
   adopting it, then pushes anything edited meanwhile.
3. **Open / localhost** — else, no auth. Identity is the `X-KB-User` header if
   present, otherwise `default`. Only use this on a trusted local machine.
   In this mode the server binds to `127.0.0.1` by default; set `KB_BIND` to
   another address only if you understand that every reachable client can
   read and write every board and settings entry.

In Azure mode, the browser also persists MSAL's immutable `homeAccountId` and
uses it to select exactly one cached account and to namespace the device-local
state that remains (today's shipped tally). It never falls back to MSAL's active
account or the first cached account. An older email-only identity is upgraded only when
exactly one cached account has that username; zero or multiple matches require
sign-in again before a token is requested.

Legacy email-keyed browser state is claimed under a Web Lock. The claim owner
is recorded before any copy, so the same `homeAccountId` can resume an
interrupted copy; a different account fails closed. If Web Locks are
unavailable, kb leaves the legacy data untouched and requires reauthentication
or manual import instead of guessing ownership. This browser key is separate
from the server's board identity, which remains the verified token's immutable
`oid` claim.

### How identity maps to boards

Every identity owns exactly one board: all rows in the database are keyed by
`sanitize(identity)`. `sanitize` lowercases the identity and rejects any
character outside `[a-z0-9._@-]` (rejected, not substituted — substitution
would collapse distinct identities such as `anna#lee@corp.com` and
`anna-lee@corp.com` onto the same board). Empty names and names starting with
`.` are also rejected. In Entra mode the identity is the account's `oid`
GUID, so Entra boards are keyed per directory object, not per email;
token-mode boards are keyed by the manual id you choose. Legacy markdown
boards are imported from `<data>/<sanitized identity>.md`, so the same rules
applied when those files were written.

## Azure app registration

You need one app registration; no client secret, no Graph permissions.

1. In the Azure portal, open **Microsoft Entra ID → App registrations → New
   registration**. Name it (e.g. `kb`), pick the supported account types for
   your tenant.
2. Under **Authentication**, add a **Single-page application (SPA)** platform
   and set the redirect URI to **exactly the origin kb is served from, with no
   trailing slash and no path**:

   | How you run kb | Redirect URI to register |
   | --- | --- |
   | the binary, default port | `http://localhost:8080` |
   | the binary, `KB_PORT=9000` | `http://localhost:9000` |
   | behind a domain | `https://kb.example.com` |
   | `npm run dev` (Vite) | `http://localhost:5173` |

   kb sends `window.location.origin` as its redirect URI, and Entra matches
   redirect URIs **exactly** — `https://kb.example.com/` with a trailing slash
   is a different URI and fails with `AADSTS50011`. Register every origin you
   actually use; a dev URI does not cover production. SPA platform means PKCE;
   **do not create a client secret**.

   > **The platform matters as much as the URI.** Registered under **Web**
   > instead of **SPA**, Entra returns the authorization code in the query
   > string rather than the fragment. MSAL watches the fragment, so sign-in
   > hangs forever on "Completing sign-in…" with no error. kb detects this
   > exact case and says so in the popup. Same URI, wrong platform, silent
   > stall — check this first if sign-in never completes.

   One app registration can serve several applications: add each app's origin
   as its own SPA redirect URI on the same registration. Sharing the client id
   is fine — kb only ever reads the ID token's `oid` claim to key your board,
   and never calls Graph.
3. Under **API permissions**, the default delegated `openid`, `profile`,
   `email` scopes are all kb needs. **Do not add Microsoft Graph or any other
   permission.**
4. Copy the **Directory (tenant) ID** and **Application (client) ID** onto the
   server as `KB_AZURE_TENANT_ID` / `KB_AZURE_CLIENT_ID` and restart it. That
   is all a released binary needs: the server validates the tokens the SPA
   sends *and* serves the two IDs to the browser from `GET /api/config`, which
   is what turns on the Microsoft sign-in button. Both IDs are public by
   design — every MSAL SPA ships them in its bundle — so nothing secret is
   exposed; the endpoint returns these two fields and nothing else.

5. Only when running the Vite dev server (`npm run dev`, no kb server behind
   it) do you need the build-time fallback:

   ```bash
   VITE_AZURE_TENANT_ID=<tenant id>
   VITE_AZURE_CLIENT_ID=<client id>
   npm run dev
   ```

   `GET /api/config` wins whenever it answers, so a value baked into the
   bundle never overrides the running server's configuration.

## Server environment reference

| Variable               | Kind               | Default              | Purpose                                                                      |
| ---------------------- | ------------------ | -------------------- | ---------------------------------------------------------------------------- |
| `KB_PORT`              | server, runtime    | `8080`               | Listen port (`--port` flag overrides)                                        |
| `KB_BIND`              | server, runtime    | unset                | Bind address; open mode defaults to `127.0.0.1`, other modes to all ifaces   |
| `KB_AI_ALLOW_PRIVATE`  | server, runtime    | unset                | `1` lets the AI proxy reach loopback/private addresses (local Ollama)        |
| `KB_FORGE_ALLOW_PRIVATE` | server, runtime  | unset                | Comma-separated forge hostnames allowed to resolve privately; `1` or `*` allows all |
| `KB_DATA`              | all modes, runtime | `~/.local/share/kb`  | Data directory: `kb.db`, `secret`, legacy `.md` boards (`--data` overrides)  |
| `KB_LOG_FILE`          | server, runtime    | unset                | Append logs to this file, created with mode `0600` (`--log` overrides)       |
| `KB_TOKEN`             | server, runtime    | unset                | Shared bearer token; enables token mode when Azure vars are unset            |
| `KB_AZURE_TENANT_ID`   | server, runtime    | unset                | Entra tenant ID; with client ID, enables Entra mode; served to the SPA by `GET /api/config` |
| `KB_AZURE_CLIENT_ID`   | server, runtime    | unset                | Entra app (client) ID; expected token audience; served to the SPA by `GET /api/config` |
| `KB_ALLOWED_HOSTS`     | server, runtime    | unset                | Comma-separated extra `Host` values the API accepts; loopback is always allowed |
| `KB_SECRET`            | server/CLI/MCP     | unset                | AES-GCM secret for stored AI keys and forge PATs; if unset, generated at `<data>/secret`. Under 16 bytes the server refuses to start and the CLI/MCP warn |
| `KB_SERVER`            | CLI, runtime       | unset                | Remote mode: base URL of a kb server; CLI talks HTTP instead of the local DB |
| `KB_SERVER_TOKEN`      | CLI, runtime       | unset                | Bearer token the CLI sends in remote mode (the server's `KB_TOKEN`)          |
| `KB_USER`              | MCP, runtime       | `default`            | Board user for `kb mcp` (`--user` flag overrides)                            |
| `VITE_AZURE_TENANT_ID` | frontend, build    | unset                | Dev-server fallback only: baked into the bundle, used when `GET /api/config` returns no tenant ID. For a released binary set `KB_AZURE_TENANT_ID` |
| `VITE_AZURE_CLIENT_ID` | frontend, build    | unset                | Dev-server fallback only: baked into the bundle, used when `GET /api/config` returns no client ID. For a released binary set `KB_AZURE_CLIENT_ID` |

Mode precedence at startup: Azure pair set → Entra mode; else `KB_TOKEN` set →
token mode; else open mode.

When `KB_LOG_FILE` or `--log` is set, kb opens the file once in append mode and
creates it with permissions `0600`. Log rotation is the operator's job. kb does
not reopen the handle on `SIGHUP`, so `logrotate` configurations must use
`copytruncate`. With neither setting, logs continue to go to stderr.

## Running under systemd

```ini
[Unit]
Description=kb board server
After=network-online.target
Wants=network-online.target

[Service]
Slice=workload.slice
ExecStart=/usr/local/bin/kb --port 8080 --data /var/lib/kb
Environment=KB_AZURE_TENANT_ID=<tenant id>
Environment=KB_AZURE_CLIENT_ID=<client id>

# Run as an ephemeral unprivileged user (or use User=kb with a dedicated user).
DynamicUser=yes
StateDirectory=kb

# Hardening
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
ReadWritePaths=/var/lib/kb
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

With `DynamicUser=yes`, `StateDirectory=kb` provisions `/var/lib/kb` with
correct ownership; the database (`kb.db`) and the encryption secret file live
there. If you prefer a fixed account, replace `DynamicUser=yes` with
`User=kb` and create the user and data directory yourself. Terminate TLS in
front of the binary (Caddy, nginx, etc.) — kb itself speaks plain HTTP.

## What kb sends over the network

kb is an on-device app with zero telemetry. There is no analytics, no crash
reporting, no font or asset CDN, and no update check. This section is the
exhaustive list of traffic kb can originate, including the parts that are not
entirely ours to control.

**1. Same-origin requests to kb's own API.** The only traffic a signed-in
board generates. Loading the app fetches `/assets/*.js`, `/assets/*.css` and
`/favicon.ico`; the app then calls `/api/config`, `/api/health`, `/api/board`,
`/api/labels` and `/api/settings`; `/api/similar` while editing a card;
`/api/integrations/*` in Settings; `/api/import/*` when importing issues; and
`/api/ai/story`, `/api/ai/stories` and `/api/ai/test` when you use AI assist.
Nothing else. The emoji table is compiled into the bundle
(`@emoji-mart/data`), so the picker fetches neither its data nor a spritesheet
— it renders system emoji glyphs. All fonts are system stacks; there is no
`@font-face` pointing off-origin.

This is enforced, not merely observed. The SPA response carries a
Content-Security-Policy that blocks cross-origin requests:

```
default-src 'self'; connect-src 'self'; script-src 'self';
style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self';
frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none';
frame-ancestors 'none'
```

`style-src` allows inline styles for two reasons: kb's own React
`style={{…}}` attributes carry values that have to be computed (the drag
clone's position, progress-bar widths, tag colours), and emoji-mart styles its
shadow-DOM picker by setting the text of a `<style>` element. Vite's own output
is a linked stylesheet and needs nothing.

Two channels are **not** covered by that policy, in any browser, so they are
named here rather than left implied:

- **WebRTC.** `connect-src` does not govern `RTCPeerConnection` anywhere, and
  a `stun:`/`turn:` URL is not http(s) either. CSP3 defines `webrtc 'block'`,
  but no shipping engine implements it — Chrome 151 opens a peer connection
  under it and logs "Unrecognized Content-Security-Policy directive 'webrtc'"
  — so kb does not send a directive that would only dirty the console.
- **Resource hints** (`<link rel="preconnect">`, `dns-prefetch`). No directive
  covers them. MSAL adds one in Azure mode — see below.

Both are covered by the repo guard instead: `src/egress.test.ts` fails the
build if an absolute URL, an off-origin `href`/`src`/`url()`, or a network API
(including `RTCPeerConnection`) appears anywhere in the shipping frontend —
the modules, `src/styles.css` and `index.html` — outside a small commented
allowlist.

**2. The Entra sign-in handshake — only when Azure auth is configured.** With
`KB_AZURE_TENANT_ID`/`KB_AZURE_CLIENT_ID` set, `connect-src` and `frame-src`
gain `https://login.microsoftonline.com` and nothing else, because:

- The browser (MSAL) opens a sign-in popup at that host and later renews the
  token there, through a hidden iframe when the session cookie still holds.
- The server fetches the tenant JWKS
  (`https://login.microsoftonline.com/<tenant>/discovery/v2.0/keys`) to verify
  token signatures. It caches the keys and calls no other Microsoft endpoint —
  no Microsoft Graph, no profile lookup.

Two things about MSAL are worth stating plainly rather than omitting:

- The sign-in **popup is a Microsoft page**, not a kb page. Our CSP does not
  and cannot govern it, so it loads Microsoft's own assets and reports to
  Microsoft's own telemetry, exactly as signing in at `office.com` would. It
  never receives your board — kb's board data is never sent to Microsoft.
- MSAL injects a `<link rel="preconnect">` to `login.microsoftonline.com` when
  it initialises. That is a DNS lookup and TLS handshake only, carrying no kb
  data, and no CSP directive can suppress it.

Without Azure configured, MSAL is never loaded at all (it is a lazy chunk) and
the policy has no cross-origin entry.

**3. Your configured GitLab/GitHub sources — server-side only.** Forge calls
go only to API hosts derived from sources you configure: `github.com` uses
`api.github.com`; GitLab and enterprise sources use their configured base
host. Testing a source, previewing an import, or splitting from a forge issue
URL makes the server request GitLab or GitHub API data from the selected
source. A configured PAT travels only in the provider's authentication
header. The forge client rejects private-address resolution by default; a
hostname must be explicitly allowlisted with `KB_FORGE_ALLOW_PRIVATE` to
reach an enterprise LAN. Redirects follow the policy below. The client ignores
`HTTP_PROXY`/`HTTPS_PROXY`.

**4. Your own configured LLM endpoint — server-side only.** If you set an AI
base URL in Settings, the *server* posts chat completions to it; the browser
never calls the model endpoint directly, so the API key never reaches the
browser and is stored AES-256-GCM encrypted at rest. The request contains the
prompt you typed; for a story split, either the document you pasted or the
bounded forge issue title, body, and comments; or, for issue import, the
bounded source titles, references, labels, bodies, and comments needed for the
transformation — nothing else about your board. The endpoint is SSRF-guarded:
the resolved IP may not be loopback, private, link-local or unspecified
(unless `KB_AI_ALLOW_PRIVATE=1` opts in for a local model server such as
Ollama), redirects follow the policy below, the round trip is capped at 60s,
and a base URL containing a username or password is rejected outright so a
credential cannot be stored in the clear. This client ignores
`HTTP_PROXY`/`HTTPS_PROXY`; it dials the endpoint directly.

**5. CLI remote mode.** `kb` with `KB_SERVER` set talks to that server and only
that server — every request carries your token and replays the whole board.
Without `KB_SERVER` the CLI is purely local, and `kb mcp` never opens a socket
at all: it speaks stdio.

The forge, AI, and remote CLI clients apply the same redirect boundary. The
normalized hostname and explicit port must remain identical to the original
request; changing a subdomain or adding, removing, or changing a port is
rejected. HTTP may upgrade to HTTPS on that same host and port, but HTTPS may
not downgrade to HTTP. URL credentials and non-HTTP(S) targets are also
rejected.

Unlike the AI and forge clients, the JWKS and CLI remote clients use Go's
default transport and therefore honour `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`.
If those are set in kb's environment, that proxy sees those requests.

Everything else is on-device: boards, labels, settings, integration
configuration, and import history live in the SQLite database under the data
directory and nowhere else.

## Security notes

**What the server validates**

- Entra mode: RS256 signature against the tenant JWKS
  (`https://login.microsoftonline.com/<tenant>/discovery/v2.0/keys`), issuer
  `https://login.microsoftonline.com/<tenant>/v2.0`, audience equal to the
  client ID, and `exp`/`nbf`. Identity is read from the verified token only.
- Token mode: constant-time comparison of the bearer token (no timing oracle),
  and a required non-empty `X-KB-User`.
- All modes: the identity sanitizer rejects malformed identities, and every
  database row is scoped to the authenticated identity — no path traversal,
  no cross-user reads.

**Stored credential handling**

- Stored AI API keys and forge PATs are AES-256-GCM encrypted at rest (secret:
  `KB_SECRET` or the generated `<data>/secret`, mode 0600). The API returns
  only `has_key` or `has_token`, never plaintext credentials; upstream error
  messages never contain them.
- The AI proxy only calls the base URL you configured (http/https); request
  bodies are size-capped and responses clamped into the draft contract.
- The forge client only calls configured sources. PATs are sent in
  authentication headers, never in URLs, and are never sent to the AI
  endpoint.

**What it does not do**

- No Microsoft Graph or network calls to Microsoft beyond fetching the JWKS.
- No sessions, refresh handling, or token revocation checks — a token is good
  until it expires.
- No authorization beyond identity: any valid identity reads and writes its
  own board, nothing else; there are no roles or sharing.
- No TLS and no rate limiting — put it behind a reverse proxy.

**Sync semantics**

Every writer participates in the per-user database revision. Local CLI and MCP
task-level mutations advance it; first-party SPA and remote CLI full-board
writes send the revision-backed `ETag` they read. If a task-level writer wins
the race, the stale full-board write receives `409` instead of deleting that
change. Safety comes from this shared revision invariant, not from task-level
storage by itself.

The SPA's own writes are task-level, so they advance the revision like any
other writer and cannot delete a change they never read. Its one full-board
write is the markdown file import.

The legacy markdown replacement still matches incoming cards to existing rows
by status/title and then title so ids and creation timestamps can survive a
round-trip. A markdown `PUT` without `If-Match` remains unconditional for
compatibility and can overwrite concurrent work. Prefer conditional clients
and the canonical-id JSON envelope; keep a markdown export if you want history.
