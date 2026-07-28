# kb

A personal kanban board that stays plain text.

**kb** is a rich-card kanban web app (PWA-style single-page app) served by a
single-binary Go server. The frontend is Vite + React + TypeScript; the server
embeds the built SPA, stores boards in a single SQLite database, and speaks a
tiny markdown wire API — your whole board still round-trips as one
human-readable `.md` document. The same binary is also a task CLI and an MCP
server for coding agents.

## Features

- **Classic status columns** — To Do / Doing / Done.
- **Rich cards** — emoji, wrapping title, description snippet, inline
  expandable checklist (tick subtasks directly on the board), chunky progress
  bar, priority dot, relative due chip ("in 5d" / "overdue · 6d"), effort chip
  (S/M/L), plain tags, and GitLab-style scoped labels (`key::value` two-tone
  pills).
- **Juice that earns its place** — confetti on ship, mini-confetti on subtask
  tick, ticking the last subtask auto-ships the card, daily streak counter,
  drag with lift shadow and column highlight (pointer events, no dnd library).
- **Plain-text native** — markdown is the wire and export format.
  Import/export from the UI; round-trip is lossless for well-formed files.
- **Single binary server** — the Go server embeds the SPA and stores all
  boards in one SQLite file (`modernc.org/sqlite`, pure Go, WAL mode, no
  CGO). Existing per-user `.md` boards are imported automatically on first
  start.
- **CLI** — `kb add/list/update/move/done/rm` against the local database, or
  against a remote kb server with `KB_SERVER` (see [CLI](#cli)).
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

> The `dist/` directory (the built SPA) is committed to the repository on
> purpose: `go install` builds from the module source, and the server embeds
> `dist/` at compile time. Without it in the tree, `go install` would produce a
> server with no frontend.

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

| Method | Path            | Auth | Behavior                                                          |
| ------ | --------------- | ---- | ----------------------------------------------------------------- |
| GET    | `/api/health`   | none | `200` JSON `{"ok":true}`                                          |
| GET    | `/api/board`    | yes  | `200` `text/markdown` (your board), `404` if none saved yet       |
| PUT    | `/api/board`    | yes  | Body is `text/markdown`; replaces your board; responds `204`      |
| GET    | `/api/labels`   | yes  | `200` JSON array of your labels, most recently used first         |
| GET    | `/api/settings` | yes  | `200` JSON `{"ai_base_url","ai_model","has_key"}` (never the key) |
| PUT    | `/api/settings` | yes  | JSON patch `{"ai_base_url","ai_model","ai_key"}`; responds `204`  |
| POST   | `/api/ai/test`  | yes  | 1-token ping of the configured endpoint; `{"ok":true}` or error   |
| POST   | `/api/ai/story` | yes  | Prompt in, structured card draft out (see AI assist)              |
| GET    | `/*`            | none | Embedded SPA static files                                         |

Auth applies to every `/api/*` route except `/api/health`; static files are
always open.

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

## Done

- [x] 🧱 Init repo
```

Tokens on the title line, parsed and stripped: `!1..!4` priority, `@YYYY-MM-DD`
due date, `~S|~M|~L` effort, `#tag` (scoped labels are just tags containing
`::`). A leading pictographic character is the card emoji. Indented plain lines
form the description; indented checkboxes form the checklist. Timestamps are
not serialized (they reset on import).

## CLI

The same `kb` binary is the task CLI. A bare `kb` serves; a subcommand runs
the CLI. Exit codes: `0` ok, `1` runtime error, `2` usage error.

```text
kb add "title"            add a task
kb list                   list tasks
kb update <id>            patch a task (only provided flags change)
kb move <id> <status>     move a task to todo, doing, or done
kb done <id>              shorthand for: move <id> done
kb rm <id>                delete a task (requires --yes)
kb help                   show help
```

Common flags on every command: `--user name` (board owner, default
`default`) and `--data dir` (default `$KB_DATA` or `~/.local/share/kb`).

Card flags on `add` and `update`: `--desc`, `--status todo|doing|done`,
`--prio 1-4`, `--due YYYY-MM-DD`, `--effort S|M|L`, `--emoji`, `--tag`
(repeatable), `--check` (repeatable), and `--title` (update only; `add` takes
the title as its argument). `list` takes `--status` and `--json` (full tasks
as JSON).

```bash
kb add "Migrate CI runner" --prio 1 --due 2026-08-01 --effort L \
  --tag infra --tag env::prod --check "provision new runner" --check "move secrets"
# added 8c1f4b02 Migrate CI runner

kb list
# ID        STATUS  PRIO  TITLE              TAGS
# 8c1f4b02  todo    1     Migrate CI runner  infra,env::prod

kb update 8c1f --desc "Old runner image is EOL this month." --effort M
kb move 8c1f doing
kb done 8c1f
kb rm 8c1f --yes    # without --yes it previews the task and refuses
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
Done; top to bottom within a column). They are valid only against the board
as currently listed: re-run `kb list` after the board changes before
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
| `add_task`    | Add a task; only `title` is required                                 |
| `update_task` | Patch fields of a task by id (unique id prefix accepted)             |
| `move_task`   | Move a task to `todo`, `doing`, or `done`                            |
| `delete_task` | Delete a task by id                                                  |

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
   with your redirect URI (e.g. `https://kb.example.com/` — and
   `http://localhost:5173/` for dev). SPA platform means PKCE; **do not create
   a client secret**.
