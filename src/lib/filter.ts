import type { Board, Task } from './model';

/**
 * Client-side board filter: free text plus exact label toggles. Text is a
 * case-insensitive substring over title, description, and tags — simpler
 * than the server's FTS on purpose: a board is small, and substring
 * matching is predictable while typing. Tags AND together (each click
 * narrows), matching the CLI's --tag semantics.
 */
export interface BoardFilter {
  text: string;
  tags: readonly string[];
}

export function emptyFilter(): BoardFilter {
  return { text: '', tags: [] };
}

export function isFilterActive(filter: BoardFilter): boolean {
  return filter.text.trim() !== '' || filter.tags.length > 0;
}

/** Add the tag if absent, remove it if present — the label-click gesture. */
export function toggleTag(filter: BoardFilter, tag: string): BoardFilter {
  if (filter.tags.includes(tag)) {
    return { ...filter, tags: filter.tags.filter((t) => t !== tag) };
  }
  return { ...filter, tags: [...filter.tags, tag] };
}

export function taskMatchesFilter(task: Task, filter: BoardFilter): boolean {
  for (const tag of filter.tags) {
    if (!task.tags.includes(tag)) return false;
  }
  const needle = filter.text.trim().toLowerCase();
  if (needle === '') return true;
  return (
    task.title.toLowerCase().includes(needle) ||
    task.desc.toLowerCase().includes(needle) ||
    task.tags.some((tag) => tag.toLowerCase().includes(needle))
  );
}

/** The board narrowed to matching tasks; the same object when inactive. */
export function filterBoard(board: Board, filter: BoardFilter): Board {
  if (!isFilterActive(filter)) return board;
  return {
    ...board,
    tasks: board.tasks.filter((task) => taskMatchesFilter(task, filter)),
  };
}
