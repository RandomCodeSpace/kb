# kb tasks

A task records work on a board. Finishing it marks the work done.

## Language

**Finishing guard**:
A refusal to finish a task while it has an open item or is flagged blocked. It applies to creation in done and to moving an existing task to done.

**Open item**:
An unticked checklist entry with non-blank text. Clearing the checklist removes its items.

**Override**:
An explicit choice to finish despite the finishing guard, expressed as `--force` in the CLI. It does not waive task field validation.
