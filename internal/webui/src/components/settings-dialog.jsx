import { useEffect } from 'react';
import { cn } from '@/lib/utils';
import { state } from '@/store';
import { Button } from '@/components/ui/button';
import { Chip } from '@/components/ui/chip';
import { Dialog, DialogHeader, DialogTitle, DialogBody, DialogFooter, DialogColumn } from '@/components/ui/dialog';
import { Field, FieldMessage, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Spinner } from '@/components/ui/spinner';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { Icon } from '@/components/icon';
import { Time } from '@/components/bits';
import { MOD, fmtDate } from '@/lib/format';
import { isDialogOpen } from '@/lib/dialogs';
import { sset, FORGE_KINDS, openSettings, closeSettings, setAIField, editAIKey, clearAIKey, keepAIKey, testAI, saveAI, addForge, setForgeField, setForgeKind, editForgeToken, clearForgeToken, keepForgeToken, testForge, saveForge, removeForge, forgeKey } from '@/lib/settings';

// Mirrors internal/tui/settings_view.go: the AI row first, then one block per configured integration.
// Secrets are write-only everywhere: a read reports only whether one is stored.
export function SettingsDialog() {
  const open = isDialogOpen('settings');
  const focusKey = sset.focusKey;
  useEffect(() => {
    if (!open || !focusKey) return;
    sset.focusKey = '';
    const root = document.getElementById('settings-dialog-body');
    if (!root) return;
    const node = focusKey === 'forge-first' ? root.querySelector('[data-k^="forge"]') : root.querySelector(`[data-k="${CSS.escape(focusKey)}"]`);
    (node || root.querySelector('input') || root).focus?.();
  }, [open, focusKey, sset.loaded, sset.loading]);
  return (
    <Dialog name="settings" open={open} onClose={closeSettings} variant="sheet" aria-labelledby="settings-dialog-title">
      <DialogColumn>
        <DialogHeader onClose={closeSettings}><DialogTitle id="settings-dialog-title">Settings</DialogTitle></DialogHeader>
        <DialogBody id="settings-dialog-body">
          {sset.loading ? <div className="flex items-center gap-2 py-8 text-13 text-fg-2"><Spinner />Loading settings…</div>
            : sset.loadError ? (
              <div className="py-8">
                <FieldMessage tone="error" role="alert">{sset.loadError}</FieldMessage>
                <Button className="mt-3" onClick={() => { sset.loaded = false; openSettings(sset.section); }}><Icon name="sync" size={14} />Try again</Button>
              </div>
            ) : sset.loaded ? <><AISection /><ForgeSection /></> : null}
        </DialogBody>
        <DialogFooter>
          <span className="phone-hide text-12 text-fg-3"><Kbd>{MOD}</Kbd> <Kbd>S</Kbd> saves the section you are in</span>
          <span className="flex-1" />
          <Button onClick={closeSettings}>Close</Button>
        </DialogFooter>
      </DialogColumn>
    </Dialog>
  );
}

function SettingsSection({ title, note, children }) {
  return (
    <section className="settings-section">
      <div className="settings-head"><h3 className="text-14 font-semibold">{title}</h3>{note && <p className="text-12 text-fg-2">{note}</p>}</div>
      {children}
    </section>
  );
}

// A saved secret never comes back from the server, so the field shows its
// state (Saved) with Replace and Remove, and only opens an input once one of
// them is chosen. noun names the thing in messages: key, token.
function SecretField({ label, noun, k, saved, editing, clearing, onEdit, onClear, onBack, className, children }) {
  if (saved && !editing && !clearing) {
    return (
      <div className={cn('field', className)}>
        <div className="field-head"><FieldLabel>{label}</FieldLabel></div>
        <div className="secret">
          <Icon name="lock" size={14} /><span>Saved</span><span className="dots" aria-hidden="true">••••••••</span>
          <Button variant="ghost" size="xs" data-k={k + '-edit'} onClick={onEdit}>Replace</Button>
          <Button variant="ghost" size="xs" data-k={k + '-clear'} onClick={onClear}>Remove</Button>
        </div>
        <FieldMessage />
      </div>
    );
  }
  const back = saved ? <Button variant="ghost" size="xs" data-k={k + '-back'} onClick={onBack}>{clearing ? `Keep the saved ${noun}` : 'Cancel'}</Button> : null;
  return (
    <Field label={label} action={back} className={className} message={clearing ? `Removed when you save. Paste a ${noun} to keep one.` : ''} tone={clearing ? 'warn' : ''}>
      {children}
    </Field>
  );
}

