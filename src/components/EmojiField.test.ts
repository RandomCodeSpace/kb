import { describe, expect, it } from 'vitest';
import { emojiRejection, firstEmoji } from './EmojiField';

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
