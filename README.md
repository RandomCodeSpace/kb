# kb

`kb` is a local-first kanban board for terminals, command-line tools, and AI
agents. The full-screen TUI, task CLI, and MCP server all read the same SQLite
database directly. An optional API-only HTTP server remains available for
remote CLI clients and integrations.

There is no web UI, JavaScript bundle, or browser runtime in the binary.

## Install

With Go 1.25.8 or newer, install the stable release:

```sh
go install github.com/RandomCodeSpace/kb@v1.0.0
kb version
```

Or build a checkout:

```sh
go test ./...
CGO_ENABLED=0 go build -o kb .
```

No Node.js, npm, Vite, or generated asset directory is required.

Prebuilt CGO-free binaries for Linux amd64/arm64, macOS amd64/arm64, and
Windows amd64 are attached to the
[v1.0.0 release](https://github.com/RandomCodeSpace/kb/releases/tag/v1.0.0).
Download `SHA256SUMS` with the binary and verify the files before installation:

```sh
grep ' kb-linux-amd64$' SHA256SUMS | sha256sum -c -
```

Substitute the downloaded asset name. On macOS use
`shasum -a 256 -c -`; on Windows compare `Get-FileHash -Algorithm SHA256`
with the matching manifest line.

## Start

Run `kb` in an interactive terminal:

```sh
kb
```

When both stdin and stdout are TTYs, a bare `kb` opens the full-screen TUI.
When either stream is redirected, it prints root help and exits successfully
without creating or opening the data directory. `kb tui` remains the explicit
form and accepts `--data`.

```sh
kb tui --data ~/.local/share/kb
kb --help
```

The default data directory is `$KB_DATA` or `~/.local/share/kb`. Local modes
always operate on the `default` board.

## Terminal UI

The TUI is the primary human interface. It includes:

- responsive Todo, Doing, Done, and optional Cancelled columns;
- card detail, markdown rendering, comments, and blocker links;
- create/edit forms with labels, due dates, effort, priority, checklists, and
  blocked state;
- persistent text and label filters per board;
- a project switcher scoping the board to one project or all of them, persisted
  per board, with a mandatory project field on every card;
- keyboard lift/drop and mouse drag moves with filtered-order correctness;
- completion guards, tick-all/force-ship choices, auto-ship, unship, cancel,
  restore, and permanent deletion;
- AI-assisted card drafts and ADR splitting with review before writes;
- forge issue import, duplicate review, provenance, and upstream drift review;
- local settings for AI providers, forge sources, and display identity.

The footer shows the controls available in the current view. The primary board
keys are:

| Key | Action |
| --- | --- |
| `j`/`k`, arrows | Move between cards |
| `h`/`l`, `Tab`/`Shift+Tab` | Move between columns |
| `1`..`4` | Jump to a column |
| `Enter` | Open card detail |
| `Space` | Lift/drop a card |
| `n` / `e` | Create / edit a card |
| `/` / `f` | Edit / clear filters |
| `p` / `P` | Switch project (next / previous, `all` included) |
| `c` | Toggle Cancelled |
| `s` | Open settings |
| `a` | Split an ADR into cards |
| `i` | Import forge issues |
| `q` | Quit |

Card-detail actions are shown in its footer, including comment and blocker
management, checklist toggles, shipping, cancellation, restore, and permanent
delete. `Escape` closes an overlay or cancels the current operation. Editors
use `Tab` to navigate, `Ctrl+S` to save, and protect unsaved work on close.

## Data and identity

The data directory contains:

- `kb.db`, the SQLite database;
- `secret`, the generated encryption secret when `KB_SECRET` is unset;
- `skills/`, optional user skill overrides;
- `state.json`, the task CLI's active project;
- TUI preference state scoped by database path.

The TUI, task CLI, and MCP server all operate on the single `default` board.
Board owners still exist in the schema, because `kb serve` serves one board per
authenticated identity, but they are not selectable from local commands. A
database written by an older version that holds tasks under another owner keeps
them; local commands print one warning line naming those namespaces and leave
the data alone. `kb users` lists them, and `kb serve` still serves them.

Provider API keys and forge tokens are encrypted at rest with AES-256-GCM.
Keep the `secret` file with the database. Losing it makes stored credentials
unreadable. A short explicit `KB_SECRET` is unsafe; `kb serve` refuses it and
local entry points warn.

SQLite WAL mode may create `kb.db-wal` and `kb.db-shm`. Stop writers and back
up all three database files plus `secret` together.

Legacy Markdown boards in the data directory are imported on first store open.
The import is idempotent; imported Markdown files are not deleted. The same
open backfills any task without a `project::` label to `project::inbox`, also
idempotently.

## Upgrade from the web UI release

No data migration is required. Point the new binary at the same data directory.
Cards, settings, integrations, comments, blocker links, tombstones, import
provenance, and encrypted credentials remain in the existing SQLite schema.

The command surface changed deliberately:

- old bare `kb --port 8080 --data DIR` becomes
  `kb serve --port 8080 --data DIR`;
- bare `kb` now opens the TUI on a terminal, or prints help when redirected;
- `kb tui`, every task CLI verb, `kb mcp`, and `kb version` remain available;
- `/`, static assets, and all other non-API HTTP paths now return 404.

Root server flags are rejected with exit code 2 and point to `kb serve`, so an
old service fails visibly instead of silently launching the wrong mode.

If an old Entra-backed deployment used the immutable `oid` claim as its board
owner, that board is still served by `kb serve` under the same identity.
Browser-only session tokens and display state are not database records and are
not migrated.

Update service units and container commands before replacing the binary. A
reverse proxy may continue forwarding `/api/*` for API clients, but there is no
HTML application behind `/`.

## Task CLI

The task CLI works locally by default:

```sh
kb add "Fix the release" --prio 1 --tag release
kb list
kb view 1
kb update 1 --check "reproduce" --check "x patch"
kb move 1 doing
kb done 1
kb cancel 1
kb restore 1
kb rm 1 --yes
```

Run `kb help` for the full flag reference. Supported verbs are `add`, `list`,
`view`, `update`, `move`, `done`, `cancel`, `restore`, `rm`, `project`, `users`,
`comment`, `link`, and `unlink`.

### Projects

Every task belongs to exactly one project, stored as the scoped label
`project::<name>`. Projects slice one board rather than splitting it into
several: nothing about the schema, the wire format, or the server changes, and
a project is filtered like any other label.

```sh
kb project use web          # set the active project
kb project current          # print the project commands default to
kb project list             # every project with its task count
kb add "Ship the docs"      # filed under project::web
kb add "Hotfix" -p api      # this one command goes to project::api
kb update 1 -p api          # move an existing task to another project
kb list --tag project::api  # filter by project
```

`kb add` refuses to invent a project: if none resolves it errors and names the
two fixes. Resolution order is `-p/--project`, then `KB_PROJECT`, then the
project stored by `kb project use`. The stored choice lives in `state.json`
inside the data directory — it is client state, so it resolves the same way in
`KB_SERVER` mode, where no local database is opened, and never travels to the
board's readers.

No task-mutating command leaves a task with zero or two project labels:
`--tag` replaces the plain labels but keeps the project, `-p` moves the task,
and spelling `--tag project::<name>` does the same. Contradicting `-p` with a
`project::` label, or passing two of them, is refused.

Tasks created before projects existed are given `project::inbox` the first
time kb opens the local database — automatically, once, and idempotently, so
the rule is already true for them by the time any command can see it.

The TUI reads and writes the same labels. It opens on the project the CLI would
default to; `p`/`P` cycle the switcher through `all` and every project on the
board, and the choice is stored in the board's preferences beside the Cancelled
toggle. The project pill leads each card's label row, and the card editor
carries a mandatory Project field, defaulted to the switcher's scope, that
refuses to save a card without exactly one project.

Tasks have stable per-board sequence numbers such as `#12`; bare `12` and a
unique UUID prefix also work. `cancel` is reversible. `rm --yes` permanently
deletes a Cancelled task. Moving to Done is refused while checklist items or
blockers remain unless `--force` is explicit.

Every task command accepts `--data` and `--json`. Set `KB_SERVER` to use the
same verbs against an optional HTTP server:

```sh
export KB_SERVER=http://127.0.0.1:8080
export KB_SERVER_TOKEN=shared-secret
kb list
```

`users` is local-only because one authenticated API identity exposes one board.

## MCP server

`kb mcp` serves the local board over MCP stdio. It opens SQLite directly; no
HTTP server is required.

```sh
kb mcp --data ~/.local/share/kb
```

Example configuration:

```toml
[mcp_servers.kb]
command = "kb"
args = ["mcp"]
```

The server exposes task operations plus board and workflow context resources.
Writes use the same store invariants as the TUI and CLI.

## Optional HTTP API

Start the API explicitly:

```sh
kb serve --port 8080 --data ~/.local/share/kb
kb serve --help
```

`kb serve` owns `--port`, `--data`, and `--log`. Environment defaults are
`KB_PORT`, `KB_DATA`, and `KB_LOG_FILE`.

The server is API-only. All non-API paths, unknown API paths, and wrong methods
return 404. It keeps the existing API, including `/api/config`, for remote
clients:

```text
GET    /api/health
GET    /api/config
GET    /api/board
PUT    /api/board
GET    /api/tasks
POST   /api/tasks
GET    /api/tasks/{ref}
PATCH  /api/tasks/{ref}
DELETE /api/tasks/{ref}
GET    /api/tasks/{ref}/comments
POST   /api/tasks/{ref}/comments
DELETE /api/comments/{id}
POST   /api/links
DELETE /api/links
GET    /api/labels
GET    /api/similar
POST   /api/tombstones
GET    /api/settings
PUT    /api/settings
POST   /api/ai/test
POST   /api/ai/story
POST   /api/ai/stories
POST   /api/ai/run-skill
POST   /api/import/preview
POST   /api/import/links
POST   /api/import/provenance
POST   /api/import/drift
POST   /api/import/drift/accept
GET    /api/integrations
PUT    /api/integrations/{name}
DELETE /api/integrations/{name}
POST   /api/integrations/{name}/test
```

Request bodies are capped at 1 MiB. Mutation routes require their documented
JSON or Markdown content type. The server preserves conditional-write,
idempotency, auth, Host, Origin, CSRF, redirect, timeout, import, and logging
contracts from the previous release.

### Binding and authentication

Auth mode is selected at startup:

1. `KB_AZURE_TENANT_ID` plus `KB_AZURE_CLIENT_ID`: Entra bearer tokens. The
   immutable `oid` claim is the board owner.
2. `KB_TOKEN`: shared bearer token. `X-KB-User` selects the board.
3. Neither: open mode. `X-KB-User` or `default` selects the board.

Open mode binds to `127.0.0.1` by default. `KB_BIND` explicitly changes the
bind address. Loopback Host headers are always accepted; add real deployment
hosts with `KB_ALLOWED_HOSTS`. Token and Entra modes may bind externally, but
TLS termination remains the operator's responsibility.

Do not expose open mode to an untrusted network. Host pinning, same-origin
checks, strict content types, bounded bodies, safe redirects, and server
timeouts remain enabled, but they are not authentication.

### systemd example

```ini
[Unit]
Description=kb API server
After=network.target

[Service]
Type=simple
User=kb
Group=kb
Environment=KB_BIND=127.0.0.1
ExecStart=/usr/local/bin/kb serve --port 8080 --data /var/lib/kb
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/kb

[Install]
WantedBy=multi-user.target
```

If `--log` is used, give the service write access to its parent directory. The
file is created with mode `0600` and appended to. Request logs never include
headers or tokens.

## AI, skills, and forge access

AI and forge settings are managed in the TUI and stored in SQLite. The shared
AI runner is used directly by the TUI and by thin HTTP handlers. Draft and ADR
split operations are read-only until the user reviews and saves results.

Outbound requests reject private, loopback, link-local, and metadata addresses
by default, including after DNS resolution and redirects. Controlled local
development can opt in:

- `KB_AI_ALLOW_PRIVATE=1` for a local AI endpoint;
- `KB_FORGE_ALLOW_PRIVATE=host1,host2` for selected forge hosts, or `1` for all;
- `KB_LINK_ALLOW_PRIVATE=host1,host2` for links fetched by skills, or `1` for
  all.

These are separate trust decisions. Enabling one does not weaken the others.
Stored provider and forge secrets are write-only through the API and are never
returned in settings responses.

## Environment reference

| Variable | Used by | Meaning |
| --- | --- | --- |
| `KB_DATA` | all local modes, serve | Data directory |
| `KB_SECRET` | all store users | Encryption secret override |
| `KB_PROJECT` | task CLI | Active project, overriding `kb project use` |
| `KB_SERVER` | task CLI | Optional remote API base URL |
| `KB_SERVER_TOKEN` | task CLI | Bearer token for remote mode |
| `KB_PORT` | serve | Listen port, default `8080` |
| `KB_BIND` | serve | Bind address |
| `KB_LOG_FILE` | serve | Append-only log file |
| `KB_TOKEN` | serve | Shared bearer token |
| `KB_AZURE_TENANT_ID` | serve | Entra tenant |
| `KB_AZURE_CLIENT_ID` | serve | Entra audience/client ID |
| `KB_ALLOWED_HOSTS` | serve | Extra accepted Host values |
| `KB_AI_ALLOW_PRIVATE` | AI runner | Permit private AI endpoints |
| `KB_FORGE_ALLOW_PRIVATE` | forge service | Permit private forge hosts |
| `KB_LINK_ALLOW_PRIVATE` | skills runner | Permit private fetched links |

## Development and release

The primary local gates are:

```sh
sh scripts/check-go-checkers.test.sh
sh scripts/check-go-coverage.sh
go test -race ./... -count=1
go vet ./...
sh scripts/check-go-format.sh
node scripts/ci/test_ci_monitor.cjs
```

The CI monitor test uses only Node built-ins. It remains because it validates
the repository's workflow monitor; it is not a frontend dependency.

Release builds are Go-only and start from a clean plain clone. The v1.0.0
release notes document the breaking command change and the v0.14.2 upgrade:

```text
docs/releases/v1.0.0.md
```

The local dry run creates a temporary annotated tag, builds and inspects all
five binaries, verifies their checksums, and exercises the native binary. It
then removes the temporary tag and proves that HEAD, the tree, the index, and
status did not change. Release provenance is pinned to the exact Go directive;
select Go 1.25.8 even when a newer compiler is installed:

```sh
GOTOOLCHAIN=go1.25.8 bash scripts/release.sh v1.0.0 docs/releases/v1.0.0.md --dry-run
```

The release tag points directly at the clean source commit. There is no
generated `dist` tree, synthetic release commit, destructive checkout reset,
or retagging. A failed published release is corrected with the next patch.

## License

See the repository metadata and dependency licenses for terms.