function AISection() {
  const ai = sset.ai;
  const on = !!(state.ai && state.ai.configured);
  let host = '';
  try { host = new URL(state.ai && state.ai.baseURL).host; } catch (e) { host = (state.ai && state.ai.baseURL) || ''; }
  return (
    <SettingsSection title="AI" note="An OpenAI-compatible endpoint. The model must support function calling. The key is stored sealed and is never sent back to this page.">
      <div className={cn('ai-state', on && 'is-on')} role="status">
        <span className="status-dot" aria-hidden="true" />
        {on ? <><span className="k">{state.ai.model || 'no model'}</span><span className="num">{host}</span><span className="ml-auto text-fg-3">saved</span></>
          : <span>No AI endpoint yet. Drafting, splitting and duplicate checks stay off until one is saved.</span>}
      </div>
      <form className="grid gap-4 sm:grid-cols-2" data-save="ai" noValidate onSubmit={(e) => { e.preventDefault(); saveAI(); }}>
        <Field label="Base URL" className="sm:col-span-2">
          <Input type="url" name="ai-baseURL" data-k="ai-base" autoComplete="off" spellCheck={false} value={ai.baseURL} placeholder="https://api.openai.com/v1" onChange={(e) => setAIField('baseURL', e.target.value)} />
        </Field>
        <Field label="Model">
          <Input name="ai-model" data-k="ai-model" autoComplete="off" spellCheck={false} value={ai.model} placeholder="gpt-4o" onChange={(e) => setAIField('model', e.target.value)} />
        </Field>
        <SecretField label="API key" noun="key" k="ai-key" saved={ai.hasKey} editing={ai.editKey} clearing={ai.clearKey} onEdit={editAIKey} onClear={clearAIKey} onBack={keepAIKey}>
          <Input type="password" name="ai-key" data-k="ai-key" autoComplete="new-password" spellCheck={false} value={ai.key} placeholder={ai.hasKey ? 'paste the new key' : 'sk-…'} onChange={(e) => setAIField('key', e.target.value)} />
        </SecretField>
        <div className="settings-actions sm:col-span-2">
          <Button data-k="ai-test" disabled={!!ai.busy} onClick={testAI}>{ai.busy === 'test' ? <><Spinner />Testing…</> : <><Icon name="sync" size={14} />Test connection</>}</Button>
          <Button type="submit" variant="default" data-k="ai-save" disabled={!!ai.busy}>{ai.busy === 'save' ? <><Spinner />Saving…</> : 'Save AI settings'}</Button>
          <FieldMessage tone={ai.tone} role="status">{ai.msg}</FieldMessage>
        </div>
      </form>
    </SettingsSection>
  );
}

function ForgeSection() {
  const rows = sset.rows;
  const draft = sset.draft;
  return (
    <SettingsSection title="Integrations" note="GitLab and GitHub sources the issue import reads from. Tokens are write-only and never leave the server.">
      {rows.length || draft
        ? <div className="rows">{rows.map((r) => <ForgeRow key={r.name} row={r} />)}{draft && <ForgeRow row={draft} />}</div>
        : <p className="rounded-md border border-dashed border-line-2 px-3 py-6 text-center text-13 text-fg-3">No integrations yet.</p>}
      <div className="settings-actions"><Button data-k="forge-add" onClick={addForge}><Icon name="plus" size={14} />Add integration</Button></div>
    </SettingsSection>
  );
}
function ForgeRow({ row }) {
  const isDraft = !!row.draft;
  const k = (name) => forgeKey(row, name);
  const text = (name, props) => (
    <Input name={k(name)} data-k={k(name)} autoComplete="off" spellCheck={false} value={row[name]} onChange={(e) => setForgeField(row, name, e.target.value)} {...props} />
  );
  return (
    <div className="row row-form">
      <form className="row-main" data-save={isDraft ? 'forge:new' : 'forge:' + row.name} noValidate onSubmit={(e) => { e.preventDefault(); saveForge(row); }}>
        {isDraft ? (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name">{text('name', { placeholder: 'work-gitlab' })}</Field>
            <div className="field">
              <FieldLabel>Kind</FieldLabel>
              <ToggleGroup type="single" grow value={row.kind} aria-label="Kind" onValueChange={(v) => { if (v) setForgeKind(row, v); }}>
                {FORGE_KINDS.map(([value, label]) => <ToggleGroupItem key={value} value={value}>{label}</ToggleGroupItem>)}
              </ToggleGroup>
              <FieldMessage />
            </div>
          </div>
        ) : (
          <div className="row-head">
            <Chip mono><span>{row.kind}</span></Chip>
            <span className="text-13 font-semibold">{row.name}</span>
            <span className="text-11 text-fg-3" data-tip="The name and kind of a saved source cannot change">locked</span>
            {row.createdAt && <span className="num ml-auto text-11 text-fg-3" data-tip={fmtDate(row.createdAt)}>added <Time iso={row.createdAt} /></span>}
          </div>
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Base URL">{text('baseURL', { placeholder: row.kind === 'github' ? 'github.com' : 'gitlab.example.com' })}</Field>
          <Field label="Project" hint="test only">{text('project', { placeholder: 'owner/project (optional)' })}</Field>
          <SecretField label="Token" noun="token" k={k('token')} className="sm:col-span-2" saved={row.hasToken} editing={row.editToken} clearing={row.clearToken}
            onEdit={() => editForgeToken(row)} onClear={() => clearForgeToken(row)} onBack={() => keepForgeToken(row)}>
            <Input type="password" name={k('token')} data-k={k('token')} autoComplete="new-password" spellCheck={false} value={row.token} placeholder={row.hasToken ? 'paste the new token' : 'personal access token'} onChange={(e) => setForgeField(row, 'token', e.target.value)} />
          </SecretField>
        </div>
        <div className="settings-actions">
          <Button data-k={k('test')} disabled={!!row.busy} onClick={() => testForge(row)}>{row.busy === 'test' ? <><Spinner />Testing…</> : 'Test'}</Button>
          <Button type="submit" variant="default" data-k={k('save')} disabled={!!row.busy}>{row.busy === 'save' ? <><Spinner />Saving…</> : 'Save'}</Button>
          <Button variant={row.armed ? 'destructive' : 'ghost'} data-k={k('remove')} onClick={() => removeForge(row)}>{row.armed ? 'Confirm remove' : 'Remove'}</Button>
          <FieldMessage tone={row.tone} role="status">{row.msg}</FieldMessage>
        </div>
      </form>
    </div>
  );
}
