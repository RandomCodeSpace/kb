import { describe, expect, it } from 'vitest';
import { CONFETTI_COLORS } from './confetti';

/**
 * Label chips (`.tag`, `.slabel .v` in styles.css) take a generated fill from
 * this palette and print ink on it. White used to be the chip colour and
 * measured 1.83:1 on the amber; ink clears the 4.5:1 minimum on every entry.
 * A new palette colour dark enough to break that would silently reintroduce
 * the failure, so the invariant is asserted here rather than left to a
 * screenshot.
 */
const INK = '#20242c';

function channel(hex: string, i: number): number {
  const c = parseInt(hex.slice(1 + i * 2, 3 + i * 2), 16) / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function luminance(hex: string): number {
  return 0.2126 * channel(hex, 0) + 0.7152 * channel(hex, 1) + 0.0722 * channel(hex, 2);
}

export function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

describe('label chip palette', () => {
  it('reads as ink at WCAG AA on every generated fill', () => {
    for (const fill of CONFETTI_COLORS) {
      expect(contrast(INK, fill), `ink on ${fill}`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it('measures the known ratios', () => {
    expect(+contrast(INK, '#ffb020').toFixed(2)).toBe(8.51);
    expect(+contrast(INK, '#3f9d58').toFixed(2)).toBe(4.57);
    // …and the white it replaced does not clear the minimum.
    expect(contrast('#ffffff', '#ffb020')).toBeLessThan(4.5);
  });
});
