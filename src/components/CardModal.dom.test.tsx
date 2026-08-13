// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CardModal } from './CardModal';

const api = vi.hoisted(() => ({ similar: vi.fn(), provenance: vi.fn(), drift: vi.fn(), accept: vi.fn(), draftCancel: vi.fn(), comments: vi.fn() }));
vi.mock('../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/api')>();
  return { ...actual, getSimilar: api.similar, getImportProvenance: api.provenance, checkDrift: api.drift, acceptDrift: api.accept, getTaskComments: api.comments };
});
vi.mock('./CardEditor', () => ({
  CardEditor: (props: { title: string; onTitleChange: (s: string) => void; onBusyChange: (busy: boolean) => void; cancelRef: { current: (() => void) | null }; titleExtras: React.ReactNode; onClose: () => void }) => <div>{props.titleExtras}<span>{props.title}</span><button onClick={() => props.onTitleChange('duplicate title')}>type title</button><button onClick={() => { props.cancelRef.current = api.draftCancel; props.onBusyChange(true); }}>draft busy</button><button onClick={() => props.onBusyChange(false)}>draft idle</button><button onClick={props.onClose}>editor close</button></div>,
}));
const identity = { kind: 'manual' as const, id: 'alice' };
const task = { id: 'local', emoji: '', title: 'Imported', desc: '', status: 'todo' as const, blocked: false, prio: 3 as const, tags: ['link::gitlab#1'], checks: [], createdAt: '', movedAt: '' };
const provenance = { source: 'work', external_key: 'gitlab:org/repo#1', link: 'gitlab#1', url: 'https://gitlab/x/1', title: 'Imported' };
const drift = { state: 'drifted' as const, link: 'gitlab#1', url: 'https://gitlab/x/1', title_changed: true, upstream_title: 'New title', baseline_title: 'Old title', baseline_at: '2026-01-01T00:00:00Z', checked_at: '2026-08-01T00:00:00Z', summary: 'changed', revision: 'rev-1' };

