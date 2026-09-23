import { useEffect, useLayoutEffect, useRef } from 'react';
import { state, STATUSES, STATUS_LABEL, PRIO_LABEL } from '@/store';
import { Button } from '@/components/ui/button';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select';
import { Icon } from '@/components/icon';
import { LabelEditor } from '@/components/label-editor';
import { animate } from '@/lib/format';
import { userTags, withLabel } from '@/lib/labels';
import { orderedSelection, moveMany, bulkPatch, bulkCancel, clearSelection } from '@/lib/board';

// The floating toolbar for a multi-card selection.
export function BulkBar() {
  const n = state.selected.size;
  if (!n) return null;
  return <Bar n={n} />;
}
function Bar({ n }) {
  const ref = useRef(null);
  // The bar is centred on a whole pixel: a translate(-50%) of an odd width lands on a half.
  const centre = () => { const node = ref.current; if (node) node.style.left = Math.round((window.innerWidth - node.offsetWidth) / 2) + 'px'; };
  useLayoutEffect(centre);
  useEffect(() => {
    animate(ref.current, [{ opacity: 0, transform: 'translateY(8px)' }, { opacity: 1, transform: 'none' }], 160);
    window.addEventListener('resize', centre);
    return () => window.removeEventListener('resize', centre);
  }, []);
  const ids = () => orderedSelection();
  return (
    <div id="bulkbar" ref={ref} className="bulkbar" role="toolbar" aria-label="Selection actions">
      <span className="text-13"><span className="num font-semibold">{n}</span> selected</span>
      <Select value="" onValueChange={(v) => { if (v) { moveMany(ids(), v); clearSelection(); } }}>
        <SelectTrigger className="w-auto" aria-label="Move selection to column"><SelectValue placeholder="Move to…" /></SelectTrigger>
        <SelectContent>{STATUSES.map((s) => <SelectItem key={s} value={s}>{STATUS_LABEL[s]}</SelectItem>)}</SelectContent>
      </Select>
      <Select value="" onValueChange={(v) => { if (v) bulkPatch(ids(), () => ({ prio: Number(v) }), `Priority ${PRIO_LABEL[v]}`); }}>
        <SelectTrigger className="w-auto" aria-label="Set priority"><SelectValue placeholder="Priority…" /></SelectTrigger>
        <SelectContent>{[1, 2, 3].map((p) => <SelectItem key={p} value={String(p)}>{PRIO_LABEL[p]}</SelectItem>)}</SelectContent>
      </Select>
      <div className="relative w-50">
        <LabelEditor tags={[]} placeholder="Add label…" single onPick={(tag) => bulkPatch(ids(), (t) => ({ tags: withLabel(userTags(t), tag) }), `Label ${tag}`)} />
      </div>
      <Button onClick={() => bulkCancel(ids())}>Cancel tasks</Button>
      <Button variant="ghost" size="icon" aria-label="Clear selection" data-tip="Clear selection (Esc)" onClick={clearSelection}><Icon name="x" /></Button>
    </div>
  );
}
