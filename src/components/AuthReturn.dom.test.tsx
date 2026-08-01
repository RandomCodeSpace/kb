// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthReturn } from './AuthReturn';

const bridge = vi.hoisted(() => ({ broadcast: vi.fn() }));
vi.mock('@azure/msal-browser/redirect-bridge', () => ({ broadcastResponseToMainFrame: bridge.broadcast }));

describe('AuthReturn DOM', () => {
  beforeEach(() => { vi.useFakeTimers(); bridge.broadcast.mockReset().mockResolvedValue(undefined); });
  afterEach(() => vi.useRealTimers());

  it('shows the Web-platform diagnosis immediately', () => {
    render(<AuthReturn search="?code=x&state=y" hash="" />);
    expect(screen.getByRole('heading').textContent).toBe('Sign-in cannot complete');
    expect(screen.getByText(/Single-page application/)).toBeTruthy();
  });

  it('shows provider errors and a useful stall diagnosis', async () => {
    const { rerender } = render(<AuthReturn search="" hash="#error=denied&error_description=no" />);
    expect(screen.getByRole('heading').textContent).toBe('Sign-in failed');
    rerender(<AuthReturn search="" hash="#code=x&state=y" />);
    await act(async () => vi.advanceTimersByTime(12_000));
    expect(screen.getByText(/taking longer/)).toBeTruthy();
    expect(screen.getByText(/Response location:/).textContent).toContain('fragment');
  });

  it('surfaces bridge failures and ignores late failure after unmount', async () => {
    bridge.broadcast.mockRejectedValueOnce(new Error('bridge exploded'));
    render(<AuthReturn />);
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(screen.getByText('bridge exploded').textContent).toBe('bridge exploded');
    let reject!: (reason: unknown) => void;
    bridge.broadcast.mockReturnValueOnce(new Promise((_, r) => { reject = r; }));
    const { unmount } = render(<AuthReturn />);
    unmount();
    reject(new Error('late'));
    await act(async () => Promise.resolve());
  });

  it('stringifies non-Error bridge failures and diagnoses an unknown stalled location', async () => {
    bridge.broadcast.mockRejectedValueOnce('bridge string');
    render(<AuthReturn search="" hash="" />);
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(screen.getByText('bridge string')).toBeTruthy();
    bridge.broadcast.mockResolvedValueOnce(undefined);
    const second = render(<AuthReturn search="" hash="" />);
    await act(async () => vi.advanceTimersByTime(12_000));
    expect(second.container.textContent).toContain('Response location: unknown');
  });
});
