# kb web: AI endpoints

The AI surface of the local HTTP API (see `docs/web-api.md` for the boundary,
request-safety and error conventions, all of which apply unchanged). It lives
in `internal/webui/ai.go` and registers itself through the `featureRoutes`
hook.

## Principles

- **Review before create.** No AI endpoint writes to the store. A run returns
  proposed cards; the UI shows them, the user edits or discards them, and only
  then does the UI `POST /api/tasks` for each accepted draft. This is the same
  contract the TUI overlays enforce (DESIGN principle 6).
- **Read-only scope.** Every run uses `ai.ScopeReadOnly`, so the model gets
  `propose_card`, `find_similar`, `list_tasks`, `get_task` and `load_skill`
  and never `update_task` or `fetch_link`. The web API cannot ask the model to
  mutate a card or fetch a URL.
- **No new outbound paths.** Requests go through `internal/ai`'s own guarded
  client: 60 s per round trip, SSRF address guard (`KB_AI_ALLOW_PRIVATE`),
  same-host redirect policy, 1 MiB response cap, `skillBudget` token clamp.
  The runner is built per request from `store.AISettings` + `store.AIKey`; the
  API key is never read by `internal/webui` and never appears in a response.
- **Skills directory** is `<dataDir>/skills`, matching `kb tui`
  (`internal/tui/run.go`). An empty data dir means built-in skills only.

## Endpoints

| Method | Path              | Body / query                                    | Response                                                     |
| ------ | ----------------- | ----------------------------------------------- | ------------------------------------------------------------ |
| GET    | `/api/ai/status`  |                                                 | `{"configured": bool, "baseURL": "…", "model": "…", "hasKey": bool}` |
| GET    | `/api/ai/skills`  |                                                 | `{"skills": [{"name", "description"}…]}` (built-ins plus operator overrides) |
| POST   | `/api/ai/draft`   | `{"prompt": "…", "card"?: {title, desc, prio, due, effort, tags, checks}}` | `{"card": draft, "commentary"?: "…", "partial"?: bool}` |
| POST   | `/api/ai/split`   | `{"text": "…", "max"?: 1-20}`                   | `{"cards": [draft…], "commentary"?: "…", "partial"?: bool}`  |

`configured` is true when a base URL is stored; the key itself is never
returned, only `hasKey`.

`POST /api/ai/draft` runs the `story-draft` skill for exactly one card. With
no `card` the prompt is packed as *"Create a new kanban card for this
request"*; with a `card` it becomes *"Update the kanban card according to this
request"* plus a `Current card JSON:` block — byte-for-byte the packing
`internal/tui/cardeditor` uses, so the same skill sees the same input on both
surfaces. Output budget 4096 tokens.

`POST /api/ai/split` runs the `adr-split` skill over one ADR or design
document. `max` is the proposed-story cap, normalised by
`ai.NormalizeStoryCount` (0 or absent → 8, clamped to 1-20). Output budget
8192 tokens. Documents over 64 KiB are refused with 413, matching the split
overlay's `maxADRBytes`.

`commentary` is the model's closing prose — the rationale to show above the
review list. `partial` is true when the run hit a budget with cards already
collected: the cards are real, the set is not complete.

## Wire type

`draft` carries the `task` wire type's field names minus everything the store
assigns (`id`, `seq`, `status`, `position`, `createdAt`, `movedAt`), because a
draft is not a task until the UI posts it:

```json
{
  "title": "…", "emoji": "🚀", "desc": "markdown", "prio": 2,
  "due": "YYYY-MM-DD", "effort": "S|M|L", "tags": ["web"],
  "checks": [{"text": "…", "done": false}], "source": 1
}
```

Empty strings, empty lists and `false` are omitted except `title` and `prio`.
`source` appears only for skills that number their inputs. The fields map
1:1 onto a `POST /api/tasks` body, so an accepted draft is posted as-is (add
`project` there if the UI offers a project picker).

## Errors

Standard shape (`{"error": "…"}`) with the codes from `docs/web-api.md`, plus:

| Situation                                                    | Code |
| ------------------------------------------------------------ | ---- |
| Malformed JSON, blank `prompt`, blank `text`                  | 400  |
| Document over 64 KiB (`/split`)                               | 413  |
| AI not configured (no base URL stored)                        | 409  |
| Upstream failure, unknown/broken skill, model returned nothing | 502  |
| Request outlived its deadline                                 | 504  |

A 409 body additionally carries `"aiUnconfigured": true` so the UI can send the
user to settings instead of showing a generic failure. The configuration check
runs before any outbound work.

502 bodies carry the `internal/ai` package's own message verbatim
(`the AI endpoint rejected the API key (HTTP 401) - check the key`,
`the AI endpoint response exceeded the 1 MiB limit`, …). Those messages are
already written for a human and already keep upstream detail out, so the UI
shows them as-is; a 502 never proves the endpoint is reachable.

## TUI parity

| TUI action                                   | Call                                                        | Web endpoint |
| -------------------------------------------- | ----------------------------------------------------------- | ------------ |
| Card editor `ai-draft` field, new card       | `RunSkill(ScopeReadOnly, "story-draft", …, 1, 4096)`        | `POST /api/ai/draft` |
| Card editor `ai-draft` field, editing a card | same, prompt packed with `Current card JSON:`               | `POST /api/ai/draft` with `card` |
| ADR split overlay (`a`), pasted document     | `RunSkill(ScopeReadOnly, "adr-split", text, max, 8192)`     | `POST /api/ai/split` |
| ADR split overlay, file path                 | reads the file, then the same run                           | not exposed — the browser reads the file and posts its `text` |
| Settings AI connection test                  | `Runner.Probe`                                              | settings API (`docs/web-api-settings.md`) |
| Issue import preview (`import-transform`)    | `forge.Preview` → `RunSkill(…, "import-transform", …)`      | forge API (`docs/web-api-forge.md`) |
| Card detail drift summary                    | `forge` → `Runner.RunText`                                  | forge API (`docs/web-api-forge.md`) |

The two forge-driven actions run behind `internal/forge`, which owns the
outbound issue fetch as well as the model call; they are exposed by the forge
endpoints rather than duplicated here. Reading an ADR from a local path stays a
TUI-only affordance: the server never opens a client-named file.
