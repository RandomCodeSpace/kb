# kb

A personal kanban board that stays plain text.

**kb** is a rich-card kanban web app (PWA-style single-page app) served by a
single-binary Go server. The frontend is Vite + React + TypeScript; the server
embeds the built SPA and exposes a tiny markdown-file API — your whole board is
one human-readable `.md` file on disk, one file per user.

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
- **Plain-text native** — the board is markdown. Import/export from the UI;
  round-trip is lossless for well-formed files.
- **Single binary server** — the Go server embeds the SPA, serves it, and
  syncs boards as markdown files. No database.
- **Multi-user by identity, not by accounts** — each identity maps to its own
  board file. Identity comes from Azure Entra ID or a manual unique id (see
  [Identity modes](#identity-modes)).
- **Sync is last-write-wins** — simple, predictable, no merge machinery.

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

The binary serves the embedded SPA and the API on the given port. Boards are
stored as markdown files under the data directory.

> The `dist/` directory (the built SPA) is committed to the repository on
> purpose: `go install` builds from the module source, and the server embeds
> `dist/` at compile time. Without it in the tree, `go install` would produce a
> server with no frontend.

## API

| Method | Path          | Auth | Behavior                                                        |
| ------ | ------------- | ---- | --------------------------------------------------------------- |
| GET    | `/api/health` | none | `200` JSON `{"ok":true}`                                        |
| GET    | `/api/board`  | yes  | `200` `text/markdown` (your board), `404` if none saved yet     |
| PUT    | `/api/board`  | yes  | Body is `text/markdown`; replaces your board; responds `204`    |
| GET    | `/*`          | none | Embedded SPA static files                                       |

Auth applies to `/api/board` only — never to `/api/health` or static files.

## The board format

A board is a markdown file. Example:

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

## Identity modes

The server picks its mode from environment variables at startup:

1. **Azure Entra ID** — set `KB_AZURE_TENANT_ID` and `KB_AZURE_CLIENT_ID`.
   Requests to `/api/board` must carry `Authorization: Bearer <Entra ID token>`
   (the SPA's ID token). The server validates the token itself — RS256
   signature against the tenant's JWKS, issuer, audience, and expiry — and
   takes the user identity from the token's immutable `oid` claim (tokens
   without `oid` are rejected; `email`/`preferred_username` are mutable and
   reassignable, so they are display-only and never select the board file).
   **Token decode/verify only: the server never calls Microsoft Graph or any
   other Microsoft API.**
2. **Shared token** — else, if `KB_TOKEN` is set, requests must carry
   `Authorization: Bearer <KB_TOKEN>` (compared in constant time). The user
   identity is the required, non-empty `X-KB-User` header — a manual unique id
   you choose (e.g. your email or a handle).
3. **Open / localhost** — else, no auth. Identity is the `X-KB-User` header if
   present, otherwise `default`. Only use this on a trusted local machine.

### How identity maps to files

Every identity owns exactly one board file:

```
<dataDir>/<sanitize(identity)>.md
```

`sanitize` lowercases the identity and rejects any character outside
`[a-z0-9._@-]` (rejected, not substituted — substitution would collapse
distinct identities such as `anna#lee@corp.com` and `anna-lee@corp.com` onto
the same board file). Empty names and names starting with `.` are also
rejected, and path separators are outside the allowed set — so an identity
can never traverse outside the data directory. In Entra mode the identity is
the account's `oid` GUID, so Entra boards are keyed per directory object, not
per email; token-mode boards are keyed by the manual id you choose.

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

| Variable                  | Kind               | Default   | Purpose                                                                 |
| ------------------------- | ------------------ | --------- | ----------------------------------------------------------------------- |
| `KB_PORT`                 | server, runtime    | `8080`    | Listen port (`--port` flag overrides)                                   |
| `KB_DATA`                 | server, runtime    | `./data`  | Directory for per-user board files (`--data` flag overrides)            |
| `KB_TOKEN`                | server, runtime    | unset     | Shared bearer token; enables token mode when Azure vars are unset       |
| `KB_AZURE_TENANT_ID`      | server, runtime    | unset     | Entra tenant ID; with client ID, enables Entra mode                     |
| `KB_AZURE_CLIENT_ID`      | server, runtime    | unset     | Entra app (client) ID; expected token audience                          |
| `VITE_AZURE_TENANT_ID` | frontend, build    | unset     | Baked into the SPA at `npm run build`; enables the MSAL sign-in flow    |
| `VITE_AZURE_CLIENT_ID` | frontend, build    | unset     | Baked into the SPA at `npm run build`; MSAL client ID                   |

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
correct ownership. If you prefer a fixed account, replace `DynamicUser=yes`
with `User=kb` and create the user and data directory yourself. Terminate TLS
in front of the binary (Caddy, nginx, etc.) — kb itself speaks plain HTTP.

## Security notes

**What the server validates**

- Entra mode: RS256 signature against the tenant JWKS
  (`https://login.microsoftonline.com/<tenant>/discovery/v2.0/keys`), issuer
  `https://login.microsoftonline.com/<tenant>/v2.0`, audience equal to the
  client ID, and `exp`/`nbf`. Identity is read from the verified token only.
- Token mode: constant-time comparison of the bearer token (no timing oracle),
  and a required non-empty `X-KB-User`.
- All modes: the identity-to-filename sanitizer guarantees board files stay
  inside the data directory — no path traversal, no dotfiles.

**What it does not do**

- No Microsoft Graph or network calls to Microsoft beyond fetching the JWKS.
- No sessions, refresh handling, or token revocation checks — a token is good
  until it expires.
- No authorization beyond identity: any valid identity reads and writes its
  own board, nothing else; there are no roles or sharing.
- No TLS and no rate limiting — put it behind a reverse proxy.
- No validation of board content: the body of `PUT /api/board` is stored as-is.

**Sync semantics**

Sync is last-write-wins on the whole board file. `PUT /api/board` replaces the
file; there is no merging, version check, or conflict detection. If the same
identity writes from two places, the later write silently wins. This is a
deliberate v1.1 trade-off for a personal, single-writer-per-user tool — keep a
copy via markdown export if that matters to you.
