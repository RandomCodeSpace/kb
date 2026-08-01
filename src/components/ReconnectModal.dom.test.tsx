// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ReconnectModal } from './ReconnectModal';

const mocks = vi.hoisted(() => ({ getLabels: vi.fn(), signInAzure: vi.fn() }));
vi.mock('../lib/api', () => ({ getLabels: mocks.getLabels }));
vi.mock('../lib/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/auth')>();
  return { ...actual, signInAzure: mocks.signInAzure };
});

describe('ReconnectModal DOM', () => {
  beforeEach(() => vi.clearAllMocks());

  it('validates a manual token before adopting it', async () => {
    mocks.getLabels.mockResolvedValue([]);
    const user = userEvent.setup();
    const onIdentity = vi.fn();
    render(<ReconnectModal identity={{ kind: 'manual', id: 'alice' }} onIdentity={onIdentity} onSignOut={vi.fn()} onClose={vi.fn()} />);
    const button = screen.getByRole('button', { name: 'Reconnect' }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    await user.type(screen.getByLabelText('server token'), ' secret ');
    await user.click(button);
    await waitFor(() => expect(onIdentity).toHaveBeenCalledWith({ kind: 'manual', id: 'alice', serverToken: 'secret' }));
    expect(mocks.getLabels).toHaveBeenCalledWith(expect.objectContaining({ serverToken: 'secret' }));
  });

  it('reports credential and network failures and remains dismissable when idle', async () => {
    const { ReauthRequiredError } = await import('../lib/auth');
    mocks.getLabels.mockRejectedValueOnce(new ReauthRequiredError()).mockRejectedValueOnce(new TypeError('Failed to fetch'));
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<ReconnectModal identity={{ kind: 'manual', id: 'alice' }} onIdentity={vi.fn()} onSignOut={vi.fn()} onClose={onClose} />);
    const input = screen.getByLabelText('server token');
    await user.type(input, 'bad');
    await user.click(screen.getByRole('button', { name: 'Reconnect' }));
    expect((await screen.findByRole('alert')).textContent).toContain('did not accept');
    await user.click(screen.getByRole('button', { name: 'Reconnect' }));
    expect((await screen.findByRole('alert')).textContent).toContain('could not reach');
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });

  it('reauthenticates Azure without live provider or server calls', async () => {
    mocks.signInAzure.mockResolvedValue({ kind: 'azure', id: 'a', homeAccountId: 'fresh' });
    mocks.getLabels.mockResolvedValue([]);
    const user = userEvent.setup();
    const onIdentity = vi.fn(), onSignOut = vi.fn(), onClose = vi.fn();
    render(<ReconnectModal identity={{ kind: 'azure', id: 'a', homeAccountId: 'old' }} onIdentity={onIdentity} onSignOut={onSignOut} onClose={onClose} />);
    await user.click(screen.getByRole('button', { name: 'Sign out' }));
    await user.click(screen.getByRole('button', { name: 'Work offline' }));
    await user.click(screen.getByRole('button', { name: 'Sign in again' }));
    await waitFor(() => expect(onIdentity).toHaveBeenCalledWith(expect.objectContaining({ homeAccountId: 'fresh' })));
    expect(onSignOut).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('blocks Escape/backdrop while Azure is busy and surfaces Error and fallback failures', async () => {
    const user = userEvent.setup();
    let reject!: (reason: unknown) => void;
    mocks.signInAzure.mockReturnValueOnce(new Promise((_resolve, r) => { reject = r; }));
    const onClose = vi.fn();
    const first = render(<ReconnectModal identity={{ kind: 'azure', id: 'a', homeAccountId: 'old' }} onIdentity={vi.fn()} onSignOut={vi.fn()} onClose={onClose} />);
    await user.click(screen.getByRole('button', { name: 'Sign in again' }));
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    fireEvent.pointerDown(screen.getByRole('dialog').parentElement!);
    expect(onClose).not.toHaveBeenCalled();
    reject(new Error('provider down'));
    expect((await screen.findByRole('alert')).textContent).toContain('provider down');
    first.unmount();

    mocks.signInAzure.mockRejectedValueOnce('bad');
    render(<ReconnectModal identity={{ kind: 'azure', id: 'a', homeAccountId: 'old' }} onIdentity={vi.fn()} onSignOut={vi.fn()} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Sign in again' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Sign-in failed');
  });

  it('ignores an empty manual submit and covers non-Escape dialog keys', () => {
    const onIdentity = vi.fn();
    render(<ReconnectModal identity={{ kind: 'manual', id: 'a' }} onIdentity={onIdentity} onSignOut={vi.fn()} onClose={vi.fn()} />);
    fireEvent.submit(screen.getByLabelText('server token').closest('form')!);
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'x' });
    expect(onIdentity).not.toHaveBeenCalled();
  });
});
