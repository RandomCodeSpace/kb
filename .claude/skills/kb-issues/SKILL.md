---
name: kb-issues
description: Map GitHub/GitLab issue workflows onto the kb CLI. Opt-in only - use when the user explicitly chooses kb as their issue tracker, typically because they have no GitHub/GitLab access or want issues tracked locally. Never substitute kb for gh/glab issue commands by default; when a repo has a reachable GitHub/GitLab tracker, that tracker stays the default.
---

# kb as a local issue tracker (opt-in)

kb is a local-only kanban board with a CLI. This skill applies only after
the user has chosen kb for issue tracking — it is an alternative for when
GitHub/GitLab is unavailable or unwanted, not a replacement policy. Once
chosen, where a workflow reaches for `gh issue` or `glab issue`, use the
`kb` verbs below instead. State lives in a local SQLite database (default
`~/.local/share/kb`).

## Environment

- `KB_DATA` — data directory override. Local commands always operate on the
  single `default` board; there is no per-user selection.
- MCP-capable agents can run `kb mcp` and use the local board tools over
  stdio instead of shelling out.

## Command mapping

| gh / glab | kb |
|---|---|
| `gh issue create --title T --body B` | `kb add "T" --desc "B"` |
| `gh issue create --label bug` | `kb add "T" --tag bug` (repeat `--tag`) |
| `gh issue list` | `kb list` |
| `gh issue list --json ...` | `kb list --json` (full tasks, fixed shape) |
| `gh issue list --state all` | `kb list --all` (includes cancelled) |
| `gh issue list --search "text"` | `kb list --search "text"` (matches title, description, tags) |
| `gh issue list --label bug --label ui` | `kb list --tag bug --tag ui` (all tags must match) |
| `gh issue view N` | `kb view N` (full task + comments; `--json`) |
| `gh issue edit N --title/--body` | `kb update <id> --title T --desc B` |
| `gh issue close N` | `kb done <id>` |
| `gh issue close N --reason "not planned"` | `kb cancel <id>` (reversible) |
| `gh issue reopen N` | `kb restore <id>` (cancelled → todo) |
| `gh issue delete N` | `kb rm <id> --yes` (permanent, prefer cancel) |
| `gh issue comment N --body B` | `kb comment add <id> "B"` |
| view comments | `kb comment list <id> [--json]` |
| delete a comment | `kb comment rm <cid> --yes` (ids are `c1, c2, ...`, stable) |
| assignees | not modelled: local kb is one board. `kb users` lists any legacy owners still in the database |

Run `kb help` for every flag (priority, due dates, effort, checklists,
blocked flag, status moves).

## Task ids

Tasks carry stable per-board numbers `#1`, `#2`, ... assigned once and
never reused, exactly like gh issue numbers. Address tasks by bare number
(`kb done 12`) or `#12`; UUIDs remain in `--json` (`id`) with the number
as `seq`, and UUID prefixes still resolve.

## Semantics that differ from GitHub/GitLab

- Statuses are kanban columns: `todo`, `doing`, `done`, `cancelled` —
  richer than open/closed. `done` refuses to land while checklist items
  are open or the task is blocked, unless `--force`.
- Labels are `--tag` values: plain strings, no colors, created on use.
- Comments are append-only with delete (`kb comment rm`); there is no
  comment editing — delete and re-post instead.
- Cross-references are typed blocks edges: `kb link <a> blocks <b>`
  (or `blocked-by`), `kb unlink <a> <b>`, shown in `kb view`. Finishing
  a task with open blockers is refused unless `--force`.
- Every command uses the local database selected by `KB_DATA`.
- There are no milestones.
- `kb rm` is a hard delete with no undo; `kb cancel` is the reversible
  close and almost always the right choice.
