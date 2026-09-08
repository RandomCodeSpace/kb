# kb web: forge import API

The forge endpoints give the browser the same issue import the TUI overlay
runs (`internal/tui/issueimport`) and the same upstream drift check the card
detail pane runs (`internal/tui/carddetail`). They live in
`internal/webui/forge.go` and follow every rule in
[docs/web-api.md](web-api.md): same-host `Origin`, JSON `Content-Type`, 1 MiB
body cap, `{"error": "…"}` on every non-2xx.

## Credentials

A forge personal access token is **never** sent to these endpoints. Tokens are
configured once per named source through the settings surface
(`PUT /api/settings/forge/{name}`), sealed in the store, and decrypted only
inside `internal/forge` at fetch time. Requests here name a `source`, so there
is no token to log, echo, or leak. The picker reads its sources from
`GET /api/settings/forge`; this file adds no second list.

Only configured sources are reachable: `internal/forge` parses a reference
against the stored base URLs and refuses any other host, and the HTTP client
refuses loopback, private and link-local addresses unless
`KB_FORGE_ALLOW_PRIVATE` says otherwise.

## Endpoints

| Method | Path                        | Body / query                                                                     | Response                                    |
| ------ | --------------------------- | -------------------------------------------------------------------------------- | ------------------------------------------- |
| POST   | `/api/forge/preview`        | `{source, ref, max?}`                                                            | `preview`                                   |
| POST   | `/api/forge/import`         | `{source, task: {…}, link: {externalKey, link, url, title, baseline}}`           | 201 `task`                                  |
| GET    | `/api/forge/provenance`     | `?link=github%23123`                                                             | `{"links": [importLink…]}`                  |
| POST   | `/api/forge/drift/check`    | `{source, externalKey}`                                                          | `drift`                                     |
| POST   | `/api/forge/drift/accept`   | `{source, externalKey, revision}`                                                | `{"baselineAt": "RFC3339Nano"}`             |

`source` is a configured source name. `ref` is an issue URL, a milestone URL,
a project URL, or a bare `owner/repo` (GitHub sources only). `max` is the
overlay's count stepper: omitted or `<= 0` means 8, anything above 20 is
clamped to 20.

## Wire types

`preview`:

```json
{
  "kind": "project|milestone|issue",
  "totalHint": 42, "fetched": 8, "truncated": false,
  "note": "rate limited — partial results (3 of 42)",
  "drafts": [draft…]
}
```

`draft` — the task wire field names plus the provenance the import step must
hand back unchanged:

```json
{
  "title": "…", "emoji": "", "desc": "markdown", "prio": 2,
  "due": "YYYY-MM-DD", "effort": "S|M|L",
  "tags": ["…", "link::github#123", "import::github:primary@https://host/owner/repo#123"],
  "checks": [{"text": "…", "done": false}],
  "source": "primary",
  "link": "github#123",
  "externalKey": "github:primary@https://host/owner/repo#123",
  "url": "https://host/owner/repo/issues/123",
  "baseline": {"title": "…", "hash": "sha256", "excerpt": "…", "at": "RFC3339Nano"},
  "duplicate": {"id": "uuid", "title": "…", "via": "link|similar"}
}
```

`duplicate` is the overlay's dedupe marker and is omitted when there is none.
`via: "link"` means a card already carries this item's `import::` tag — the
overlay starts those rows unticked. `via: "similar"` is a fuzzy title hit from
`store.SearchSimilar`. The marker is **advisory**: the import endpoint writes
whatever card it is handed, exactly as the overlay does when a reviewer
re-ticks a duplicate row.

`importLink`: `{"source", "kind", "externalKey", "link", "url", "title"}`

`drift`:

```json
{
  "state": "unchanged|drifted|baseline_recorded",
  "link": "github#123", "url": "https://…",
  "titleChanged": true,
  "upstreamTitle": "…", "baselineTitle": "…", "baselineAt": "RFC3339Nano",
  "checkedAt": "RFC3339Nano",
  "summary": "plain-text change summary",
  "revision": "sha256 of the accepted snapshot"
}
```

