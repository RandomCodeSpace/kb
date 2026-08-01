// @vitest-environment jsdom
import { act, cleanup, render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { DebugOverlay } from './DebugOverlay';

const confetti = vi.hoisted(() => ({ cap: vi.fn() }));
vi.mock('../lib/confetti', () => ({ setFrameCap: confetti.cap }));

describe('DebugOverlay DOM', () => {
  it('changes and resets the frame cap and closes by button or Escape', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { unmount } = render(<DebugOverlay onClose={onClose} inert />);
    const overlay = screen.getByRole('complementary', { name: 'Debug overlay' });
    expect(overlay.hasAttribute('inert')).toBe(true);
    await user.selectOptions(screen.getByLabelText('Frame cap'), '90');
    expect(confetti.cap).toHaveBeenLastCalledWith(90);
    fireEvent.keyDown(overlay, { key: 'Escape' });
    await user.click(screen.getByRole('button', { name: 'Hide debug overlay' }));
    expect(onClose).toHaveBeenCalledTimes(2);
    unmount();
    expect(confetti.cap).toHaveBeenLastCalledWith(null);
  });

  it('moves aside when focus would be obscured', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    const { container } = render(<><button>target</button><DebugOverlay onClose={vi.fn()} /></>);
    const overlay = screen.getByRole('complementary', { name: 'Debug overlay' });
    Object.defineProperty(overlay, 'getBoundingClientRect', { configurable: true, value: () => ({ width: 200, height: 200 }) });
    const target = screen.getByRole('button', { name: 'target' });
    Object.defineProperty(target, 'getBoundingClientRect', { configurable: true, value: () => ({ left: 900, right: 1000, top: 600, bottom: 650 }) });
    target.focus();
    fireEvent.focusIn(target);
    await waitFor(() => expect((container.querySelector('aside') as HTMLElement).style.top).toBe('12px'));
  });

  it('reports available renderers and calculates FPS across a full rolling window', async () => {
    Object.defineProperty(navigator, 'gpu', { configurable: true, value: {} });
    const loseContext = vi.fn();
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ getExtension: () => ({ loseContext }) } as unknown as WebGL2RenderingContext);
    const callbacks: FrameRequestCallback[] = [];
    vi.stubGlobal('requestAnimationFrame', vi.fn((callback: FrameRequestCallback) => { callbacks.push(callback); return callbacks.length; }));
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    vi.spyOn(performance, 'now').mockReturnValue(0);
    render(<DebugOverlay onClose={vi.fn()} />);
    expect(screen.getAllByText('available')).toHaveLength(2);
    expect(loseContext).toHaveBeenCalled();
    await act(async () => {
      for (let i = 1; i <= 61; i++) callbacks.shift()?.(i * 5);
    });
    expect(screen.getByText('200 fps').textContent).toBe('200 fps');
    fireEvent.keyDown(screen.getByRole('complementary'), { key: 'x' });
    cleanup();
    Reflect.deleteProperty(navigator, 'gpu');
    vi.unstubAllGlobals();
  });

  it('survives a renderer probe exception, keeps focus-contained checks inert, and returns to uncapped', async () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => { throw new Error('no gpu'); });
    const user = userEvent.setup();
    render(<DebugOverlay onClose={vi.fn()} />);
    expect(screen.getAllByText('unavailable')).toHaveLength(2);
    const overlay = screen.getByRole('complementary');
    const close = screen.getByRole('button', { name: 'Hide debug overlay' });
    close.focus();
    fireEvent.focusIn(close);
    expect((overlay as HTMLElement).style.top).toBe('');
    await user.selectOptions(screen.getByLabelText('Frame cap'), '120');
    await user.selectOptions(screen.getByLabelText('Frame cap'), 'uncapped');
    expect(confetti.cap).toHaveBeenLastCalledWith(null);
  });
});
