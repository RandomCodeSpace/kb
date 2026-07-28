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
- **MCP server** — `kb mcp` exposes the board as five task tools over stdio
  for agent harnesses (see [MCP server](#mcp-server)).
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

Markdown is now the **wire and export format**, not the storage format:

- `GET/PUT /api/board` still speak `text/markdown` — the SPA sync API is
  unchanged, and UI import/export is unchanged.
- A Go markdown codec (same grammar as the frontend's `src/lib/markdown.ts`,
  shared golden test vectors) converts at the boundary.

**Automatic one-time import**: on first start, any existing `<data>/<user>.md`
board files are parsed and imported into the database — once per user, and
only for users with no tasks in the database yet. The original `.md` files
are left untouched, and a user is never reimported (even after exporting or
deleting tasks).

Why SQLite: the SPA, the CLI, and MCP agents can now write concurrently.
Whole-file last-write-wins markdown cannot survive that; WAL plus task-level
operations can.

The data directory also holds `secret` — an auto-generated encryption key for
stored AI API keys (see [AI assist and settings](#ai-assist-and-settings)).

## API

| Method | Path              | Auth | Behavior                                                              |
| ------ | ----------------- | ---- | --------------------------------------------------------------------- |
| GET    | `/api/health`     | none | `200` JSON `{"ok":true}`                                              |
| GET    | `/api/config`     | none | `200` JSON `{"azure_client_id","azure_tenant_id"}` (see Entra below)  |
| GET    | `/api/board`      | yes  | `200` `text/markdown` (your board), `404` if none saved yet; both carry an `ETag` |
| PUT    | `/api/board`      | yes  | Body is `text/markdown`; replaces your board; `204`, or `409` on a stale `If-Match` |
| GET    | `/api/labels`     | yes  | `200` JSON array of your labels, most recently used first             |
| GET    | `/api/settings`   | yes  | `200` JSON `{"ai_base_url","ai_model","has_key"}` (never the key)     |
| PUT    | `/api/settings`   | yes  | JSON patch `{"ai_base_url","ai_model","ai_key"}`; responds `204`      |
| POST   | `/api/ai/test`    | yes  | 1-token ping of the configured endpoint; `{"ok":true}` or error       |
| POST   | `/api/ai/story`   | yes  | Prompt in, structured card draft out (see AI assist)                  |
| POST   | `/api/ai/stories` | yes  | `{"adr","max"}` in, `{"stories":[…]}` out (see AI assist)             |
| GET    | `/*`              | none | Embedded SPA static files                                             |

Auth applies to every `/api/*` route except `/api/health` and `/api/config`
(the SPA has to read its sign-in configuration before there is anyone to
authenticate); static files are always open.

**Content types are enforced.** A request with a body must declare it:
`text/markdown` on `PUT /api/board`, `application/json` everywhere else.
Anything else — including the `text/plain` and form types a cross-site request
can send without a preflight — is rejected with `415`. This is deliberate: it
is what stops another site's page from driving your API.

**Concurrent writes.** `GET /api/board` returns an `ETag` — a token for the
board's exact contents, so a write by any surface (SPA, CLI, MCP) changes it.
Send it back as `If-Match` on your `PUT` and a board that changed underneath
you answers `409` (with the current `ETag`) instead of overwriting the other
writer; refetch, merge, and retry. The `404` from a board that does not exist
yet carries the token too, so a first write is conditional as well. A `PUT`
with no `If-Match` is unconditional and last-writer-wins, which will silently
delete tasks another surface created — always send the token.

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

| Tool          | Purpose                                                              |
| ------------- | -------------------------------------------------------------------- |
| `list_tasks`  | List tasks (optionally one column), ordered by column then position |
| `add_task`    | Add a task; only `title` is required (`blocked` optional)            |
| `update_task` | Patch fields of a task by id (unique id prefix accepted)             |
| `move_task`   | Move a task to `todo`, `doing`, `done`, or `cancelled`               |
| `delete_task` | Delete a task by id; soft by default (see below)                     |

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

## AI assist and settings

The story modal can draft a card from a prompt. `POST /api/ai/story` takes
`{"mode":"create"|"update","prompt":"...","task":{...}}` (`task` is the
current card JSON in update mode) and returns a structured draft —
`{"title","desc","prio","due","effort","tags","checks"}` — that prefills the
modal. **You always confirm before anything is saved.** The server calls the
model, clamps every field into the card contract, and never forwards your API
key to the browser.

The **Split ADR** header button (shown only when AI is configured) turns an
architecture decision record into a set of stories. `POST /api/ai/stories`
takes `{"adr":"<markdown>","max":<1..20, default 8>}` and returns
`{"stories":[…]}`, where each story has the same shape `/api/ai/story`
returns. An ADR over 64 KiB is rejected (`413`), the ADR text is never stored
server-side, and every returned story goes through the same field clamping.
The SPA lists the proposals with checkboxes and a destination column; nothing
reaches the board until you press **Add selected**.

Configure it on the gear screen (or via `PUT /api/settings`): an
OpenAI-compatible base URL, a model name, and an API key. The base URL is
accepted with or without `/v1`; the server appends `/v1/chat/completions`.
A test-connection button (`POST /api/ai/test`) runs a 1-token ping. Examples:

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

A hidden performance and capability readout, opened by adding `?debug=1` to
the board's URL:

```
http://localhost:8080/?debug=1     # on
http://localhost:8080/?debug=0     # off
```

The flag is persisted in localStorage (`kb.debug.v1`), so once you have turned
it on the overlay stays on across reloads at a plain `http://localhost:8080/`
— turn it off with `?debug=0` or the `×` on the overlay itself.

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
board, and carries zero performance cost when disabled.

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
3. **Open / localhost** — else, no auth. Identity is the `X-KB-User` header if
   present, otherwise `default`. Only use this on a trusted local machine.
   In this mode the server binds to `127.0.0.1` by default; set `KB_BIND` to
   another address only if you understand that every reachable client can
   read and write every board and settings entry.

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
| `KB_DATA`              | all modes, runtime | `~/.local/share/kb`  | Data directory: `kb.db`, `secret`, legacy `.md` boards (`--data` overrides)  |
| `KB_TOKEN`             | server, runtime    | unset                | Shared bearer token; enables token mode when Azure vars are unset            |
| `KB_AZURE_TENANT_ID`   | server, runtime    | unset                | Entra tenant ID; with client ID, enables Entra mode; served to the SPA by `GET /api/config` |
| `KB_AZURE_CLIENT_ID`   | server, runtime    | unset                | Entra app (client) ID; expected token audience; served to the SPA by `GET /api/config` |
| `KB_ALLOWED_HOSTS`     | server, runtime    | unset                | Comma-separated extra `Host` values the API accepts; loopback is always allowed |
| `KB_SECRET`            | server/CLI/MCP     | unset                | AES-GCM secret for stored AI keys; if unset, generated at `<data>/secret`. Under 16 bytes the server refuses to start and the CLI/MCP warn |
| `KB_SERVER`            | CLI, runtime       | unset                | Remote mode: base URL of a kb server; CLI talks HTTP instead of the local DB |
| `KB_SERVER_TOKEN`      | CLI, runtime       | unset                | Bearer token the CLI sends in remote mode (the server's `KB_TOKEN`)          |
| `KB_USER`              | MCP, runtime       | `default`            | Board user for `kb mcp` (`--user` flag overrides)                            |
| `VITE_AZURE_TENANT_ID` | frontend, build    | unset                | Dev-server fallback only: baked into the bundle, used when `GET /api/config` returns no tenant ID. For a released binary set `KB_AZURE_TENANT_ID` |
| `VITE_AZURE_CLIENT_ID` | frontend, build    | unset                | Dev-server fallback only: baked into the bundle, used when `GET /api/config` returns no client ID. For a released binary set `KB_AZURE_CLIENT_ID` |

Mode precedence at startup: Azure pair set → Entra mode; else `KB_TOKEN` set →
token mode; else open mode.

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

**AI key handling**

- Stored AI API keys are AES-256-GCM encrypted at rest (secret: `KB_SECRET`
  or the generated `<data>/secret`, mode 0600). The API returns only
  `has_key`, never the key; AI error messages never contain it; the browser
  only ever sends it once, on save.
- The AI proxy only calls the base URL you configured (http/https); request
  bodies are size-capped and responses clamped into the draft contract.

**What it does not do**

- No Microsoft Graph or network calls to Microsoft beyond fetching the JWKS.
- No sessions, refresh handling, or token revocation checks — a token is good
  until it expires.
- No authorization beyond identity: any valid identity reads and writes its
  own board, nothing else; there are no roles or sharing.
- No TLS and no rate limiting — put it behind a reverse proxy.

**Sync semantics**

The markdown wire API is still last-write-wins on the whole board:
`PUT /api/board` replaces your board, and if the same identity PUTs from two
places, the later write wins. Since the store is task-level SQLite, the
replace matches incoming cards to existing ones by title, so ids and creation
timestamps survive round-trips. Task-level writers — the CLI in local mode
and MCP tools — update individual rows and are safe to run concurrently with
the SPA. Keep a copy via markdown export if you want history.
