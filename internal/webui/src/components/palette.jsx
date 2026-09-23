import { useEffect, useRef } from 'react';
import { state } from '@/store';
import { Dialog } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Icon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { isDialogOpen, closeDialog } from '@/lib/dialogs';
import { setPaletteQuery, selectPalette, runPalette } from '@/lib/palette';

function Label({ s }) {
  if (s.item.group === 'Task') {
    const m = /^(#\d+)\s(.*)$/s.exec(s.item.label);
    return <span className="flex min-w-0 flex-1 items-center gap-2"><span className="seq">{m[1]}</span><span className="truncate"><Markdown text={m[2]} mode="inline" /></span></span>;
  }
  const pos = new Set(s.positions);
  const parts = [];
  let run = '';
  const text = s.item.label;
  for (let k = 0; k <= text.length; k++) {
    const ch = text[k];
    if (k < text.length && pos.has(k)) { if (run) { parts.push(run); run = ''; } parts.push(<mark key={k}>{ch}</mark>); }
    else if (k < text.length) run += ch;
  }
  if (run) parts.push(run);
  return <span className="min-w-0 flex-1 truncate">{parts}</span>;
}

export function Palette() {
  const open = isDialogOpen('palette');
  const ref = useRef(null);
  const listRef = useRef(null);
  const index = state.paletteIndex;
  useEffect(() => { if (open && ref.current) ref.current.focus(); }, [open]);
  useEffect(() => {
    const li = listRef.current && listRef.current.children[index];
    if (li && li.scrollIntoView) li.scrollIntoView({ block: 'nearest' });
  }, [index, open]);
  const items = state.paletteItems;
  return (
    <Dialog name="palette" open={open} onClose={() => closeDialog('palette')} variant="overlay" aria-label="Command palette">
      <div className="flex h-12 items-center gap-2 border-b border-line px-3">
        <Icon name="terminal" className="text-fg-3" />
        <Input ref={ref} variant="palette" id="palette-search" placeholder="Type a command, #number, project or label…" autoComplete="off" spellCheck={false}
          role="combobox" aria-expanded="true" aria-controls="palette-list" aria-autocomplete="list" aria-label="Command palette" aria-activedescendant={items.length ? `pal-${index}` : undefined}
          value={state.paletteQuery} onChange={(e) => setPaletteQuery(e.target.value)} />
        <Kbd>Esc</Kbd>
      </div>
      <ul id="palette-list" ref={listRef} className="max-h-90 overflow-y-auto overscroll-contain p-1" role="listbox" aria-label="Commands">
        {items.length ? items.map((s, i) => (
          <li key={s.item.group + s.item.label} className="pal-item" role="option" id={`pal-${i}`} aria-selected={i === index ? 'true' : 'false'} onClick={() => runPalette(i)} onMouseMove={() => selectPalette(i)}>
            <span className="group">{s.item.group}</span>
            <Label s={s} />
            {s.item.kbd ? <Kbd>{s.item.kbd}</Kbd> : null}
          </li>
        )) : <li className="px-3 py-6 text-center text-13 text-fg-3">No matches. Try a task number or a label.</li>}
      </ul>
    </Dialog>
  );
}
