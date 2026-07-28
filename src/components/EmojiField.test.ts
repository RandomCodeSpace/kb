import { describe, expect, it } from 'vitest';
import { emojiRejection, firstEmoji, perLine } from './EmojiField';

describe('firstEmoji', () => {
  it('keeps a hand-typed emoji', () => {
    expect(firstEmoji('🔧')).toBe('🔧');
  });

  it('keeps the variation selector the codec also accepts', () => {
    expect(firstEmoji('⚙️')).toBe('⚙️');
  });

  it('drops everything after the first emoji', () => {
    expect(firstEmoji('🔧🔨')).toBe('🔧');
    expect(firstEmoji('🔧 fix the thing')).toBe('🔧');
  });

  it('rejects plain text, which the title line would swallow', () => {
    expect(firstEmoji('nope')).toBe('');
    expect(firstEmoji('#tag')).toBe('');
    expect(firstEmoji('')).toBe('');
  });

  it('ignores surrounding whitespace', () => {
    expect(firstEmoji('  ✅  ')).toBe('✅');
  });

  it('is idempotent, so re-editing a saved card cannot drift', () => {
    const once = firstEmoji('👋 wave');
    expect(firstEmoji(once)).toBe(once);
  });
});

describe('emojiRejection', () => {
  it('accepts what the codec stores unchanged', () => {
    for (const ok of ['', '  ', '🔧', '⚙️', '  ✅  ']) {
      expect(emojiRejection(ok)).toBe('');
    }
  });

  // The picker ships Flags and People, so every one of these is reachable by
  // clicking: before, the popover closed and the field silently stayed empty
  // (or kept only the first code point of a ZWJ sequence).
  it('explains itself instead of silently dropping or truncating', () => {
    for (const bad of ['🇯🇵', '1️⃣', '#️⃣', '👨‍💻', '🏳️‍🌈', '🧑‍🚀', '👍🏽', '🔧🔨', 'nope']) {
      expect(emojiRejection(bad)).not.toBe('');
    }
  });
});

describe('perLine', () => {
  // The picker's grid is a fixed column count, not a reflowing one: at the
  // default 9 it is wider than the modal on a phone, and the frame around it
  // would cut the last column off.
  it('narrows the grid on a phone-width viewport', () => {
    expect(perLine(375)).toBe(7);
    expect(perLine(479)).toBe(7);
  });

  it('keeps the full grid everywhere it fits', () => {
    expect(perLine(480)).toBe(9);
    expect(perLine(1280)).toBe(9);
  });
});
