import { useEffect, useRef, useState } from 'react';
import type { ComponentType } from 'react';

// Exactly what the codec reads back off a title line (see markdown.ts): one
// Extended_Pictographic code point plus an optional variation selector. The
// field is constrained to this so what the user sees is what round-trips —
// a longer sequence would be silently truncated on the wire, and trailing
// text would come back as title words.
const EMOJI_RE = /^\p{Extended_Pictographic}️?/u;

/**
 * The emoji token at the start of `s`, or '' when there is none. Typing an
 * emoji by hand goes through this, so hand-entry and the picker cannot
 * disagree about what the field may hold.
 */
export function firstEmoji(s: string): string {
  const m = s.trim().match(EMOJI_RE);
  return m ? m[0] : '';
}

/**
 * Why `s` cannot be stored, or '' when it can. The codec's token is ONE
 * Extended_Pictographic code point (plus an optional variation selector),
 * which is narrower than "a single emoji grapheme": a flag is a pair of
 * regional indicators, a keycap is digit + VS16 + U+20E3, and ZWJ sequences
 * and skin tones run to several code points. Keeping only the leading piece
 * of those would store something other than what the user picked — 👨‍💻 as 👨,
 * 🇯🇵 as nothing at all — so they are refused with a reason instead. Widening
 * this means widening both codecs in lockstep (src/lib/markdown.ts and
 * internal/board/markdown.go), which is a wire-format change.
 */
export function emojiRejection(s: string): string {
  const trimmed = s.trim();
  if (trimmed === '' || firstEmoji(trimmed) === trimmed) return '';
  return `“${trimmed}” can't be used here: the board format stores a single emoji character, so flags, keycaps, skin tones and multi-part sequences are not supported.`;
}

interface PickerModule {
  Picker: ComponentType<Record<string, unknown>>;
  data: unknown;
}

let pickerPromise: Promise<PickerModule> | null = null;

/**
 * emoji-mart and its ~1.5MB emoji table are pulled in by dynamic import the
 * first time the picker opens, so they land in their own chunk and the board
 * bundle does not carry them. Bundled by Vite — nothing is fetched from a CDN,
 * the binary stays self-contained. Cached for the page's life.
 */
function loadPicker(): Promise<PickerModule> {
  pickerPromise ??= (async () => {
    const [picker, data] = await Promise.all([
      import('@emoji-mart/react'),
      // The package ships only type declarations, so its default export (the
      // emoji table itself) is invisible to TS.
      import('@emoji-mart/data') as unknown as Promise<{ default: unknown }>,
    ]);
    return {
      Picker: picker.default as ComponentType<Record<string, unknown>>,
      data: data.default,
    };
  })();
  return pickerPromise;
}

/**
 * Emoji per row. The picker's grid is a fixed number of columns wide, not a
 * reflowing one, so on a narrow phone the default 9 is wider than the modal
 * and the last column would be cut off by the frame around it. 7 fits.
 */
export function perLine(width: number): number {
  return width < 480 ? 7 : 9;
}

export interface EmojiFieldProps {
  inputId: string;
  value: string;
  onChange: (emoji: string) => void;
}

/**
 * Emoji chooser: the current emoji, as a button that opens a lazily loaded
 * emoji-mart popover. Deliberately not a text field — an input invites
 * typing, and everything typeable here is either already in the picker or is
 * something the wire format refuses.
 */
