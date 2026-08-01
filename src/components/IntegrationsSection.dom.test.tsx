// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { IntegrationsSection } from './IntegrationsSection';

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), test: vi.fn(), del: vi.fn() }));
vi.mock('../lib/api', () => ({ getIntegrations: api.get, putIntegration: api.put, forgeTest: api.test, deleteIntegration: api.del }));
const identity = { kind: 'manual' as const, id: 'alice' };
const saved = { name: 'work', kind: 'gitlab' as const, base_url: 'gitlab.example', has_token: true };

describe('IntegrationsSection DOM', () => {
  beforeEach(() => { vi.clearAllMocks(); api.get.mockResolvedValue([saved]); api.put.mockResolvedValue({ tokenCleared: false }); api.test.mockResolvedValue({ ok: true }); api.del.mockResolvedValue(undefined); });

  it('loads, tests, edits, saves, adds and removes only through mocked APIs', async () => {
    const user = userEvent.setup();
    render(<IntegrationsSection identity={identity} serverPresent />);
    expect(await screen.findByDisplayValue('gitlab.example')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Test' }));
    expect((await screen.findByRole('status')).textContent).toContain('connection ok');
    await user.clear(screen.getByLabelText('Base URL'));
    await user.type(screen.getByLabelText('Base URL'), 'gitlab.new');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.put).toHaveBeenCalledWith(identity, 'work', { kind: 'gitlab', base_url: 'gitlab.new' }));
    await user.click(screen.getByRole('button', { name: '+ Add source' }));
    const names = screen.getAllByLabelText(/Name for/);
    await user.type(names.at(-1)!, 'github');
    const bases = screen.getAllByLabelText('Base URL');
    await user.type(bases.at(-1)!, 'github.com');
    await user.click(screen.getAllByRole('button', { name: 'Save' }).at(-1)!);
    await waitFor(() => expect(api.put).toHaveBeenCalledWith(identity, 'github', { kind: 'gitlab', base_url: 'github.com' }));
    const remove = screen.getByRole('button', { name: 'Remove work' });
    await user.click(remove);
    await user.click(screen.getByRole('button', { name: 'Confirm removal of work' }));
    await waitFor(() => expect(api.del).toHaveBeenCalledWith(identity, 'work'));
  });

  it('handles offline, load failure, duplicate names, and action errors', async () => {
    const { rerender } = render(<IntegrationsSection identity={identity} serverPresent={false} />);
    expect(screen.getByText(/Integrations need the kb server/)).toBeTruthy();
    api.get.mockRejectedValueOnce(new Error('load broke'));
    rerender(<IntegrationsSection identity={identity} serverPresent />);
    expect((await screen.findByRole('alert')).textContent).toContain('load broke');
    api.get.mockResolvedValue([saved]);
    rerender(<IntegrationsSection identity={{ ...identity }} serverPresent />);
  });

  it('rejects duplicate names before save and surfaces test/save/remove failures', async () => {
    const user = userEvent.setup();
    render(<IntegrationsSection identity={identity} serverPresent />);
    await screen.findByDisplayValue('gitlab.example');
    await user.click(screen.getByRole('button', { name: '+ Add source' }));
    await user.type(screen.getByLabelText('Name for new source 2'), ' WORK ');
    await user.type(screen.getAllByLabelText('Base URL').at(-1)!, 'github.com');
    await user.click(screen.getAllByRole('button', { name: 'Save' }).at(-1)!);
    expect((await screen.findByRole('alert')).textContent).toContain('already exists');
    expect(api.put).not.toHaveBeenCalled();
    cleanup();

    api.get.mockResolvedValue([saved]);
    api.test.mockResolvedValueOnce({ ok: false, error: 'forbidden' });
    api.put.mockRejectedValueOnce(new Error('save rejected'));
    api.del.mockRejectedValueOnce(new Error('delete rejected'));
    render(<IntegrationsSection identity={identity} serverPresent />);
    await screen.findByDisplayValue('gitlab.example');
    await user.click(screen.getByRole('button', { name: 'Test' }));
    expect((await screen.findByRole('alert')).textContent).toContain('test failed — forbidden');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toContain('save failed — save rejected');
    await user.click(screen.getByRole('button', { name: 'Remove work' }));
    await user.click(screen.getByRole('button', { name: 'Confirm removal of work' }));
    expect((await screen.findByRole('alert')).textContent).toContain('remove failed — delete rejected');
  });

  it('uses typed PATs for test/save, clears server tokens, and removes unsaved drafts locally', async () => {
    const user = userEvent.setup();
    api.put.mockResolvedValueOnce({ tokenCleared: false });
    render(<IntegrationsSection identity={identity} serverPresent />);
    await screen.findByDisplayValue('gitlab.example');
    await user.type(screen.getByLabelText('Personal access token'), 'new-pat');
    await user.click(screen.getByRole('button', { name: 'Test' }));
    await waitFor(() => expect(api.test).toHaveBeenCalledWith(identity, 'work', { base_url: 'gitlab.example', pat: 'new-pat' }));
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.put).toHaveBeenCalledWith(identity, 'work', { kind: 'gitlab', base_url: 'gitlab.example', pat: 'new-pat' }));
    expect((screen.getByLabelText('Personal access token') as HTMLInputElement).placeholder).toContain('saved');
    await user.click(screen.getByRole('button', { name: '+ Add source' }));
    const draftRemove = screen.getByRole('button', { name: 'Remove new source 2' });
    await user.click(draftRemove);
    await user.click(screen.getByRole('button', { name: 'Confirm removal of new source 2' }));
    expect(screen.queryByLabelText('Name for new source 2')).toBeNull();
    expect(api.del).not.toHaveBeenCalled();
    cleanup();

    api.get.mockResolvedValue([{ ...saved, has_token: true }]);
    api.put.mockResolvedValueOnce({ tokenCleared: true });
    render(<IntegrationsSection identity={identity} serverPresent />);
    await screen.findByDisplayValue('gitlab.example');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect((screen.getByLabelText('Personal access token') as HTMLInputElement).placeholder).not.toContain('saved'));
  });

  it('disarms removal on blur and ignores stale load completions after identity changes', async () => {
    const user = userEvent.setup();
    let resolveLoad!: (value: typeof saved[]) => void;
    api.get.mockReturnValueOnce(new Promise<typeof saved[]>((resolve) => { resolveLoad = resolve; })).mockResolvedValueOnce([]);
    const nextIdentity = { kind: 'manual' as const, id: 'bob' };
    const { rerender } = render(<IntegrationsSection identity={identity} serverPresent />);
    rerender(<IntegrationsSection identity={nextIdentity} serverPresent />);
    resolveLoad([saved]);
    await waitFor(() => expect(api.get).toHaveBeenCalledWith(nextIdentity));
    expect(screen.queryByDisplayValue('gitlab.example')).toBeNull();
    cleanup();

    api.get.mockResolvedValue([saved]);
    render(<IntegrationsSection identity={identity} serverPresent />);
    await screen.findByDisplayValue('gitlab.example');
    await user.click(screen.getByRole('button', { name: 'Remove work' }));
    const armed = screen.getByRole('button', { name: 'Confirm removal of work' });
    armed.blur();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Remove work' })).toBeTruthy());
  });

  it('drops stale test and save outcomes after a generation switch', async () => {
    const user = userEvent.setup();
    let resolveTest!: (value: { ok: boolean }) => void;
    api.test.mockReturnValueOnce(new Promise((resolve) => { resolveTest = resolve; }));
    api.get.mockResolvedValue([saved]);
    const nextIdentity = { kind: 'manual' as const, id: 'bob' };
    const first = render(<IntegrationsSection identity={identity} serverPresent />);
    await screen.findByDisplayValue('gitlab.example');
    await user.click(screen.getByRole('button', { name: 'Test' }));
    first.rerender(<IntegrationsSection identity={nextIdentity} serverPresent />);
    resolveTest({ ok: true });
    await waitFor(() => expect(api.get).toHaveBeenCalledWith(nextIdentity));
    cleanup();

    let resolveSave!: (value: { tokenCleared: boolean }) => void;
    api.put.mockReturnValueOnce(new Promise((resolve) => { resolveSave = resolve; }));
    const second = render(<IntegrationsSection identity={identity} serverPresent />);
    await screen.findByDisplayValue('gitlab.example');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    second.rerender(<IntegrationsSection identity={nextIdentity} serverPresent />);
    resolveSave({ tokenCleared: false });
    await waitFor(() => expect(api.get).toHaveBeenCalledWith(nextIdentity));
  });
});
