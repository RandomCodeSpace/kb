---
name: kb-issues
description: Use the kb CLI as a local replacement for GitHub/GitLab issue commands (gh issue, glab issue). Use when tracking tasks, bugs, or issues locally with kb instead of a remote issue tracker, or when translating a gh/glab issue workflow to kb.
---

# kb as a local issue tracker

kb is a local-first kanban board with a CLI. Where a workflow reaches for
`gh issue` or `glab issue`, use the `kb` verbs below instead. State lives in
a local SQLite database (default `~/.local/share/kb`), or on a kb server
when `KB_SERVER` is set.

## Environment

- `KB_USER` — board owner every command operates on (default `default`).
  An explicit `--user` flag beats it. Identities are lowercased; stick to
  `[a-z0-9._@-]`.
- `KB_DATA` — data directory override.
- `KB_SERVER` + `KB_SERVER_TOKEN` — operate against a running kb server
  over HTTP instead of the local database.
- MCP-capable agents can run `kb mcp` and use the board tools directly
  instead of shelling out.

## Command mapping

| gh / glab | kb |
|---|---|
| `gh issue create --title T --body B` | `kb add "T" --desc "B"` |
| `gh issue create --label bug` | `kb add "T" --tag bug` (repeat `--tag`) |
| `gh issue list` | `kb list` |
| `gh issue list --json ...` | `kb list --json` (full tasks, fixed shape) |
| `gh issue list --state all` | `kb list --all` (includes cancelled) |
| `gh issue view N` | `kb list --json` and select by id (no single-task view) |
| `gh issue edit N --title/--body` | `kb update <id> --title T --desc B` |
| `gh issue close N` | `kb done <id>` |
| `gh issue close N --reason "not planned"` | `kb cancel <id>` (reversible) |
| `gh issue reopen N` | `kb restore <id>` (cancelled → todo) |
| `gh issue delete N` | `kb rm <id> --yes` (permanent, prefer cancel) |
| assignees | one board per user: `--user name`; `kb users` lists boards |

Run `kb help` for every flag (priority, due dates, effort, checklists,
blocked flag, status moves).

## Task ids

- Local mode: UUIDs, addressable by unique prefix (first 8 chars shown).
- Remote mode (`KB_SERVER`): ephemeral listing indexes `i1`, `i2`, ...
  valid only against the current listing — re-run `kb list` after writes
  and never store these ids.

## Semantics that differ from GitHub/GitLab

- Statuses are kanban columns: `todo`, `doing`, `done`, `cancelled` —
  richer than open/closed. `done` refuses to land while checklist items
  are open or the task is blocked, unless `--force`.
- Labels are `--tag` values: plain strings, no colors, created on use.
- There are no issue comments, no milestones, and no stable issue
  numbers to cross-reference. Put discussion in the description
  (`--desc`) or checklist items (`--check`).
- `kb rm` is a hard delete with no undo; `kb cancel` is the reversible
  close and almost always the right choice.