describe('CardModal DOM', () => {
  beforeEach(() => { vi.clearAllMocks(); api.similar.mockResolvedValue([]); api.provenance.mockResolvedValue([provenance]); api.drift.mockResolvedValue(drift); api.accept.mockResolvedValue({ baseline_at: '2026-08-01T00:00:00Z' }); api.comments.mockResolvedValue([]); });

  it('renders read-only comments for an acknowledged task', async () => {
    api.comments.mockResolvedValue([
      { id: 1, author: 'alice', body: 'shipped in **v2**', createdAt: '2026-08-13T08:00:00Z' },
      { id: 2, author: 'bob', body: '- follow up', createdAt: 'garbage' },
    ]);
    render(<CardModal state={{ mode: 'edit', task }} identity={identity} canonicalTaskId="server-id" labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    const section = await screen.findByRole('region', { name: 'Comments' });
    await waitFor(() => expect(api.comments).toHaveBeenCalledWith(identity, 'server-id', expect.any(AbortSignal)));
    expect(section.querySelector('h3')?.textContent).toBe('2 comments');
    expect(screen.getByText('alice')).toBeTruthy();
    expect(screen.getByText('Aug 13, 2026')).toBeTruthy();
    // Markdown renders through the safe tokenizer, never raw HTML.
    expect(section.querySelector('strong')?.textContent).toBe('v2');
    expect(section.querySelector('.bullet')?.textContent).toContain('follow up');
    // A malformed timestamp renders as nothing rather than "Invalid Date".
    expect(section.textContent).not.toContain('Invalid');
  });

  it('shows no comments section for empty lists and never fetches without a canonical id', async () => {
    const withoutId = render(<CardModal state={{ mode: 'edit', task }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByRole('region', { name: 'Comments' })).toBeNull();
    withoutId.unmount();

    render(<CardModal state={{ mode: 'add', status: 'todo' }} identity={identity} canonicalTaskId="server-id" labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Comments' })).toBeNull());
    expect(api.comments).not.toHaveBeenCalled();
  });

  it('renders a singular comment heading', async () => {
    api.comments.mockResolvedValue([
      { id: 1, author: 'alice', body: 'only one', createdAt: '2026-08-13T08:00:00Z' },
    ]);
    render(<CardModal state={{ mode: 'edit', task }} identity={identity} canonicalTaskId="server-id" labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    const section = await screen.findByRole('region', { name: 'Comments' });
    expect(section.querySelector('h3')?.textContent).toBe('1 comment');
  });

  it('debounces deterministic similarity lookup and dismisses rows and panel', async () => {
    const user = userEvent.setup();
    api.similar.mockResolvedValue([{ id: 'other', title: 'Existing card', via: 'card' }]);
    render(<CardModal state={{ mode: 'add', status: 'todo' }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'type title' }));
    await waitFor(() => expect(api.similar).toHaveBeenCalledWith(identity, 'duplicate title', undefined, [], expect.any(AbortSignal)), { timeout: 1500 });
    expect(await screen.findByText(/1 similar item/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Dismiss Existing card' }));
    expect(screen.queryByText('Existing card')).toBeNull();
  });

  it('checks and accepts upstream drift entirely through mocked forge APIs', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<CardModal state={{ mode: 'edit', task }} identity={identity} canonicalTaskId="server-id" labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={onClose} />);
    await user.click(screen.getByRole('button', { name: 'Check upstream' }));
    await waitFor(() => expect(api.provenance).toHaveBeenCalledWith(identity, 'gitlab#1', expect.any(AbortSignal)));
    expect(await screen.findByText(/Upstream changed since/)).toBeTruthy();
    expect(screen.getByText(/Old title/).textContent).toContain('New title');
    await user.click(screen.getByRole('button', { name: 'Update card' }));
    await waitFor(() => expect(api.accept).toHaveBeenCalledWith(identity, 'work', 'gitlab:org/repo#1', 'rev-1', expect.any(AbortSignal)));
    expect(await screen.findByText(/Baseline updated/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'editor close' }));
    expect(onClose).toHaveBeenCalled();
  });

  it('handles missing and ambiguous provenance plus stale-baseline conflicts', async () => {
    const user = userEvent.setup();
    api.provenance.mockResolvedValueOnce([]);
    const first = render(<CardModal state={{ mode: 'edit', task }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Check upstream' }));
    expect((await screen.findByRole('alert')).textContent).toContain('import link not found');
    first.unmount();

    const secondCandidate = { ...provenance, external_key: 'gitlab:org/repo#2', title: 'Second' };
    api.provenance.mockResolvedValueOnce([provenance, secondCandidate]);
    api.drift.mockResolvedValueOnce(drift);
    const { DriftConflictError } = await import('../lib/api');
    api.accept.mockRejectedValueOnce(new DriftConflictError('stale'));
    render(<CardModal state={{ mode: 'edit', task }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Check upstream' }));
    expect(await screen.findByText(/Choose the imported issue/)).toBeTruthy();
    await user.selectOptions(screen.getByLabelText('Imported issue'), secondCandidate.external_key);
    await user.click(screen.getByRole('button', { name: 'Check selected' }));
    expect(await screen.findByText(/Upstream changed since/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Update card' }));
    expect((await screen.findByRole('alert')).textContent).toContain('changed again');
  });

  it('covers all similarity render variants, panel dismissal, short-title reset, Escape, and backdrop close', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    api.similar.mockResolvedValue([
      { id: 'killed', title: 'Rejected one', via: 'killed', killedAt: '2026-01-01T00:00:00Z', reason: 'not useful' },
      { link: 'gitlab#2', title: 'Imported one', via: 'import' },
    ]);
    render(<CardModal state={{ mode: 'add', status: 'doing' }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={onClose} />);
    await user.click(screen.getByRole('button', { name: 'type title' }));
    expect(await screen.findByText(/2 similar items/)).toBeTruthy();
    expect(screen.getByText('rejected')).toBeTruthy();
    expect(screen.getByText('imported')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Dismiss similar items' }));
    expect(screen.queryByText(/similar items/)).toBeNull();
    await user.click(screen.getByRole('button', { name: 'draft busy' }));
    await user.keyboard('{Escape}');
    expect(api.draftCancel).toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'draft idle' }));
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
    const backdrop = screen.getByRole('dialog').parentElement!;
    await user.pointer({ target: backdrop, keys: '[MouseLeft]' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('aborts drift checks and accept operations without publishing stale completion', async () => {
    const user = userEvent.setup();
    let rejectCheck!: (reason: unknown) => void;
    api.provenance.mockImplementationOnce((_identity, _link, signal: AbortSignal) => new Promise((_resolve, reject) => { rejectCheck = reject; signal.addEventListener('abort', () => reject(new DOMException('abort', 'AbortError'))); }));
    const first = render(<CardModal state={{ mode: 'edit', task }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Check upstream' }));
    expect(screen.getByRole('status').textContent).toContain('Checking upstream');
    await user.keyboard('{Escape}');
    rejectCheck(new DOMException('abort', 'AbortError'));
    await waitFor(() => expect(screen.queryByText('Checking upstream…')).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
    first.unmount();

    api.provenance.mockResolvedValueOnce([provenance]);
    api.drift.mockResolvedValueOnce(drift);
    let rejectAccept!: (reason: unknown) => void;
    api.accept.mockImplementationOnce((_i, _s, _k, _r, signal: AbortSignal) => new Promise((_resolve, reject) => { rejectAccept = reject; signal.addEventListener('abort', () => reject(new DOMException('abort', 'AbortError'))); }));
    render(<CardModal state={{ mode: 'edit', task }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Check upstream' }));
    await screen.findByRole('button', { name: 'Update card' });
    await user.click(screen.getByRole('button', { name: 'Update card' }));
    expect(screen.getByRole('status').textContent).toContain('Updating');
    await user.keyboard('{Escape}');
    rejectAccept(new DOMException('abort', 'AbortError'));
    await waitFor(() => expect(screen.queryByText(/Updating the comparison/)).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('renders body-only drift and unknown check/update failures with deterministic defaults', async () => {
    const user = userEvent.setup();
    api.provenance.mockRejectedValueOnce('bad check');
    const first = render(<CardModal state={{ mode: 'edit', task }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Check upstream' }));
    expect((await screen.findByRole('alert')).textContent).toContain('upstream check failed');
    first.unmount();

    api.provenance.mockResolvedValueOnce([provenance]);
    api.drift.mockResolvedValueOnce({ ...drift, title_changed: false, summary: '', revision: undefined });
    render(<CardModal state={{ mode: 'edit', task: { ...task, tags: ['link::gitlab#1', 'link::gitlab#2'] } }} identity={identity} labels={[]} onSave={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    await user.selectOptions(screen.getByLabelText('Upstream link'), 'gitlab#2');
    await user.click(screen.getByRole('button', { name: 'Check upstream' }));
    expect(await screen.findByText('The issue body changed; the title did not.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Update card' })).toBeNull();
  });
});