export function EmojiField({ inputId, value, onChange }: EmojiFieldProps) {
  const [open, setOpen] = useState(false);
  const [picker, setPicker] = useState<PickerModule | null>(null);
  const [failed, setFailed] = useState(false);
  // Why the last pick was not taken, or '' — the picker offers flags, keycaps
  // and ZWJ sequences the codec cannot store, and choosing one used to close
  // the popover and write nothing.
  const [reason, setReason] = useState('');
  const wrapRef = useRef<HTMLDivElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  // Set while an outside pointerdown is closing the popover. The field's
  // <label> is "outside" (it sits beside this component) and a label forwards
  // its click to the button, so the close would be followed by a reopen.
  const closingRef = useRef(false);

  /**
   * Close the popover and put focus back on the button that opened it —
   * otherwise Escape (or picking an emoji) drops focus onto <body>, inside a
   * dialog the user is still filling in.
   */
  const close = () => {
    setOpen(false);
    btnRef.current?.focus();
  };

  /** Take `raw` if the codec can store it, otherwise explain why not. */
  const apply = (raw: string) => {
    const why = emojiRejection(raw);
    setReason(why);
    if (!why) onChange(firstEmoji(raw));
  };

  useEffect(() => {
    if (!open || picker) return;
    let cancelled = false;
    loadPicker().then(
      (m) => {
        if (!cancelled) setPicker(m);
      },
      () => {
        // Nothing to fall back to now that the field is selector-only; the
        // emoji is optional, and the card saves fine without one.
        if (!cancelled) setFailed(true);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [open, picker]);

  useEffect(() => {
    if (!open) return;
    // Capture phase on both: Escape must close the picker without also
    // closing the card modal (which listens on window while bubbling), and
    // an outside press must not reach the backdrop and dismiss the modal.
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.stopPropagation();
      close();
    };
    const onDown = (e: PointerEvent) => {
      if (wrapRef.current?.contains(e.target as Node)) return;
      e.stopPropagation();
      // A press elsewhere is already choosing where focus goes; don't steal it
      // back to the field.
      closingRef.current = true;
      setTimeout(() => {
        closingRef.current = false;
      }, 0);
      setOpen(false);
    };
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('pointerdown', onDown, true);
    return () => {
      window.removeEventListener('keydown', onKey, true);
      window.removeEventListener('pointerdown', onDown, true);
    };
  }, [open]);

  return (
    <div className="emojifield" ref={wrapRef}>
      {/* The current emoji IS the control: pressing it opens the picker. A
          button rather than an input, so it is operable by pointer and
          keyboard alike with no text-entry affordance to mislead. Empty
          reads as a prompt, not as a value. */}
      <button
        ref={btnRef}
        id={inputId}
        type="button"
        className={value ? 'emojibtn' : 'emojibtn empty'}
        aria-haspopup="dialog"
        aria-expanded={open}
        // The glyph alone is the whole label, and "none" has no glyph at all.
        aria-label={value ? `Emoji: ${value}. Choose another` : 'Choose an emoji'}
        title="Choose an emoji"
        onClick={() => {
          if (closingRef.current) return; // see closingRef
          setOpen((o) => !o);
        }}
        aria-describedby={reason ? `${inputId}-note` : undefined}
      >
        {value || '+'}
      </button>
      {open && (
        // The role and name deliver what the trigger's aria-haspopup="dialog"
        // promised; without them the popup announced as nothing at all.
        <div className="emojipop" role="dialog" aria-label="Emoji picker">
          {picker ? (
            <picker.Picker
              // Passing `data` is what keeps the picker offline: without it
              // emoji-mart fetches the table from a CDN at runtime.
              data={picker.data}
              autoFocus
              // kb is one light paper palette and has no dark variant, so the
              // picker is pinned rather than left on 'auto' — which would go
              // dark inside a light form whenever the OS is dark.
              theme="light"
              perLine={perLine(window.innerWidth)}
              previewPosition="none"
              // A skin-tone modifier is a second code point, which the title
              // line cannot carry — don't offer what would be dropped.
              skinTonePosition="none"
              onEmojiSelect={(e: { native?: string }) => {
                // Closed either way, so a refusal is not hidden behind the
                // popover that caused it.
                apply(e.native ?? '');
                close();
              }}
            />
          ) : (
            <p className="mnote">
              {failed
                ? 'picker unavailable — the emoji is optional, save without one'
                : 'loading…'}
            </p>
          )}
          {/* The picker can set an emoji but never unset one, and a card that
              has one would otherwise be stuck with it. */}
          {value && picker && (
            <div className="emojifoot">
              <button
                type="button"
                onClick={() => {
                  setReason('');
                  onChange('');
                  close();
                }}
              >
                Remove emoji
              </button>
            </div>
          )}
        </div>
      )}
      {reason && (
        <p className="emojinote" id={`${inputId}-note`} role="alert">
          {reason}
        </p>
      )}
    </div>
  );
}
