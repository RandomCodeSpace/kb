// @vitest-environment jsdom
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Confetti } from './Confetti';

const loop = vi.hoisted(() => ({ start: vi.fn(), stop: vi.fn() }));
vi.mock('../lib/confetti', () => ({ startLoop: loop.start }));

describe('Confetti DOM', () => {
  it('starts the loop with the canvas and stops it on unmount', () => {
    loop.start.mockReturnValue(loop.stop);
    const { container, unmount } = render(<Confetti />);
    const canvas = container.querySelector('canvas')!;
    expect(canvas.getAttribute('aria-hidden')).toBe('true');
    expect(loop.start).toHaveBeenCalledWith(canvas);
    unmount();
    expect(loop.stop).toHaveBeenCalled();
  });
});