`revision` is only present on `drifted` and is what `/drift/accept`
compare-and-swaps on, so two clients cannot silently overwrite each other's
accepted baseline.

## Import semantics

`POST /api/forge/import` commits **one** reviewed card, matching the overlay's
one-write-per-row queue. The card, its `import_links` provenance row, and the
upstream baseline captured by the preview are written in a single store
transaction (`store.AddTaskWithImportLink`), so a card can never exist without
its provenance.

The `task` object takes the same fields as `POST /api/tasks`
(`title, desc, status?, prio?, due?, effort?, tags?, checks?, emoji?, project?`)
and is validated the same way. Status defaults to `todo`. The project is
stamped at write time from `cliapp.ProjectTags` — the request's `project` or
`project::` tag; there is no ambient default, so a card without one is refused
with 400 `no project given`, exactly as the overlay resolves it per card.

The `link` block must be the draft's own `externalKey`, `link`, `url`, `title`
and `baseline`. The service re-derives the provenance from the configured
source and refuses a mismatch rather than trusting the client's copy.

## TUI parity

| TUI import step (`internal/tui/issueimport`)          | Web equivalent                                              |
| ------------------------------------------------------ | ----------------------------------------------------------- |
| Open overlay, source picker (`Service.Sources`)         | `GET /api/settings/forge` (settings surface)                 |
| Reference field: issue/milestone/project URL or `owner/repo` | `ref` in `POST /api/forge/preview`                      |
| Count stepper, 1–20, default 8                          | `max` in `POST /api/forge/preview` (clamped server-side)     |
| Fetch + draft (`Service.Preview`, 25 s fetch deadline)  | `POST /api/forge/preview` → `preview`                        |
| List with dedupe markers, exact duplicates start unticked | `draft.duplicate.via` = `link` (exact) or `similar` (fuzzy) |
| Tick/untick rows, review fields                         | client-side; the reviewed card is the `task` object          |
| Write queue, one card per write (`Service.CreateTask`)  | `POST /api/forge/import`, once per selected card             |
| Card detail: list an imported card's sources (`Service.Provenance`) | `GET /api/forge/provenance?link=…`, one call per `link::` tag on the card |
| Card detail: check upstream drift (`Service.CheckDrift`) | `POST /api/forge/drift/check`                               |
| Card detail: accept the new baseline (`Service.AcceptDrift`) | `POST /api/forge/drift/accept`                          |

Not exposed: forge source CRUD and the connection test, which belong to the
settings surface; and the overlay's cancellation, progress meter and scroll
state, which are client concerns with no server contract. There is no per-task
provenance lookup because the store has none — the card's `link::` tags are the
index, and the detail pane reads them the same way.

## Errors

| Situation                                                       | Code |
| ---------------------------------------------------------------- | ---- |
| Malformed JSON, empty `ref`/`externalKey`, invalid reference, invalid card field, invalid revision, provenance that does not match the source | 400 |
| Unknown source name; unknown import link or external key         | 404  |
| Baseline changed again between check and accept                  | 409  |
| Storage failure                                                  | 500  |
| Upstream forge refused, rate limited, or was unreachable         | 502  |
| Upstream fetch exceeded its deadline                             | 504  |

Error messages come from the categorized `*forge.Error` and `*ai.Error` values,
which are written to be caller-safe. An uncategorized failure is reported as
`502 forge request failed` without echoing its text, so upstream detail cannot
leak into the browser.

## Assistant dependency

`POST /api/forge/preview` transforms the fetched issues into card proposals by
running the embedded `import-transform` skill, exactly as the overlay does.
That requires a configured AI endpoint (`PUT /api/settings/ai`); without one
the preview answers with the AI package's own categorized error (400 or 502).
There is no raw-issue preview mode — the TUI has none either.
