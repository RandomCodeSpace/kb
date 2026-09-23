import { useState } from 'react';
import { state, settings, applySettings, STATUSES, STATUS_LABEL, SORTS } from '@/store';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Popover, PopoverTrigger, PopoverContent } from '@/components/ui/popover';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select';
import { Icon } from '@/components/icon';
import { toggleDisplay } from '@/lib/keys';

// Display options: density, column sort, card details, columns and WIP limits.
// Every change patches the shared settings object, so the command palette and
// this panel always agree.
const properties = [
  ['seq', 'Task number'], ['emoji', 'Emoji'], ['desc', 'Description'], ['tags', 'Labels'],
  ['due', 'Due date'], ['effort', 'Effort'], ['checks', 'Checklist'], ['comments', 'Comments'],
];

function CheckSetting({ id, label, checked, onChange }) {
  return (
    <label htmlFor={id} className="flex min-h-9 cursor-pointer items-center gap-2 text-13 phone:min-h-11 coarse:min-h-11">
      <Checkbox id={id} checked={checked} onCheckedChange={(value) => onChange(value === true)} />
      {label}
    </label>
  );
}

function WorkLimit({ status, label, value, onChange }) {
  const [draft, setDraft] = useState(value ? String(value) : '');
  const [error, setError] = useState(false);
  const id = `display-wip-${status}`;
  function commit(event) {
    const raw = event.target.value.trim();
    const number = Number(raw);
    if (raw && (!Number.isSafeInteger(number) || number < 1)) {
      setError(true);
      return;
    }
    setError(false);
    setDraft(raw ? String(number) : '');
    onChange(raw ? number : undefined);
  }
  return (
    <div className="min-w-0 space-y-1.5">
      <label htmlFor={id} className="text-12 font-medium">{label}</label>
      <Input id={id} inputMode="numeric" autoComplete="off" aria-label={`WIP limit for ${label}`} aria-invalid={error}
        aria-describedby={error ? `${id}-error` : 'display-wip-help'} placeholder="None" value={draft}
        onChange={(event) => { setDraft(event.target.value); setError(false); }}
        onBlur={commit} onKeyDown={(event) => { if (event.key === 'Enter') commit(event); }} />
      {error && <p id={`${id}-error`} className="text-12 text-destructive" role="alert">Use a positive whole number.</p>}
    </div>
  );
}

function DisplayOptions({ onClose }) {
  const update = (patch) => { Object.assign(settings, patch); applySettings(); };
  return (
    <div className="text-foreground">
      <header className="sticky top-0 z-10 flex items-start gap-3 border-b border-border bg-background p-4">
        <div className="min-w-0 flex-1">
          <h2 id="display-title" className="text-16 font-semibold">Display options</h2>
          <p className="mt-1 text-12 text-muted-foreground">Choose how tasks appear on your board.</p>
        </div>
        <Button variant="ghost" size="icon" aria-label="Close display options" onClick={onClose}><Icon name="x" /></Button>
      </header>
      <div className="space-y-5 p-4">
        <fieldset className="space-y-2">
          <legend className="mb-2 text-13 font-semibold">Density</legend>
          <div className="grid grid-cols-2 gap-2" role="group" aria-label="Density">
            {['comfortable', 'compact'].map((value) => (
              <Button key={value} variant={settings.density === value ? 'default' : 'secondary'} aria-pressed={settings.density === value ? 'true' : 'false'} onClick={() => update({ density: value })}>
                {value === 'comfortable' ? 'Comfortable' : 'Compact'}
              </Button>
            ))}
          </div>
        </fieldset>
        <div className="space-y-2">
          <span id="display-sort-label" className="block text-13 font-semibold">Column sort</span>
          <Select value={settings.sort} onValueChange={(v) => update({ sort: v })}>
            <SelectTrigger id="display-sort" aria-labelledby="display-sort-label"><SelectValue /></SelectTrigger>
            <SelectContent>{SORTS.map(([value, label]) => <SelectItem key={value} value={value}>{label}</SelectItem>)}</SelectContent>
          </Select>
        </div>
        <fieldset>
          <legend className="mb-1 text-13 font-semibold">Card details</legend>
          <div className="grid grid-cols-2 gap-x-4">
            {properties.map(([key, label]) => (
              <CheckSetting key={key} id={`display-${key}`} label={label} checked={!!settings.show[key]} onChange={(value) => update({ show: { ...settings.show, [key]: value } })} />
            ))}
          </div>
        </fieldset>
        <fieldset>
          <legend className="mb-1 text-13 font-semibold">Columns</legend>
          <CheckSetting id="display-hide-empty" label="Hide empty columns" checked={settings.hideEmpty} onChange={(value) => update({ hideEmpty: value })} />
          <CheckSetting id="display-show-cancelled" label="Show cancelled column" checked={settings.showCancelled} onChange={(value) => update({ showCancelled: value })} />
        </fieldset>
        <fieldset>
          <legend className="text-13 font-semibold">Work in progress limits</legend>
          <p id="display-wip-help" className="mb-3 mt-1 text-12 text-muted-foreground">Highlight columns over their limit. Leave blank for no limit.</p>
          <div className="grid grid-cols-2 gap-3">
            {STATUSES.map((status) => (
              <WorkLimit key={status} status={status} label={STATUS_LABEL[status]} value={settings.wip[status]}
                onChange={(value) => {
                  const wip = { ...settings.wip };
                  if (value === undefined) delete wip[status]; else wip[status] = value;
                  update({ wip });
                }} />
            ))}
          </div>
        </fieldset>
      </div>
    </div>
  );
}

// The header button and its panel.
export function DisplayPanel() {
  return (
    <Popover open={state.displayOpen} onOpenChange={(open) => toggleDisplay(open)}>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="icon" id="display-btn" aria-label="Display options" data-tip="Display  d"><Icon name="sliders" /></Button>
      </PopoverTrigger>
      <PopoverContent align="end" collisionPadding={8} variant="panel" aria-label="Display options" data-display-panel="" onFocusOutside={(e) => e.preventDefault()}
        onOpenAutoFocus={(e) => { e.preventDefault(); const first = e.currentTarget.querySelector('button[aria-pressed]'); if (first) first.focus(); }}>
        <DisplayOptions onClose={() => toggleDisplay(false)} />
      </PopoverContent>
    </Popover>
  );
}
