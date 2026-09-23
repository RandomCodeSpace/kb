// A label is `name` or `scope::value`. The project:: scope is data, never a chip on a card.
export const isProjectTag = (tag) => tag.startsWith('project::');
// link:: and import:: are import provenance, not labels anyone filters by: the detail
// panel's Provenance section reads them. They stay in the editor so a save cannot drop them.
export const isProvenanceTag = (tag) => tag.startsWith('link::') || tag.startsWith('import::');
export const userTags = (t) => (t.tags || []).filter((tag) => !isProjectTag(tag));
export const shownTags = (t) => userTags(t).filter((tag) => !isProvenanceTag(tag));
// A label is scoped when a colon separates a non-empty key from a non-empty
// value: "type::bug" is GitLab's spelling, "type: bug" and "type:bug" are the
// ones GitHub projects use. The double colon is tried first so "a::b" never
// reads as the value ":b"; one space after a single colon belongs to the
// separator. The tag text is never rewritten, only its presentation splits.
// allowEmpty keeps a bare "type:" as a scope query for the label picker.
export function splitLabel(tag, allowEmpty = false) {
  let i = tag.indexOf('::'), len = 2;
  if (i < 0) { i = tag.indexOf(':'); len = 1; }
  if (i <= 0) return { scope: '', value: tag };
  let value = tag.slice(i + len);
  if (len === 1) value = value.replace(/^ /, '');
  return value || allowEmpty ? { scope: tag.slice(0, i), value } : { scope: '', value: tag };
}
// Twelve label hues, spaced for contrast and skipping the violet band so a
// label never reads as the accent or as purple chrome. Hashed from the scope
// name so every type::* shares a hue.
export const LABEL_HUES = [0, 18, 36, 55, 80, 110, 140, 165, 190, 210, 230, 345];
export function hueOf(name) {
  let h = 2166136261;
  for (let i = 0; i < name.length; i++) { h ^= name.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0; }
  return LABEL_HUES[h % LABEL_HUES.length];
}
export const labelHue = (tag) => { const { scope } = splitLabel(tag); return hueOf(scope || tag); };
// One value per scope: adding type::feature drops type::bug.
export function withLabel(tags, tag) {
  const { scope } = splitLabel(tag);
  const kept = tags.filter((t) => t !== tag && !(scope && splitLabel(t).scope === scope));
  return [...kept, tag];
}
export const normalizeTags = (tags) => tags.reduce((acc, t) => (t && !isProjectTag(t) ? withLabel(acc, t) : acc), []);
// {scopes: Map<scope, tags[]>, plain: tags[]}
export function groupLabels(tags) {
  const scopes = new Map(), plain = [];
  for (const tag of tags) {
    const { scope } = splitLabel(tag);
    if (!scope) { plain.push(tag); continue; }
    if (!scopes.has(scope)) scopes.set(scope, []);
    scopes.get(scope).push(tag);
  }
  return { scopes, plain };
}
