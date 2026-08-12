// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SettingsModal } from './SettingsModal';

const settings = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), test: vi.fn() }));
vi.mock('../lib/settings', () => ({ getSettings: settings.get, putSettings: settings.put, aiTest: settings.test }));
vi.mock('./IntegrationsSection', () => ({ IntegrationsSection: () => <section>mock integrations</section> }));
const identity = { kind: 'manual' as const, id: 'alice' };

describe('SettingsModal DOM', () => {
  beforeEach(() => { vi.clearAllMocks(); settings.get.mockResolvedValue({ ai_base_url: 'https://llm.local/v1', ai_model: 'small', has_key: true }); settings.put.mockResolvedValue({ keyCleared: false }); settings.test.mockResolvedValue({ ok: true }); });

  it('loads, tests unsaved values, saves, toggles debug, and closes', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn(), onDebugChange = vi.fn(), onClose = vi.fn();
    render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={onDebugChange} onClose={onClose} onSaved={onSaved} />);
    const base = await screen.findByDisplayValue('https://llm.local/v1');
    await user.clear(base); await user.type(base, ' https://other/v1 ');
    await user.clear(screen.getByLabelText('Model')); await user.type(screen.getByLabelText('Model'), ' newer ');
    await user.type(screen.getByLabelText('API key'), 'secret');
    await user.click(screen.getByRole('button', { name: 'Test connection' }));
    expect(await screen.findByText('✓ connection ok')).toBeTruthy();
    expect(settings.test.mock.calls[0][1]).toEqual({ ai_base_url: 'https://other/v1', ai_model: 'newer', ai_key: 'secret' });
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ ai_base_url: 'https://other/v1', ai_model: 'newer', has_key: true }));
    await user.click(screen.getByLabelText('Show debug overlay'));
    expect(onDebugChange).toHaveBeenCalledWith(true);
  });

  it('cancels a test, reports save/load failures, and supports offline close', async () => {
    const user = userEvent.setup();
    settings.test.mockImplementation((_i, _p, signal: AbortSignal) => new Promise((_r, reject) => signal.addEventListener('abort', () => reject(new DOMException('abort', 'AbortError')))));
    const onClose = vi.fn();
    const { rerender } = render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={vi.fn()} onClose={onClose} onSaved={vi.fn()} />);
    await screen.findByDisplayValue('small');
    await user.click(screen.getByRole('button', { name: 'Test connection' }));
    await user.click(screen.getByRole('button', { name: 'Cancel test' }));
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
    settings.put.mockRejectedValueOnce('bad');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toContain('save failed');
    rerender(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent={false} debug={false} onDebugChange={vi.fn()} onClose={onClose} onSaved={vi.fn()} />);
    expect(screen.getByText(/AI drafting needs the kb server/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalled();
  });

  it('shows load and negative test errors and reports a key cleared by host change', async () => {
    const user = userEvent.setup();
    settings.get.mockRejectedValueOnce(new Error('offline'));
    render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={vi.fn()} onClose={vi.fn()} onSaved={vi.fn()} />);
    expect((await screen.findByRole('alert')).textContent).toContain('could not load settings');
    cleanup();

    settings.get.mockResolvedValue({ ai_base_url: 'https://old/v1', ai_model: 'm', has_key: true });
    settings.test.mockResolvedValueOnce({ ok: false, error: 'model missing' });
    settings.put.mockResolvedValueOnce({ keyCleared: true });
    const onSaved = vi.fn();
    render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={vi.fn()} onClose={vi.fn()} onSaved={onSaved} />);
    await screen.findByDisplayValue('https://old/v1');
    await user.click(screen.getByRole('button', { name: 'Test connection' }));
    expect((await screen.findByRole('alert')).textContent).toContain('model missing');
    await user.clear(screen.getByLabelText('AI base URL')); await user.type(screen.getByLabelText('AI base URL'), 'https://new/v1');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/re-enter your API key/)).toBeTruthy();
    expect(onSaved).toHaveBeenCalledWith({ ai_base_url: 'https://new/v1', ai_model: 'm', has_key: false });
  });

  it('routes Escape for idle, testing, and saving states and blocks a busy backdrop', async () => {
    const user = userEvent.setup();
    let rejectTest!: (reason: unknown) => void;
    settings.test.mockImplementationOnce((_i, _p, signal: AbortSignal) => new Promise((_resolve, reject) => { rejectTest = reject; signal.addEventListener('abort', () => reject(new DOMException('abort', 'AbortError'))); }));
    const onClose = vi.fn();
    const first = render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={vi.fn()} onClose={onClose} onSaved={vi.fn()} />);
    await screen.findByDisplayValue('small');
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'x' }));
    await user.click(screen.getByRole('button', { name: 'Test connection' }));
    await user.keyboard('{Escape}');
    rejectTest(new DOMException('abort', 'AbortError'));
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
    expect(onClose).toHaveBeenCalledTimes(1);
    first.unmount();

    let resolveSave!: (value: { keyCleared: boolean }) => void;
    settings.put.mockReturnValueOnce(new Promise((resolve) => { resolveSave = resolve; }));
    render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={vi.fn()} onClose={onClose} onSaved={vi.fn()} />);
    await screen.findByDisplayValue('small');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await user.keyboard('{Escape}');
    const backdrop = screen.getByRole('dialog').parentElement!;
    await user.pointer({ target: backdrop, keys: '[MouseLeft]' });
    expect(onClose).toHaveBeenCalledTimes(1);
    resolveSave({ keyCleared: false });
    await screen.findByText('saved');
    await user.pointer({ target: backdrop, keys: '[MouseLeft]' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('ignores stale loads and covers default test/save failures', async () => {
    let resolveLoad!: (value: { ai_base_url: string; ai_model: string; has_key: boolean }) => void;
    settings.get.mockReturnValueOnce(new Promise((resolve) => { resolveLoad = resolve; }));
    const stale = render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={vi.fn()} onClose={vi.fn()} onSaved={vi.fn()} />);
    stale.unmount();
    resolveLoad({ ai_base_url: 'late', ai_model: 'late', has_key: false });
    await Promise.resolve();

    settings.get.mockResolvedValue({ ai_base_url: '', ai_model: '', has_key: false });
    settings.test.mockResolvedValueOnce({ ok: false });
    settings.put.mockRejectedValueOnce(new Error('save exploded'));
    render(<SettingsModal identity={identity} displayNameValue="" onDisplayNameChange={() => {}} serverPresent debug={false} onDebugChange={vi.fn()} onClose={vi.fn()} onSaved={vi.fn()} />);
    await waitFor(() => expect((screen.getByRole('button', { name: 'Test connection' }) as HTMLButtonElement).disabled).toBe(false));
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }));
    expect((await screen.findByRole('alert')).textContent).toContain('connection failed');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toContain('save exploded');
  });

  it('edits the device-local display name outside the AI form', async () => {
    const user = userEvent.setup();
    const onDisplayNameChange = vi.fn();
    render(<SettingsModal identity={identity} displayNameValue="Board Goblin" onDisplayNameChange={onDisplayNameChange} serverPresent={false} debug={false} onDebugChange={vi.fn()} onClose={vi.fn()} onSaved={vi.fn()} />);
    // Present even offline: the name is a device preference, not AI config.
    const field = screen.getByLabelText('Display name');
    expect((field as HTMLInputElement).value).toBe('Board Goblin');
    await user.type(field, '!');
    expect(onDisplayNameChange).toHaveBeenCalledWith('Board Goblin!');
  });
});