3. Under **API permissions**, the default delegated `openid`, `profile`,
   `email` scopes are all kb needs. **Do not add Microsoft Graph or any other
   permission.**
4. Copy the **Directory (tenant) ID** and **Application (client) ID** into the
   frontend build environment:

   ```bash
   VITE_AZURE_TENANT_ID=<tenant id>
   VITE_AZURE_CLIENT_ID=<client id>
   npm run build
   ```

5. Set the same two IDs on the server as `KB_AZURE_TENANT_ID` /
   `KB_AZURE_CLIENT_ID` so it validates the tokens the SPA sends.

## Server environment reference

| Variable               | Kind               | Default              | Purpose                                                                      |
| ---------------------- | ------------------ | -------------------- | ---------------------------------------------------------------------------- |
| `KB_PORT`              | server, runtime    | `8080`               | Listen port (`--port` flag overrides)                                        |
| `KB_BIND`              | server, runtime    | unset                | Bind address; open mode defaults to `127.0.0.1`, other modes to all ifaces   |
| `KB_AI_ALLOW_PRIVATE`  | server, runtime    | unset                | `1` lets the AI proxy reach loopback/private addresses (local Ollama)        |
| `KB_DATA`              | all modes, runtime | `~/.local/share/kb`  | Data directory: `kb.db`, `secret`, legacy `.md` boards (`--data` overrides)  |
| `KB_TOKEN`             | server, runtime    | unset                | Shared bearer token; enables token mode when Azure vars are unset            |
| `KB_AZURE_TENANT_ID`   | server, runtime    | unset                | Entra tenant ID; with client ID, enables Entra mode                          |
| `KB_AZURE_CLIENT_ID`   | server, runtime    | unset                | Entra app (client) ID; expected token audience                               |
| `KB_SECRET`            | server/CLI/MCP     | unset                | AES-GCM secret for stored AI keys; if unset, generated at `<data>/secret`    |
| `KB_SERVER`            | CLI, runtime       | unset                | Remote mode: base URL of a kb server; CLI talks HTTP instead of the local DB |
| `KB_SERVER_TOKEN`      | CLI, runtime       | unset                | Bearer token the CLI sends in remote mode (the server's `KB_TOKEN`)          |
| `KB_USER`              | MCP, runtime       | `default`            | Board user for `kb mcp` (`--user` flag overrides)                            |
| `VITE_AZURE_TENANT_ID` | frontend, build    | unset                | Baked into the SPA at `npm run build`; enables the MSAL sign-in flow         |
| `VITE_AZURE_CLIENT_ID` | frontend, build    | unset                | Baked into the SPA at `npm run build`; MSAL client ID                        |

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
