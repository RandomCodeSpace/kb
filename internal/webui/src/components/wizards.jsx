import { useEffect, useRef } from 'react';
import { cn } from '@/lib/utils';
import { state, STATUSES, STATUS_LABEL, PRIO_LABEL, EFFORTS } from '@/store';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Chip } from '@/components/ui/chip';
import { Dialog, DialogHeader, DialogTitle, DialogBody, DialogFooter, DialogColumn } from '@/components/ui/dialog';
import { Field, FieldMessage, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Progress } from '@/components/ui/progress';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select';
import { Spinner } from '@/components/ui/spinner';
import { Textarea } from '@/components/ui/textarea';
import { Icon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { TaskLink } from '@/components/bits';
import { clamp, utf8Bytes } from '@/lib/format';
import { isProjectTag, isProvenanceTag } from '@/lib/labels';
import { isDialogOpen, closeDialog } from '@/lib/dialogs';
import { writeProject } from '@/lib/filters';
import { openSettings, isAIConfigured } from '@/lib/settings';
import { openDetail } from '@/lib/detail';
import { splitState, MAX_ADR_BYTES, setSplit, readSplitFile, runSplit, createSplitCards, closeSplit, selectedDrafts } from '@/lib/split';
import { importState, setImport, runPreview, runImport, closeImport, localTask } from '@/lib/import';

// A bounded count control: two buttons and a numeric text field that always agree.
function Stepper({ label, value, onChange, min = 1, max = 20 }) {
  const step = (d) => onChange(clamp(value + d, min, max));
  return (
    <div className="flex items-center gap-1">
      <Button size="icon" aria-label={`One fewer, ${label}`} disabled={value <= min} onClick={() => step(-1)}><Icon name="minus" size={14} /></Button>
      <Input variant="number" inputMode="numeric" autoComplete="off" value={String(value)} aria-label={label}
        onChange={(e) => { const n = Number(e.target.value); if (Number.isFinite(n) && String(n) === e.target.value.trim() && n >= min && n <= max) onChange(n); }}
        onBlur={(e) => onChange(clamp(Math.round(Number(e.target.value) || min), min, max))} />
      <Button size="icon" aria-label={`One more, ${label}`} disabled={value >= max} onClick={() => step(1)}><Icon name="plus" size={14} /></Button>
    </div>
  );
}
function SettingsLink({ text, section }) {
  return <Button variant="link" size="none" onClick={() => openSettings(section)}>{text}</Button>;
}
// One review row for a proposed card: include, title, priority, effort.
function DraftRow({ card, index, noun, extra, onChange }) {
  const label = `${noun} ${index + 1}`;
  return (
    <div className={cn('row', !card.include && 'is-off')} role="group" aria-label={label}>
      <Checkbox className="mt-2" checked={card.include} aria-label={`Include ${label}`} onCheckedChange={(v) => { card.include = v === true; onChange(); }} />
      <div className="row-main">
        <Input value={card.title} aria-label={`${label} title`} onChange={(e) => { card.title = e.target.value; onChange(); }} />
        <div className="flex flex-wrap items-center gap-2">
          <Select value={String(card.prio || 3)} onValueChange={(v) => { card.prio = Number(v); onChange(); }}>
            <SelectTrigger className="w-auto" aria-label={`${label} priority`}><SelectValue /></SelectTrigger>
            <SelectContent>{[1, 2, 3].map((p) => <SelectItem key={p} value={String(p)}>{PRIO_LABEL[p]}</SelectItem>)}</SelectContent>
          </Select>
          <Select value={card.effort || 'none'} onValueChange={(v) => { card.effort = v === 'none' ? '' : v; onChange(); }}>
            <SelectTrigger className="w-auto" aria-label={`${label} effort`}><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value="none">No effort</SelectItem>{EFFORTS.map((x) => <SelectItem key={x} value={x}>{x}</SelectItem>)}</SelectContent>
          </Select>
          {(card.tags || []).filter((t) => !isProjectTag(t) && !isProvenanceTag(t)).map((t) => <Chip key={t} tag={t} />)}
          {extra}
        </div>
        {card.error && <FieldMessage tone="error">{card.error}</FieldMessage>}
        {card.created && <FieldMessage tone="ok">created #{card.created.seq}</FieldMessage>}
      </div>
    </div>
  );
}
function ProgressLine({ progress, label, verb }) {
  if (!progress) return null;
  const { i, n } = progress;
  return (
    <div className="mt-3">
      <Progress className="mb-1" value={i} max={n} aria-label={label} />
      <FieldMessage role="status">{verb(i, n)}</FieldMessage>
    </div>
  );
}

/* ============================== split an ADR into stories ============================== */
export function SplitDialog() {
  const open = isDialogOpen('split');
  const s = splitState;
  const ta = useRef(null);
  const fileRef = useRef(null);
  const wantFocus = s.focusText;
  useEffect(() => { if (open && wantFocus && ta.current) { s.focusText = false; ta.current.focus(); } }, [open, wantFocus, s.stage]);
  const bytes = utf8Bytes(s.text);
  const byteLabel = `UTF-8 bytes: ${bytes.toLocaleString()} / ${MAX_ADR_BYTES.toLocaleString()}`;
  const count = s.stage === 'review' ? selectedDrafts(s.cards).length : 0;
  const rerender = () => setSplit({});
  return (
    <Dialog name="split" open={open} onClose={closeSplit} variant="sheet" aria-labelledby="split-dialog-title">
      <DialogColumn>
        <DialogHeader onClose={closeSplit}><DialogTitle id="split-dialog-title">{s.stage === 'input' ? 'Split ADR into stories' : 'Review proposed stories'}</DialogTitle></DialogHeader>
        <DialogBody>
          {s.stage === 'input' ? (
            <>
              <p className="mb-3 text-12 text-fg-2">Paste one decision or design document. Nothing is created until you review the stories it proposes.</p>
              <Field label="ADR markdown" message={bytes > MAX_ADR_BYTES ? byteLabel + ' — over the limit; trim the document' : byteLabel} tone={bytes > MAX_ADR_BYTES ? 'error' : ''}>
                <Textarea ref={ta} variant="code" rows={12} spellCheck={false} value={s.text} placeholder={'# ADR 0007: adopt SQLite for the board store\n\n## Context\n…'} onChange={(e) => setSplit({ text: e.target.value })} />
              </Field>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <input ref={fileRef} type="file" hidden accept=".md,.markdown,.txt,text/markdown,text/plain" tabIndex={-1} aria-hidden="true"
                  onChange={(e) => { const f = e.target.files && e.target.files[0]; e.target.value = ''; readSplitFile(f); }} />
                <Button onClick={() => fileRef.current.click()}><Icon name="upload" size={14} />Read a file…</Button>
                {s.text && <Button variant="ghost" size="sm" onClick={() => { setSplit({ text: '' }); ta.current.focus(); }}>Clear</Button>}
              </div>
              <div className="mt-4 flex flex-wrap items-end gap-4">
                <div className="field">
                  <FieldLabel>Max stories<span className="ml-1.5 font-normal tracking-normal text-fg-3">1–20</span></FieldLabel>
                  <Stepper label="Max stories" value={s.max} onChange={(n) => setSplit({ max: n })} />
                  <FieldMessage />
                </div>
                <div className="field">
                  <FieldLabel as="span" id="split-dest-label">Destination</FieldLabel>
                  <Select value={s.status} onValueChange={(v) => setSplit({ status: v })}>
                    <SelectTrigger className="w-auto" aria-labelledby="split-dest-label"><SelectValue /></SelectTrigger>
                    <SelectContent>{STATUSES.filter((x) => x !== 'cancelled').map((x) => <SelectItem key={x} value={x}>{STATUS_LABEL[x]}</SelectItem>)}</SelectContent>
                  </Select>
                  <FieldMessage />
                </div>
              </div>
              {s.error && <FieldMessage tone="error" className="mt-3" role="alert">{s.error}</FieldMessage>}
              {state.ai && !isAIConfigured() && <p className="mt-3 text-12 text-fg-3">Splitting needs an AI endpoint. <SettingsLink text="Set one up in Settings" section="ai" />.</p>}
            </>
          ) : (
            <>
              <p className="mb-3 text-12 text-fg-2">Nothing is created until you add the selected stories. They go to <span className="font-medium text-fg">{STATUS_LABEL[s.status]}</span> in <span className="font-medium text-fg">{writeProject()}</span>.</p>
              {s.partial && <p className="mb-3 flex items-center gap-2 text-12 text-warn"><Icon name="alert" size={14} />The run hit its budget: these stories are real, but the set may be incomplete.</p>}
              {s.commentary && <div className="mb-4 rounded-md border border-line bg-surface px-3 py-2"><Markdown text={s.commentary} /></div>}
              <div className="rows">{s.cards.map((c, i) => <DraftRow key={i} card={c} index={i} noun="Story" onChange={rerender} />)}</div>
              {s.error && <FieldMessage tone="error" className="mt-3" role="alert">{s.error}</FieldMessage>}
              <ProgressLine progress={s.progress} label="Creating stories" verb={(i, n) => (i < n ? `creating card ${i + 1} of ${n}…` : `created ${n} of ${n}`)} />
            </>
          )}
        </DialogBody>
        <DialogFooter>
          <span className="flex-1" />
          {s.stage === 'input' ? (
            <>
              <Button onClick={closeSplit}>Cancel</Button>
              <Button variant="default" disabled={s.busy} onClick={runSplit}>{s.busy ? <><Spinner />Proposing stories…</> : <><Icon name="sparkle" size={14} />Propose stories</>}</Button>
            </>
          ) : (
            <>
              <Button onClick={() => setSplit({ stage: 'input' })}><Icon name="chevL" size={14} />Back to source</Button>
              <Button onClick={closeSplit}>Close</Button>
              <Button variant="default" disabled={s.busy || count === 0} onClick={createSplitCards}>{s.busy ? <><Spinner />Adding…</> : `Add selected (${count})`}</Button>
            </>
          )}
        </DialogFooter>
      </DialogColumn>
    </Dialog>
  );
}

/* ============================== import forge issues ============================== */
function DuplicateBadge({ dup }) {
  if (!dup) return null;
  const known = localTask(dup.id);
  const seq = known ? known.seq : dup.seq;
  const label = dup.via === 'link' ? (seq ? `Already imported as #${seq}` : 'Already imported') : `Similar: ${dup.title}`;
  const tone = dup.via === 'link' ? 'warn' : 'fg-2';
  if (!seq) return <Chip tone={tone} icon="alert" title={dup.title}><span>{label}</span></Chip>;
  return <Chip tone={tone} icon="alert" title={`Open #${seq} ${dup.title}`} onClick={() => { closeDialog('import'); openDetail(seq); }}><span>{label}</span></Chip>;
}
export function ImportDialog() {
  const open = isDialogOpen('import');
  const s = importState;
  const refInput = useRef(null);
  const wantFocus = s.focusRef;
  useEffect(() => { if (open && wantFocus && refInput.current) { s.focusRef = false; refInput.current.focus(); } }, [open, wantFocus]);
  useEffect(() => {
    if (!open || s.stage !== 'input' || !s.sources || !s.sources.length) return;
    const first = document.querySelector('#import-dialog-body [data-slot="select-trigger"], #import-dialog-body input');
    if (first && !document.activeElement?.closest('#import-dialog-body')) first.focus();
  }, [open, s.sources, s.stage]);
  const p = s.preview || {};
  const count = s.stage === 'review' ? selectedDrafts(s.drafts).length : 0;
  const rerender = () => setImport({});
  const fetched = p.totalHint && p.totalHint > p.fetched
    ? `Fetched ${p.fetched} of about ${p.totalHint}${p.truncated ? '; results truncated' : ''}`
    : `Fetched ${p.fetched || s.drafts.length}${p.truncated ? '; results truncated' : ''}`;
  let body, foot;
  if (s.stage === 'input' && s.sources === null) {
    body = <div className="flex items-center gap-2 py-8 text-13 text-fg-2"><Spinner />Loading sources…</div>;
    foot = <Button onClick={closeImport}>Cancel</Button>;
  } else if (s.stage === 'input' && !s.sources.length) {
    body = (
      <div className="rounded-md border border-dashed border-line-2 px-4 py-8 text-center">
        <p className="text-13 font-medium">No forge integration configured</p>
        <p className="mx-auto mt-1 max-w-prose text-12 text-fg-2">Add a GitLab or GitHub source with a personal access token, then come back to import its issues.</p>
        <Button variant="default" className="mt-4" onClick={() => openSettings('forge')}><Icon name="gear" size={14} />Open settings</Button>
      </div>
    );
    foot = <Button onClick={closeImport}>Cancel</Button>;
  } else if (s.stage === 'input') {
    body = (
      <>
        <div className="import-grid grid gap-3">
          <div className="field">
            <FieldLabel as="span" id="import-source-label">Source</FieldLabel>
            <Select value={s.source} onValueChange={(v) => setImport({ source: v })}>
              <SelectTrigger aria-labelledby="import-source-label"><SelectValue /></SelectTrigger>
              <SelectContent>{s.sources.map((x) => <SelectItem key={x.name} value={x.name}>{`${x.name} · ${x.kind}`}</SelectItem>)}</SelectContent>
            </Select>
            <FieldMessage />
          </div>
          <Field label="Reference">
            <Input ref={refInput} name="forge-ref" value={s.ref} autoComplete="off" spellCheck={false} placeholder="owner/repo, or an issue, milestone or project URL"
              onChange={(e) => setImport({ ref: e.target.value })} onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); if (!s.busy) runPreview(); } }} />
          </Field>
        </div>
        <div className="field mt-4">
          <FieldLabel>Max issues<span className="ml-1.5 font-normal tracking-normal text-fg-3">1–20</span></FieldLabel>
          <Stepper label="Max issues" value={s.max} onChange={(n) => setImport({ max: n })} />
          <FieldMessage />
        </div>
        {s.error && <FieldMessage tone="error" className="mt-3" role="alert">{s.error}</FieldMessage>}
        {state.ai && !isAIConfigured()
          ? <p className="mt-4 text-12 text-fg-3">The preview turns fetched issues into cards with the AI endpoint, so it needs one configured. <SettingsLink text="Set one up in Settings" section="ai" />.</p>
          : <p className="mt-4 text-12 text-fg-3">The preview fetches the issues and drafts cards from them. Nothing is written until you review them.</p>}
      </>
    );
    foot = (
      <>
        <Button onClick={closeImport}>Cancel</Button>
        <Button variant="default" disabled={s.busy} onClick={runPreview}>{s.busy ? <><Spinner />Fetching and drafting…</> : <><Icon name="sync" size={14} />Preview</>}</Button>
      </>
    );
  } else {
    body = (
      <>
        <p className="text-12 text-fg-2">{fetched} · {p.kind || 'issue'} · {s.source}</p>
        {p.note && <p className="mt-2 flex items-start gap-2 text-12 text-warn"><Icon name="alert" size={14} />{p.note}</p>}
        <p className="mb-3 mt-2 text-12 text-fg-2">Cards already carrying the same upstream link start unticked. Nothing is written until you import.</p>
        <div className="rows">{s.drafts.map((d, i) => <DraftRow key={i} card={d} index={i} noun="Issue" extra={<DuplicateBadge dup={d.duplicate} />} onChange={rerender} />)}</div>
        {s.error && <FieldMessage tone="error" className="mt-3" role="alert">{s.error}</FieldMessage>}
        {s.created.length > 0 && (
          <div className="mt-4">
            <h3 className="label-11 mb-1">Imported ({s.created.length})</h3>
            <ul className="flex flex-col gap-0.5">{s.created.map((t) => <li key={t.id}><TaskLink task={t} /></li>)}</ul>
          </div>
        )}
        <ProgressLine progress={s.progress} label="Importing issues" verb={(i, n) => (i < n ? `writing ${i + 1}/${n}` : `wrote ${n}/${n}`)} />
      </>
    );
    foot = (
      <>
        <Button onClick={() => setImport({ stage: 'input' })}><Icon name="chevL" size={14} />Back</Button>
        <Button onClick={closeImport}>Close</Button>
        <Button variant="default" disabled={s.busy || count === 0} onClick={runImport}>{s.busy ? <><Spinner />Importing…</> : `Import selected (${count})`}</Button>
      </>
    );
  }
  return (
    <Dialog name="import" open={open} onClose={closeImport} variant="sheet" aria-labelledby="import-dialog-title">
      <DialogColumn>
        <DialogHeader onClose={closeImport}><DialogTitle id="import-dialog-title">{s.stage === 'input' ? 'Forge issue import' : 'Review fetched issues'}</DialogTitle></DialogHeader>
        <DialogBody id="import-dialog-body">{body}</DialogBody>
        <DialogFooter><span className="flex-1" />{foot}</DialogFooter>
      </DialogColumn>
    </Dialog>
  );
}
