import { useEffect, useId, useRef, useState } from 'react';
import { cn } from '@/lib/utils';
import { Chip } from '@/components/ui/chip';
import { Input } from '@/components/ui/input';
import { Icon } from '@/components/icon';
import { fuzzy } from '@/lib/format';
import { splitLabel, withLabel, normalizeTags, groupLabels, isProjectTag } from '@/lib/labels';
import { allLabels } from '@/lib/filters';

// Removable chips, an input and a grouped picker fed by the board's labels.
// One value per scope: picking type::bug replaces type::feature. `single`
// reports each pick instead of keeping a list (the bulk bar).
export function LabelEditor({ tags: initial, onChange, onPick, placeholder = 'Add label…', single, boxed, className }) {
  const key = (initial || []).join('\n');
  const [tags, setTagsState] = useState(() => normalizeTags(initial || []));
  useEffect(() => { setTagsState(normalizeTags(initial || [])); }, [key]);
  const [text, setText] = useState('');
  const [open, setOpen] = useState(false);
  const [index, setIndex] = useState(0);
  const inputRef = useRef(null);
  const pickRef = useRef(null);
  const id = useId();
  const setTags = (next) => { const clean = normalizeTags(next); setTagsState(clean); if (onChange) onChange(clean); };
  const close = () => setOpen(false);
  const choose = (tag) => {
    setText('');
    close();
    if (single) { if (onPick) onPick(tag); return; }
    setTags(withLabel(tags, tag));
    inputRef.current.focus();
  };
  const candidates = () => {
    const q = text.trim();
    const ql = q.toLowerCase();
    const pool = allLabels().filter((l) => !tags.includes(l));
    let list;
    if (!ql) list = pool;
    else if (splitLabel(ql, true).scope) { const { scope, value } = splitLabel(ql, true); list = pool.filter((l) => { const p = splitLabel(l); return p.scope.toLowerCase() === scope && (!value || p.value.toLowerCase().includes(value)); }); }
    else list = pool.map((l) => ({ l, m: fuzzy(ql, l) })).filter((x) => x.m).sort((a, b) => b.m.score - a.m.score).map((x) => x.l);
    const out = [];
    const { scopes, plain } = groupLabels(list);
    for (const [scope, ts] of scopes) { out.push({ head: scope }); for (const t of ts) out.push({ tag: t }); }
    if (plain.length) { if (scopes.size) out.push({ head: 'Other' }); for (const t of plain) out.push({ tag: t }); }
    // A new label needs a value after its colon, and the project scope is
    // reserved in either spelling so a decoy like project:web cannot exist.
    if (q && !/\s/.test(q) && !pool.includes(q) && !tags.includes(q) && !isProjectTag(q) && !/^project:/i.test(q) && !/:$/.test(q) && !/^:/.test(q)) out.push({ create: q });
    return out;
  };
  const all = open ? candidates() : [];
  const items = all.filter((x) => !x.head);
  const shown = open && all.length > 0;
  const cur = Math.max(0, Math.min(items.length - 1, index));
  useEffect(() => {
    if (!shown || !pickRef.current) return;
    const opt = pickRef.current.querySelectorAll('[role="option"]')[cur];
    if (opt) opt.scrollIntoView({ block: 'nearest' });
  }, [shown, cur]);
  const show = () => { setIndex(0); setOpen(true); };
  const onKeyDown = (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); if (!shown) show(); else setIndex(Math.min(items.length - 1, cur + 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setIndex(Math.max(0, cur - 1)); }
    else if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); const it = shown ? items[cur] : null; if (it) choose(it.tag || it.create); else if (text.trim()) choose(text.trim()); }
    else if (e.key === 'Escape') { if (shown) { e.preventDefault(); e.stopPropagation(); close(); } else if (text) { e.preventDefault(); e.stopPropagation(); setText(''); } }
    else if (e.key === 'Backspace' && !text && tags.length && !single) { e.preventDefault(); setTags(tags.slice(0, -1)); }
    else if (e.key === ',') { e.preventDefault(); if (text.trim()) choose(text.trim()); }
  };
  let k = -1;
  return (
    <div className={cn('relative', boxed && 'input h-auto min-h-(--control) py-0', className)}>
      <div className="label-row">
        {tags.map((tag) => <Chip key={tag} tag={tag} enter remove={() => setTags(tags.filter((t) => t !== tag))} />)}
        <Input ref={inputRef} variant={boxed ? 'boxed' : 'ghost'} className="min-w-30 flex-1" value={text} placeholder={placeholder} aria-label={placeholder} autoComplete="off" spellCheck={false}
          role="combobox" aria-expanded={shown ? 'true' : 'false'} aria-autocomplete="list" aria-controls={`${id}-pick`} aria-activedescendant={shown && items[cur] ? `${id}-opt-${cur}` : undefined}
          onChange={(e) => { setText(e.target.value); setIndex(0); setOpen(true); }} onFocus={show} onBlur={() => setTimeout(close, 120)} onKeyDown={onKeyDown} />
      </div>
      <div ref={pickRef} id={`${id}-pick`} className="pick" role="listbox" aria-label="Labels" hidden={!shown}>
        {all.map((x, i) => {
          if (x.head) return <div key={'h' + i} className="menu-head">{x.head}</div>;
          k++;
          const at = k;
          return (
            <div key={x.tag || 'create:' + x.create} className="menu-item h-8" role="option" id={`${id}-opt-${at}`} aria-selected={at === cur ? 'true' : 'false'}
              onMouseDown={(e) => e.preventDefault()} onClick={() => choose(x.tag || x.create)} onMouseMove={() => setIndex(at)}>
              {x.create ? <><Icon name="plus" size={12} /><span>Create </span><Chip tag={x.create} /></> : <Chip tag={x.tag} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}
