import { useId, useRef, useState } from 'react';
import type { KeyboardEvent } from 'react';
import { isScoped } from '../lib/model';
import { addTags, filterLabels, tagColor } from '../lib/labels';

export interface LabelsComboboxProps {
  inputId: string;
  value: string[];
  suggestions: string[];
  onChange: (tags: string[]) => void;
}

/**
 * Chip editor for tags: existing chips removable, free text allowed (Enter),
 * suggestion dropdown filtered from known labels. Keyboard: arrows move the
 * highlight, Enter adds, Escape closes the dropdown (not the modal),
 * Backspace on an empty input removes the last chip.
 */
export function LabelsCombobox({
  inputId,
  value,
  suggestions,
  onChange,
}: LabelsComboboxProps) {
  const [text, setText] = useState('');
  const [open, setOpen] = useState(false);
  const [hi, setHi] = useState(0);
  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  const filtered = filterLabels(suggestions, value, text);
  const showDrop = open && filtered.length > 0;
  const hiIdx = Math.min(hi, filtered.length - 1);

  const add = (raw: string) => {
    const next = addTags(value, raw);
    if (next.length !== value.length) onChange(next);
    setText('');
    setHi(0);
  };

  const remove = (tag: string) => {
    onChange(value.filter((t) => t !== tag));
    inputRef.current?.focus();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!showDrop) {
        setOpen(true);
        setHi(0);
      } else {
        setHi((hiIdx + 1) % filtered.length);
      }
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      if (showDrop) setHi((hiIdx - 1 + filtered.length) % filtered.length);
    } else if (e.key === 'Enter') {
      if (showDrop) {
        e.preventDefault();
        add(filtered[hiIdx]);
      } else if (text.trim() !== '') {
        e.preventDefault();
        add(text);
      }
      // Empty input, no dropdown: fall through so the form can submit.
    } else if (e.key === 'Escape') {
      if (open) {
        // Consume it: close only the dropdown, keep the modal open.
        e.preventDefault();
        e.stopPropagation();
        setOpen(false);
      }
    } else if (e.key === 'Backspace' && text === '' && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  return (
    <div className="labelbox">
      <div
        className="labelchips"
        onPointerDown={(e) => {
          // Click on the container (not a chip/input) focuses the input.
          if (e.target === e.currentTarget) {
            e.preventDefault();
            inputRef.current?.focus();
          }
        }}
      >
        {value.map((tag) =>
          isScoped(tag) ? (
            <span key={tag} className="slabel">
              <span className="k">{tag.split('::')[0]}</span>
              <span className="v" style={{ background: tagColor(tag) }}>
                {tag.split('::').slice(1).join('::')}
                <button
                  type="button"
                  className="rm"
                  aria-label={`Remove ${tag}`}
                  onClick={() => remove(tag)}
                >
                  ×
                </button>
              </span>
            </span>
          ) : (
            <span key={tag} className="tag" style={{ background: tagColor(tag) }}>
              #{tag}
              <button
                type="button"
                className="rm"
                aria-label={`Remove ${tag}`}
                onClick={() => remove(tag)}
              >
                ×
              </button>
            </span>
          ),
        )}
        <input
          ref={inputRef}
          id={inputId}
          value={text}
          role="combobox"
          aria-expanded={showDrop}
          // Only while the list exists: a reference to an unmounted id is a
          // broken one, and some screen readers report it as such.
          aria-controls={showDrop ? listId : undefined}
          aria-autocomplete="list"
          aria-activedescendant={showDrop ? `${listId}-${hiIdx}` : undefined}
          autoComplete="off"
          placeholder={value.length === 0 ? 'infra env::prod' : ''}
          onChange={(e) => {
            setText(e.target.value);
            setOpen(true);
            setHi(0);
          }}
          onFocus={() => setOpen(true)}
          onBlur={() => setOpen(false)}
          onKeyDown={onKeyDown}
        />
      </div>
      {/* Mounted for the whole focus, hidden (not unmounted) when nothing
          matches: remounting on the empty↔non-empty boundary replayed the
          entrance animation on ordinary keystrokes. visibility keeps the
          animation's one run intact; display or a remount would restart it. */}
      {open && (
        <ul
          className="labeldrop"
          id={listId}
          role="listbox"
          aria-label="Label suggestions"
          style={filtered.length === 0 ? { visibility: 'hidden' } : undefined}
        >
          {filtered.map((l, i) => (
            <li
              key={l}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === hiIdx}
              className={i === hiIdx ? 'hi' : undefined}
              onMouseEnter={() => setHi(i)}
              onPointerDown={(e) => {
                // preventDefault keeps focus (and the dropdown) on the input.
                e.preventDefault();
                add(l);
              }}
            >
              {l}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
