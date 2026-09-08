# kb web: board and project endpoints

The board-level half of the local HTTP API — the operations the TUI performs on
a whole board rather than on one card. Read `docs/web-api.md` first: the
boundary, request safety, error shape and `{ref}` grammar described there apply
unchanged. Owner is always `s.user` (`"default"`).

Implementation: `internal/webui/board.go`, registered through the `featureRoutes`
hook so the core route table stays put.

## Endpoints

| Method | Path                            | Body / query           | Response                                                              |
| ------ | ------------------------------- | ---------------------- | --------------------------------------------------------------------- |
| GET    | `/api/actions`                  |                        | `{"actions": [action…]}` — the TUI keyboard registry                   |
| GET    | `/api/projects`                 |                        | `{"active": "web", "projects": [project…]}`                            |
| PUT    | `/api/projects/active`          | `{name}`               | `{"active": "web"}` (writes the CLI's `state.json`)                    |
| GET    | `/api/tasks/{ref}/tombstone`    |                        | `tombstone` (404 when the card carries no recorded reason)             |
| GET    | `/api/by-link`                  | `?link=`               | `{"items": [similar…]}` — cards carrying `link` as one whole tag       |
| GET    | `/api/shipped`                  | `?date=YYYY-MM-DD`     | `{"date", "count", "seqs": [..]}` — cards that reached done that day   |

Status codes follow the shared table: 400 for a malformed body or an invalid
field, 404 for an unknown task or a missing tombstone, 405 for an unsupported
method (with `Allow`), 500 when the store or the CLI state file cannot be read.
`PUT /api/projects/active` is the only mutating call here, so it is the only one
subject to the `Origin` and `Content-Type` rules.

`/api/by-link` is deliberately not `/api/tasks/by-link`: a literal path under
`/api/tasks/` and the method-less 405 catch-all the route table registers for
`GET /api/tasks/{ref}` are an ambiguous pair that `net/http` refuses to serve.

## Wire types

`action` — one row of the TUI's keyboard registry:

```json
{
  "id": "shipCard", "group": "act", "key": "t",
  "hint": "t", "name": "ship card", "enabled": true,
  "endpoint": "POST /api/tasks/{ref}/move"
}
```

`id` is stable; `key`, `hint` and `name` are copies of
`internal/tui/action/action.go`'s `registry`, in declaration order, so the web
palette lists exactly what `ctrl+k` lists in the terminal. The copy exists
because that package depends on bubbletea and the HTTP server has no business
linking a terminal UI framework to spell out a key table;
`TestActionsMirrorTUIRegistry` compares the two row by row, so a rebind or a
reword in the registry fails this package's tests instead of drifting.

`enabled` reports whether *this API* can perform the row, not whether the TUI
can — the web's answer to the registry's feature gates, and true for every row
today. `endpoint` is the call the web UI makes for it and is absent for the
rows that are pure client-side navigation. A web palette should list what the TUI's
`action.Listed` lists: the enabled rows outside the `dismiss` group, minus
`openPalette`.

`project`:

```json
{"name": "web", "counts": {"todo": 3, "doing": 1, "done": 12, "cancelled": 2}}
```

Counts include cancelled cards, as `kb project list` does. Ordering matches
`/api/meta`'s `projects`: every project label on the board plus the active one,
sorted, `inbox` last. A card carrying two `project::` labels — only possible on
a board a foreign writer touched — counts under both.

`tombstone`: `{"taskId": "uuid", "reason": "duplicate of #1", "killedAt": "RFC3339"}`

## Active project

`PUT /api/projects/active` is the web's `kb project use`: the name is validated
with `project.ValidateName` (trimmed, no whitespace, no leading `#`, no `::`)
and stored through `cliapp.SetActiveProject`, which writes the same
`<dataDir>/state.json` the CLI writes, atomically. `kb project current`, the
TUI's opening scope and `/api/projects` therefore agree.

Resolution order is the CLI's and is unchanged: `KB_PROJECT` beats the stored
value. With `KB_PROJECT` set in the server's environment, a `PUT` still stores
the name but `/api/projects` keeps reporting the environment's project.

A project with no cards has no `project::` label anywhere, so it exists only as
the active selection — which is exactly how the TUI treats it: the switcher's
list is every project on the board plus `m.activeProject`, so a freshly named
project is cycleable and is the default for the next card created, and it
disappears again if the selection moves away before a card is written. There is
therefore no separate "create project" call: `PUT /api/projects/active` with a
new name is the creation.

## Shipped

`GET /api/shipped` counts the cards in `done` whose `MovedAt` falls on the given
local date, defaulting to today. It is the tally behind the TUI's ship
celebration, but it is derived from the board rather than read from the TUI's
preference file: the TUI counts the ships made in one session
(`shipped` in `<dataDir>/.kb-tui/preferences.json`), which is display state the
web keeps for itself. `seqs` is always present, `[]` on a quiet day.

## TUI parity

Every row of `internal/tui/action/action.go` — the one table the help pane and
the `ctrl+k` palette both read — plus the board-level state the TUI keeps
outside it.

| TUI command / key                    | Web                                                        |
| ------------------------------------ | ---------------------------------------------------------- |
| `enter` open card                    | already covered by `GET /api/tasks/{ref}`                   |
| `space` lift or drop card            | already covered by `POST /api/tasks/{ref}/move` (`index`)   |
| `j`/`k` select card                  | client-side selection; no endpoint                          |
| `h`/`l` select column                | client-side selection; no endpoint                          |
| `1-4` jump to column                 | client-side selection; no endpoint                          |
| `/` text filter                      | already covered by `GET /api/tasks?q=`                      |
| `f` label filter                     | already covered by `GET /api/tasks?tag=` + `GET /api/labels`|
| `X` clear filter                     | client-side; an unfiltered `GET /api/tasks`                 |
| `p`/`P` switch project               | `GET /api/projects`, `PUT /api/projects/active`             |
| `ctrl+k` command palette             | `GET /api/actions`                                          |
| `t` ship card                        | already covered by `POST /api/tasks/{ref}/move` (`done`)    |
| `x` cancel card                      | already covered by `POST /api/tasks/{ref}/cancel` (`reason`)|
| `D` permanently delete (armed purge) | already covered by `DELETE /api/tasks/{ref}`                |
| `r` restore card                     | already covered by `POST /api/tasks/{ref}/restore`          |
| `n` new card                         | already covered by `POST /api/tasks`                        |
| `e` edit card                        | already covered by `PATCH /api/tasks/{ref}`                 |
| `s` settings                         | already covered by `GET`/`PUT /api/settings`                |
| `a` split ADR                        | already covered by `POST /api/ai/split`                     |
| `i` import forge issue               | already covered by `POST /api/forge/import`                 |
| `?` / `esc` close help               | client-side; the help pane is `GET /api/actions` rendered   |
| `q` quit                             | not exposed: a browser tab has nothing to quit              |
| cancellation reason under a card     | `GET /api/tasks/{ref}/tombstone`                            |
| "already imported?" link lookup      | `GET /api/by-link`                                          |
| ship celebration's shipped-today count | `GET /api/shipped`                                        |
| board title in the header            | not exposed: no TUI or CLI surface edits it (`board.Board.Title` is render-only), and the only write path, `store.ReplaceBoard`, deletes and reinserts every card |
| board owners / namespaces            | not exposed: `store.Users()` has no TUI surface; `kb web` serves one fixed owner |
| show-cancelled toggle, filter, project scope, shipped record | not exposed: TUI display state in `<dataDir>/.kb-tui/preferences.json`; the web keeps its own in the browser |
| bulk "purge all cancelled"           | not exposed: no such command exists; `D` purges the selected card only |
