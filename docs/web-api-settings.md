# kb web: settings API

The settings endpoints give the browser what the TUI settings pane
(`internal/tui/settings.go`) gives the terminal: the AI endpoint configuration
and the configured forge integrations. Everything in `docs/web-api.md` applies
unchanged — same `{"error": "..."}` shape, same Origin and `Content-Type` rules
on mutating requests, same 1 MiB body cap, same `Cache-Control: no-store`.

Owner is always `"default"` (`s.user`), as everywhere else in `kb web`.

## Secrets

- No endpoint ever returns the AI API key or a forge token, in whole or in
  part. Reads report only `hasKey` / `hasToken`, which is exactly what the TUI
  shows (its secret fields load blank and echo masked).
- Error text is scrubbed the way `safeSettingsError` scrubs it in the TUI: the
  secret the caller just sent is replaced with `[redacted]`, escape sequences
  and control characters are dropped, the message is folded onto one line and
  capped at 160 runes. An upstream that echoes a request body back inside an
  error cannot leak the credential through this API.
- A stored credential never follows a re-pointed endpoint. Changing the base
  URL to a different scheme or host without supplying a secret in the same call
  clears the stored one (`store.SameAIOrigin`), reported as `keyCleared` /
  `tokenCleared`. The connection tests apply the same rule and refuse with 400
  rather than sending a stored credential to a newly named origin.

## Endpoints

| Method | Path                              | Body                                                          | Response                                                     |
| ------ | --------------------------------- | ------------------------------------------------------------- | ------------------------------------------------------------ |
| GET    | `/api/settings/ai`                |                                                               | `{"ai": {"baseURL", "model", "hasKey"}}`                     |
| PUT    | `/api/settings/ai`                | `{baseURL?, model?, apiKey?}` — pointer semantics             | `{"ai": {...}, "keyCleared"?: true}`                          |
| POST   | `/api/settings/ai/test`           | `{baseURL?, model?, apiKey?}` (optional; blank = stored value) | 204, or an error body                                        |
| GET    | `/api/settings/forge`             |                                                               | `{"sources": [{"name", "kind", "baseURL", "hasToken", "createdAt"}]}` |
| PUT    | `/api/settings/forge/{name}`      | `{kind, baseURL?, token?}` — pointer semantics                | `{"source": {...}, "tokenCleared"?: true}`                    |
| DELETE | `/api/settings/forge/{name}`      |                                                               | 204                                                          |
| POST   | `/api/settings/forge/{name}/test` | `{kind?, baseURL?, project?, token?, saved?}` (optional)      | 204, or an error body                                        |

Pointer semantics on the two PUTs: an omitted field is left unchanged, an
explicit empty string clears the stored value. A secret that is only whitespace
counts as a clear, never as a credential. `baseURL` and `model` are trimmed the
way the TUI trims its fields.

`PUT /api/settings/forge/{name}` is an upsert: `{name}` is lower-cased and must
match `[a-z0-9._-]{1,64}`, `kind` is `gitlab` or `github`, and `baseURL` is
required when the source does not exist yet (a bare hostname is accepted and
normalised to `https://host`). `createdAt` is RFC3339 UTC.

`POST /api/settings/forge/{name}/test` sends `saved: true` to let blank fields
fall back to the stored integration, which is what the TUI does for a persisted
row; a draft row sends `saved: false` and is tested entirely from the supplied
values. `project` is probe-only and is never persisted.

`keyCleared` and `tokenCleared` are omitted when false.

## Status codes

| Situation                                                                  | Code |
| -------------------------------------------------------------------------- | ---- |
| Malformed or empty JSON body on a write                                    | 400  |
| Base URL rejected by `ai.ValidateBaseURL`, or by the store's forge rules   | 400  |
| Invalid forge source name or kind, missing base URL on a new source        | 400  |
| Store rejected a write (`PUT`)                                             | 400  |
| Connection test rejected the caller's values (blank model, stored credential against a new origin, unusable URL) | 400 or the AI package's own 4xx |
| Connection test could not reach or understand the endpoint                 | 502  |
| Store could not be read (`GET`)                                            | 500  |
| Unsupported method on a settings path                                      | 405  |

The AI test keeps the status `ai.Error.Code` already assigned (400 for settings
the caller can fix, 502 for the upstream, 422 for a truncated reply), falling
back to 502 for anything else. The forge prober does not carry a code, so its
opaque `connection failed` maps to 502 and every other failure — all of which
name a value the caller supplied — maps to 400.

## TUI parity

| TUI settings pane                     | Web                                                      |
| ------------------------------------- | -------------------------------------------------------- |
| AI "Base URL"                         | `ai.baseURL`                                             |
| AI "Model"                            | `ai.model`                                               |
| AI "API key" / "API key (saved)"      | `apiKey` on write, `ai.hasKey` on read                   |
| AI "Test connection" (20 s timeout)   | `POST /api/settings/ai/test` (same `ai.Runner.Probe`, same 20 s cap) |
| AI "Save AI settings"                 | `PUT /api/settings/ai` (`store.SetAISettings`)           |
| "saved; endpoint changed, re-enter the API key" | `"keyCleared": true`                           |
| Integration "Name" (locked once saved)| `{name}` in the path                                     |
| Integration "Kind" (gitlab/github)    | `kind`                                                   |
| Integration "Base URL"                | `baseURL`                                                |
| Integration "Project"                 | `project`, on the test endpoint only (never persisted)   |
| Integration "Token" / "Token (saved)" | `token` on write, `hasToken` on read                     |
| Integration "Test"                    | `POST /api/settings/forge/{name}/test`                   |
| Integration "Save"                    | `PUT /api/settings/forge/{name}`                         |
| Integration "Remove" (arm, then confirm) | `DELETE /api/settings/forge/{name}` (no arming step)  |
| "saved; endpoint changed, re-enter the token" | `"tokenCleared": true`                            |
| Error row text                        | `{"error": "..."}`, scrubbed identically                 |

### Deviations

- **Clearing a secret.** The TUI cannot: a blank key or token field means
  "keep what is stored". The web API distinguishes an omitted field from an
  explicit `""`, so a caller can clear a key, a token, or a base URL outright.
- **Remove is immediate.** The TUI arms the Remove button and needs a second
  Enter; HTTP has no such state, so `DELETE` removes on the first call.
  Deleting a source that is not there succeeds, as it does in the store.
- **Save is an upsert.** The TUI refuses a new draft whose name collides with
  an existing integration; `PUT /api/settings/forge/{name}` updates the
  existing row instead. The name in the path is authoritative.
- **No key hint.** `GET /api/settings/ai` reports `hasKey` and nothing else —
  not even the last characters of the key. The TUI reveals no part of a stored
  key either, and a hint would be a web-only downgrade.
- **No preferences endpoint.** The only user settings the store holds are the
  AI row and the forge sources. What the TUI calls preferences (show
  cancelled, the active filter, the project scope, the shipped record) lives in
  a JSON file beside the database (`internal/tui/preferences.go`) and is view
  state of one terminal session, so the web API exposes none of it.
- **Write failures are 400.** The store validates forge names, kinds and URLs
  inside the write, so a failed `PUT` is reported as a bad request even in the
  rare case where the cause was storage. Reads have no such ambiguity and
  report 500.
