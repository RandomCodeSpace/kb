export function shouldQuery(title: string, last: string): boolean {
  const trimmed = title.trim();
  return Array.from(trimmed).length >= 3 && trimmed !== last.trim();
}
