# kb web: local HTTP API

`kb web [--data DIR] [--addr 127.0.0.1:0] [--no-open] [--unsafe-listen]`
serves the board to a browser on the local machine. It is an optional,
explicitly started listener: nothing else in `kb` opens a port.

## Boundary

- Package: `internal/webui`. Entry points:
  - `func Run(opts Options, stdout, stderr io.Writer) error` with
    `Options{DataDir, User, Version, Addr string; OpenBrowser, AllowRemote bool}`
    opens the store via `cliapp.OpenLocalStore`, listens on `Addr`, prints
    `kb web: serving http://127.0.0.1:PORT` to stdout, optionally opens the
    browser, and blocks until SIGINT/SIGTERM. `web_dispatch.go` maps
    `--no-open` to `OpenBrowser=false` and `--unsafe-listen` to
    `AllowRemote=true`.
  - `func NewHandler(st *store.Store, user, dataDir, version string) http.Handler`
    is the testable core: JSON API under `/api/`, embedded static UI at `/`.
- Default bind address is `127.0.0.1:0` (random loopback port). A non-loopback
  `--addr` is refused unless `--unsafe-listen` is also given.
- Board owner is always `"default"` (see `defaultBoardUser` in dispatch.go).
- Project resolution reuses `cliapp.ActiveProject(dataDir)`; `project.Of(tags)`
  derives a task's project; `project.Ensure(tags, name)` stamps one.
- Done guard reuses `store.CompletionWarning` + `store.NewCompletionBlockedError`
  exactly as `internal/cliapp/local.go` does (`UpdateAndMoveTask` with a guard
  closure). A refused move returns HTTP 409.

## Request safety

- Mutating methods (POST, PATCH, PUT, DELETE) require either no `Origin`
  header or an `Origin` whose host equals the request `Host`. Otherwise 403.
- Mutating requests must send `Content-Type: application/json`. Otherwise 415.
- Request bodies are capped at 1 MiB.
- No CORS headers are ever emitted.
- Responses set `Cache-Control: no-store` on `/api/`, and
  `X-Content-Type-Options: nosniff` everywhere.

## Error shape

Every non-2xx response is `{"error": "<message>"}`. Status codes:

| Situation                                | Code |
| ---------------------------------------- | ---- |
| Malformed JSON, invalid field, bad ref   | 400  |
| Unknown task / comment                   | 404  |
| Completion guard refused a move to done  | 409  |
| Unsupported method                       | 405  |

A 409 body additionally carries `"completionBlocked": true` so the UI can offer
a force retry.

## Wire types

`task`:

```json
{
  "id": "uuid", "seq": 12, "emoji": "", "title": "…", "desc": "markdown",
  "status": "todo|doing|done|cancelled", "blocked": false,
  "prio": 1, "due": "YYYY-MM-DD", "effort": "S|M|L", "tags": ["a", "type::bug"],
  "checks": [{"text": "…", "done": false}],
  "project": "personal", "position": 0,
  "createdAt": "RFC3339", "movedAt": "RFC3339"
}
```

Field rules mirror `internal/mcpserv` `taskJSON`: omit empty strings and false
booleans except `title`, `status`, `prio`, `project`, `position`, timestamps.
`project` is `project.Of(tags)`; the `project::` label stays inside `tags` too.

`comment`: `{"id": 3, "taskId": "uuid", "taskSeq": 12, "author": "default", "body": "…", "createdAt": "RFC3339"}`

`links`: `{"blocks": [task…], "blockedBy": [task…]}`

## Endpoints

| Method | Path                          | Body / query                                                                                            | Response                                        |
| ------ | ----------------------------- | ------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| GET    | `/api/meta`                   |                                                                                                         | `{"version", "activeProject", "projects": [..], "labels": [..], "statuses": [..]}` |
| GET    | `/api/tasks`                  | `?status=&q=&tag=a&tag=b&project=`                                                                      | `{"tasks": [task…]}` in board order             |
| POST   | `/api/tasks`                  | `{title, desc?, status?, blocked?, prio?, due?, effort?, tags?, checks?, emoji?, project?}`             | 201 `task`                                      |
| GET    | `/api/tasks/{ref}`            |                                                                                                         | `{"task", "comments": [..], "links": {..}}`     |
| PATCH  | `/api/tasks/{ref}`            | `{title?, desc?, blocked?, prio?, due?, effort?, tags?, checks?, emoji?, project?, status?, index?, force?}` — pointer semantics, omitted = unchanged | `task` |
| POST   | `/api/tasks/{ref}/move`       | `{status, index?, force?}`                                                                              | `task` (409 when guard refuses)                 |
| POST   | `/api/tasks/{ref}/cancel`     | `{reason?}`                                                                                             | `task`                                          |
| POST   | `/api/tasks/{ref}/restore`    |                                                                                                         | `task` (moves back to todo)                     |
| DELETE | `/api/tasks/{ref}`            |                                                                                                         | `task` (permanent; only allowed when cancelled → use `store.DeleteCancelledTask`) |
| GET    | `/api/tasks/{ref}/comments`   |                                                                                                         | `{"comments": [comment…]}`                      |
| POST   | `/api/tasks/{ref}/comments`   | `{body}`                                                                                                | 201 `comment`                                   |
| DELETE | `/api/comments/{id}`          |                                                                                                         | `comment`                                       |
| POST   | `/api/links`                  | `{blocker, blocked}` (refs)                                                                             | 201 `{"blocker": task, "blocked": task}`        |
| DELETE | `/api/links`                  | `{a, b}` (refs)                                                                                         | 204                                             |
| GET    | `/api/similar`                | `?q=&limit=`                                                                                            | `{"items": [{id,title,status,via,link}…]}`      |
| GET    | `/api/labels`                 |                                                                                                         | `{"labels": [..]}`                              |

`{ref}` accepts what the CLI accepts: `12`, `#12`, a UUID, or a unique UUID
prefix (the store's `resolveID`). `index` on move/patch is the 0-based target
position inside the destination column (`UpdateAndMoveTask` index argument).

`projects` in `/api/meta` are the distinct `project::` label values present on
the board plus the active project, sorted, `inbox` last.

Restore semantics: `POST /restore` is `UpdateAndMoveTask(user, ref, {}, &todo, nil, nil)`,
matching `kb restore`. Cancel semantics: `store.CancelTask(user, ref, reason)`.

## Static UI

Embedded from `internal/webui/static/` via `embed.FS`:
`index.html`, `app.css`, `app.js`. No build step, no external network fetches,
no CDN. Served at `/` with `Content-Type` by extension. Unknown paths under `/`
fall back to `index.html`; `/api/*` never falls back.

## Change detection

`GET /api/tasks` sets an `ETag` computed from the SHA-256 of the response body.
Clients poll with `If-None-Match` and receive 304 when nothing changed. The UI
polls every 2 seconds while the tab is visible.
