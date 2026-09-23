import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { Textarea } from '@/components/ui/textarea';
import { Icon } from '@/components/icon';
import { Markdown } from '@/components/markdown';
import { MOD, insertText } from '@/lib/format';
import { confirmDiscard } from '@/lib/dialogs';

const LIST_RE = /^(\s*)([-*+]|\d+[.)])\s(\[[ xX]\]\s)?/;
const TOOLS = [
  ['heading', 'Heading', '', 'h'], ['bold', 'Bold', MOD + ' B'], ['italic', 'Italic', MOD + ' I'], ['strike', 'Strikethrough'], null,
  ['code', 'Inline code', MOD + ' E'], ['codeblock', 'Code block'], ['link', 'Link', MOD + ' K'], ['quote', 'Quote'], null,
  ['ul', 'Bullet list'], ['ol', 'Numbered list'], ['task', 'Task list'],
];

// A markdown textarea with a formatting bar and a preview toggle. Controlled:
// the owner holds the text. `busy` marks an open inline editor in the detail
// panel; `is-dirty` follows the text against its initial value.
export function MarkdownEditor({ value, onChange, placeholder = 'Write markdown…', label = 'Markdown editor', rows = 4, onSave, onCancel, saveLabel = 'Save', busy, autoFocus, className }) {
  const ta = useRef(null);
  const initial = useRef(value);
  const [view, setView] = useState('write');
  useLayoutEffect(() => {
    const node = ta.current;
    if (!node) return;
    node.style.height = 'auto';
    node.style.height = Math.max(node.scrollHeight, 120) + 'px';
  }, [value, view]);
  useEffect(() => {
    if (!autoFocus) return;
    const node = ta.current;
    node.focus();
    node.setSelectionRange(0, 0);
    node.scrollTop = 0;
  }, [autoFocus]);
  const sel = () => ({ s: ta.current.selectionStart, e: ta.current.selectionEnd, v: ta.current.value });
  const replaceRange = (s, e, text, selStart, selEnd) => { const node = ta.current; node.setSelectionRange(s, e); insertText(node, text); node.setSelectionRange(selStart, selEnd); };
  const wrap = (before, after, ph) => {
    const { s, e, v } = sel();
    const inner = v.slice(s, e) || ph || '';
    if (v.slice(s - before.length, s) === before && v.slice(e, e + after.length) === after) { replaceRange(s - before.length, e + after.length, inner, s - before.length, s - before.length + inner.length); return; }
    replaceRange(s, e, before + inner + after, s + before.length, s + before.length + inner.length);
  };
  const lineRange = () => { const { s, e, v } = sel(); const ls = v.lastIndexOf('\n', s - 1) + 1; let le = v.indexOf('\n', e); if (le < 0) le = v.length; return { ls, le, text: v.slice(ls, le) }; };
  const mapLines = (fn) => { const { ls, le, text } = lineRange(); const out = text.split('\n').map(fn).join('\n'); replaceRange(ls, le, out, ls, ls + out.length); };
  const togglePrefix = (prefix, re) => {
    const all = lineRange().text.split('\n').every((l) => re.test(l));
    mapLines((l) => (all ? l.replace(re, '') : re.test(l) ? l : prefix + l));
  };
  const tools = {
    heading: () => mapLines((l) => { const m = /^(#{1,6})\s/.exec(l); if (!m) return '# ' + l; if (m[1].length >= 3) return l.slice(m[0].length); return '#' + l; }),
    bold: () => wrap('**', '**', 'bold'), italic: () => wrap('_', '_', 'italic'), strike: () => wrap('~~', '~~', 'text'), code: () => wrap('`', '`', 'code'),
    codeblock: () => { const { s, e, v } = sel(); const inner = v.slice(s, e); const lead = s > 0 && v[s - 1] !== '\n' ? '\n' : ''; replaceRange(s, e, `${lead}\`\`\`\n${inner}\n\`\`\``, s + lead.length + 4, s + lead.length + 4 + inner.length); },
    link: () => {
      const { s, e, v } = sel();
      const inner = v.slice(s, e);
      if (/^https?:\/\/\S+$/.test(inner)) { replaceRange(s, e, `[text](${inner})`, s + 1, s + 5); return; }
      const lbl = inner || 'text';
      const out = `[${lbl}](url)`;
      replaceRange(s, e, out, s + lbl.length + 3, s + lbl.length + 6);
    },
    quote: () => togglePrefix('> ', /^>\s?/), ul: () => togglePrefix('- ', /^\s*[-*+]\s(?!\[)/), task: () => togglePrefix('- [ ] ', /^\s*[-*+]\s\[[ xX]\]\s/),
    ol: () => { const all = lineRange().text.split('\n').every((l) => /^\s*\d+[.)]\s/.test(l)); mapLines((l, i) => (all ? l.replace(/^\s*\d+[.)]\s/, '') : /^\s*\d+[.)]\s/.test(l) ? l : `${i + 1}. ${l}`)); },
  };
  const dirty = value !== initial.current;
  const save = () => { if (onSave) onSave(value); };
  const cancel = async () => { if (!onCancel) return; if (dirty && !(await confirmDiscard('The text you typed here has not been saved.'))) return; onCancel(); };
  const onKeyDown = (e) => {
    const mod = e.ctrlKey || e.metaKey;
    if (mod && !e.shiftKey && !e.altKey) {
      const k = e.key.toLowerCase();
      if (k === 'b') { e.preventDefault(); e.stopPropagation(); tools.bold(); return; }
      if (k === 'i') { e.preventDefault(); e.stopPropagation(); tools.italic(); return; }
      if (k === 'k') { e.preventDefault(); e.stopPropagation(); tools.link(); return; }
      if (k === 'e') { e.preventDefault(); e.stopPropagation(); tools.code(); return; }
      if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); save(); return; }
      return;
    }
    if (e.key === 'Escape') { if (onCancel) { e.preventDefault(); e.stopPropagation(); cancel(); } return; }
    if (e.key === 'Enter' && !e.shiftKey && !e.altKey) {
      const { s, e: end, v } = sel();
      if (s !== end) return;
      const ls = v.lastIndexOf('\n', s - 1) + 1;
      const line = v.slice(ls, s);
      const m = LIST_RE.exec(line);
      if (!m) return;
      e.preventDefault();
      if (line.trim() === m[0].trim()) { replaceRange(ls, s, '', ls, ls); return; } // empty item ends the list
      const marker = /^\d+/.test(m[2]) ? String(Number(m[2]) + 1) + m[2].slice(-1) : m[2];
      const next = `\n${m[1]}${marker} ${m[3] ? '[ ] ' : ''}`;
      replaceRange(s, s, next, s + next.length, s + next.length);
      return;
    }
    if (e.key === 'Tab' && !e.altKey && !mod) {
      const { text } = lineRange();
      if (!text.split('\n').every((l) => LIST_RE.test(l))) return;
      e.preventDefault();
      const { s, e: end } = sel();
      const single = s === end;
      const before = lineRange().text.length;
      mapLines((l) => (e.shiftKey ? l.replace(/^ {1,2}/, '') : '  ' + l));
      if (single) { const delta = lineRange().text.length - before; ta.current.setSelectionRange(s + delta, s + delta); }
      return;
    }
    if (e.key === '`' && !mod && !e.altKey) {
      const { s, e: end, v } = sel();
      if (s !== end) { e.preventDefault(); wrap('`', '`'); return; }
      if (v[s] === '`') { e.preventDefault(); ta.current.setSelectionRange(s + 1, s + 1); return; }
      if (v[s - 1] === '`' && v[s - 2] === '`') return; // typing a fence
      e.preventDefault();
      replaceRange(s, s, '``', s + 1, s + 1);
    }
  };
  const preview = view === 'preview';
  return (
    <div className={cn('editor', className)} data-view={view} data-busy={busy ? '' : undefined} data-dirty={dirty ? '' : undefined}>
      <div className="editor-bar" role="toolbar" aria-label="Formatting">
        {TOOLS.map((t, i) => (t === null ? <span key={'s' + i} className="sep" aria-hidden="true" /> : (
          <Button key={t[0]} variant="ghost" size="icon-xs" aria-label={t[1] + (t[2] ? ` (${t[2]})` : '')} data-tip={t[1] + (t[2] ? `  ${t[2]}` : '')} tabIndex={-1}
            onMouseDown={(e) => e.preventDefault()} onClick={() => tools[t[0]]()}><Icon name={t[3] || t[0]} size={14} /></Button>
        )))}
        <Button variant="editor-tab" size="xs" aria-pressed={preview ? 'true' : 'false'} aria-label={preview ? 'Write' : 'Preview'} data-tip={preview ? 'Write' : 'Preview'}
          onMouseDown={(e) => e.preventDefault()} onClick={() => { setView(preview ? 'write' : 'preview'); if (preview) queueMicrotask(() => ta.current && ta.current.focus()); }}>
          <Icon name={preview ? 'pencil' : 'eye'} size={14} />{preview ? 'Write' : 'Preview'}
        </Button>
      </div>
      <div className="editor-body">
        <Textarea ref={ta} variant="editor" value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} aria-label={label} rows={rows} spellCheck autoComplete="off" onKeyDown={onKeyDown} />
        {preview && <div className="editor-preview" aria-live="polite" aria-label="Preview"><Markdown text={value} empty="Nothing to preview yet" /></div>}
      </div>
      {onSave && (
        <div className="editor-foot">
          <span className="phone-hide text-11 text-fg-3"><Kbd>{MOD}</Kbd> <Kbd>Enter</Kbd> saves{onCancel ? <> · <Kbd>Esc</Kbd> cancels</> : null}</span>
          <span className="flex-1" />
          {onCancel && <Button variant="ghost" size="sm" onClick={cancel}>Cancel</Button>}
          <Button variant="default" size="sm" onClick={save}>{saveLabel}</Button>
        </div>
      )}
    </div>
  );
}
