// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { StrictMode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { IdentityGate } from './IdentityGate';

const auth = vi.hoisted(() => ({ azureAvailable: vi.fn(), signInAzure: vi.fn() }));
vi.mock('../lib/auth', () => ({ ...auth }));

describe('IdentityGate DOM', () => {
  beforeEach(() => vi.clearAllMocks());

  it('submits a trimmed manual identity with an optional token', async () => {
    auth.azureAvailable.mockResolvedValue(false);
    const user = userEvent.setup();
    const onIdentity = vi.fn();
    render(<IdentityGate onIdentity={onIdentity} />);
    await user.type(screen.getByLabelText(/your unique id/), ' alice@example.com ');
    await user.type(screen.getByLabelText(/server token/), ' secret ');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(onIdentity).toHaveBeenCalledWith({ kind: 'manual', id: 'alice@example.com', serverToken: 'secret' });
    expect(await screen.findByText(/not configured/)).toBeTruthy();
  });

  it('rejects unsafe manual ids and handles Azure success and failure', async () => {
    auth.azureAvailable.mockResolvedValue(true);
    auth.signInAzure.mockResolvedValue({ kind: 'azure', id: 'azure-user', accessToken: 'token' });
    const user = userEvent.setup();
    const onIdentity = vi.fn();
    render(<IdentityGate onIdentity={onIdentity} />);
    const id = screen.getByLabelText(/your unique id/);
    await user.type(id, '.hidden');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByRole('alert').textContent).toContain('no leading dot');
    const azure = screen.getByRole('button', { name: 'Sign in with Microsoft' });
    await waitFor(() => expect((azure as HTMLButtonElement).disabled).toBe(false));
    await user.click(azure);
    await waitFor(() => expect(onIdentity).toHaveBeenCalledWith(expect.objectContaining({ kind: 'azure' })));

    auth.signInAzure.mockRejectedValueOnce('bad');
    await user.click(azure);
    expect((await screen.findByRole('alert')).textContent).toContain('Sign-in failed');
  });

  it('ignores Azure availability from a cleaned-up StrictMode effect', async () => {
    const availability = Array.from({ length: 2 }, () => {
      let resolve!: (value: boolean) => void;
      const promise = new Promise<boolean>((done) => { resolve = done; });
      return { promise, resolve };
    });
    auth.azureAvailable
      .mockReturnValueOnce(availability[0].promise)
      .mockReturnValueOnce(availability[1].promise);

    render(
      <StrictMode>
        <IdentityGate onIdentity={vi.fn()} />
      </StrictMode>,
    );
    await waitFor(() => expect(auth.azureAvailable).toHaveBeenCalledTimes(2));
    const azure = screen.getByRole('button', { name: 'Sign in with Microsoft' });

    await act(async () => availability[0].resolve(true));
    expect(azure).toBeDisabled();

    await act(async () => availability[1].resolve(true));
    await waitFor(() => expect(azure).toBeEnabled());
  });

  it('handles availability rejection, provider Error, blank submit, invalid characters, and token omission', async () => {
    auth.azureAvailable.mockRejectedValueOnce(new Error('config down'));
    auth.signInAzure.mockRejectedValueOnce(new Error('popup blocked'));
    const user = userEvent.setup();
    const onIdentity = vi.fn();
    render(<IdentityGate onIdentity={onIdentity} />);
    expect(await screen.findByText(/not configured/)).toBeTruthy();
    fireEvent.submit(screen.getByLabelText(/your unique id/).closest('form')!);
    expect(onIdentity).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText(/your unique id/), 'bad space');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByRole('alert')).toBeTruthy();
    await user.clear(screen.getByLabelText(/your unique id/));
    await user.type(screen.getByLabelText(/your unique id/), 'clean-id');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(onIdentity).toHaveBeenCalledWith({ kind: 'manual', id: 'clean-id' });

    auth.azureAvailable.mockResolvedValue(true);
    const second = render(<IdentityGate onIdentity={onIdentity} />);
    const azure = screen.getAllByRole('button', { name: 'Sign in with Microsoft' }).at(-1)!;
    await waitFor(() => expect((azure as HTMLButtonElement).disabled).toBe(false));
    await user.click(azure);
    expect((await screen.findAllByRole('alert')).at(-1)!.textContent).toContain('popup blocked');
    second.unmount();
  });
});
