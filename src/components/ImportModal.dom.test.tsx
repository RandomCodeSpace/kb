// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ImportModal } from './ImportModal';

const source = { name: 'work', kind: 'github' as const, base_url: 'github.com', has_token: true };
const draft = { title: 'Imported', emoji: '📥', desc: 'desc', prio: 2 as const, due: '', effort: 'M' as const, tags: ['link::github#1'], checks: [], link: 'github#1', external_key: 'github:org/repo#1', url: 'https://github.com/org/repo/issues/1' };
const preview = { kind: 'issue' as const, total_hint: 2, fetched: 1, truncated: true, note: 'review it', drafts: [draft] };

describe('ImportModal DOM', () => {
  it('fetches a mocked forge preview, edits, and commits cards plus provenance', async () => {
    const user = userEvent.setup();
    const onPreview = vi.fn().mockResolvedValue(preview), onAdd = vi.fn(), onCommitLinks = vi.fn();
    render(<ImportModal sources={[source]} onPreview={onPreview} onAdd={onAdd} onCommitLinks={onCommitLinks} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText(/Issue, project/), ' org/repo ');
    await user.click(screen.getByRole('button', { name: 'Fetch' }));
    expect(await screen.findByText(/showing first 1 of 2/)).toBeTruthy();
    expect(onPreview.mock.calls[0][0]).toEqual({ source: 'work', ref: 'org/repo', max: 8 });
    await user.clear(screen.getByLabelText('Card title'));
    await user.type(screen.getByLabelText('Card title'), 'Reviewed import');
    await user.click(screen.getByRole('button', { name: 'Add selected (1)' }));
    expect(onAdd.mock.calls[0][0][0].title).toBe('Reviewed import');
    expect(onCommitLinks).toHaveBeenCalledWith({ source: 'work', items: [expect.objectContaining({ external_key: 'github:org/repo#1', title: 'Reviewed import' })] });
  });

  it('cancels stale requests and reports empty or unknown failures', async () => {
    const user = userEvent.setup();
    let resolve!: (value: typeof preview) => void;
    const onPreview = vi.fn((_req, signal: AbortSignal) => new Promise<typeof preview>((r) => { resolve = r; signal.addEventListener('abort', () => undefined); }));
    render(<ImportModal sources={[source]} onPreview={onPreview} onAdd={vi.fn()} onCommitLinks={vi.fn()} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText(/Issue, project/), 'org/repo');
    await user.click(screen.getByRole('button', { name: 'Fetch' }));
    await user.click(screen.getByRole('button', { name: 'Cancel fetch' }));
    expect(onPreview.mock.calls[0][1].aborted).toBe(true);
    resolve(preview);
    await waitFor(() => expect(screen.queryByText(/AI transformations/)).toBeNull());
    cleanup();

    const { unmount } = render(<ImportModal sources={[source]} onPreview={vi.fn().mockResolvedValue({ ...preview, drafts: [] })} onAdd={vi.fn()} onCommitLinks={vi.fn()} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText(/Issue, project/), 'none');
    await user.click(screen.getByRole('button', { name: 'Fetch' }));
    expect((await screen.findByRole('alert')).textContent).toContain('no usable cards');
    unmount();
  });

  it('keeps exact duplicates unselected, supports back, and reports request failures', async () => {
    const user = userEvent.setup();
    const duplicate = { ...draft, duplicate_of: { id: 'old', title: 'Already here', via: 'link' as const } };
    const onPreview = vi.fn().mockResolvedValue({ ...preview, truncated: false, note: '', drafts: [duplicate] });
    const { unmount } = render(<ImportModal sources={[source]} onPreview={onPreview} onAdd={vi.fn()} onCommitLinks={vi.fn()} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText(/Issue, project/), 'org/repo');
    await user.click(screen.getByRole('button', { name: 'Fetch' }));
    expect(await screen.findByText(/already on the board/)).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Add selected (0)' }) as HTMLButtonElement).disabled).toBe(true);
    await user.click(screen.getByLabelText(/Include/));
    expect((screen.getByRole('button', { name: 'Add selected (1)' }) as HTMLButtonElement).disabled).toBe(false);
    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByRole('button', { name: 'Fetch' })).toBeTruthy();
    unmount();

    render(<ImportModal sources={[source]} onPreview={vi.fn().mockRejectedValue('bad')} onAdd={vi.fn()} onCommitLinks={vi.fn()} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText(/Issue, project/), 'broken');
    await user.click(screen.getByRole('button', { name: 'Fetch' }));
    expect((await screen.findByRole('alert')).textContent).toContain('import preview failed');
  });

  it('covers empty sources, Escape idle/busy, abort errors, backdrop, max clamp, destination, and cards without provenance', async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    const empty = render(<ImportModal sources={[]} onPreview={vi.fn()} onAdd={vi.fn()} onCommitLinks={vi.fn()} onClose={close} />);
    expect((screen.getByRole('button', { name: 'Fetch' }) as HTMLButtonElement).disabled).toBe(true);
    await user.keyboard('{Escape}');
    expect(close).toHaveBeenCalled();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'x' }));
    empty.unmount();

    const aborting = vi.fn().mockRejectedValue(new DOMException('abort', 'AbortError'));
    render(<ImportModal sources={[source]} onPreview={aborting} onAdd={vi.fn()} onCommitLinks={vi.fn()} onClose={close} />);
    const max = screen.getByLabelText('Max cards');
    await user.clear(max); await user.type(max, '99'); await user.tab();
    expect((max as HTMLInputElement).value).toBe('20');
    await user.type(screen.getByLabelText(/Issue, project/), 'org/repo');
    await user.click(screen.getByRole('button', { name: 'Fetch' }));
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
    cleanup();

    const noLink = { ...draft, link: undefined, external_key: undefined, url: undefined };
    const onAdd = vi.fn(), onCommitLinks = vi.fn();
    render(<ImportModal sources={[source]} onPreview={vi.fn().mockResolvedValue({ ...preview, drafts: [noLink] })} onAdd={onAdd} onCommitLinks={onCommitLinks} onClose={close} />);
    await user.type(screen.getByLabelText(/Issue, project/), 'org/repo');
    await user.click(screen.getByRole('button', { name: 'Fetch' }));
    await user.selectOptions(await screen.findByLabelText('Add to column'), 'done');
    await user.click(screen.getByRole('button', { name: 'Add selected (1)' }));
    expect(onAdd.mock.calls[0]![0][0].status).toBe('done');
    expect(onCommitLinks).not.toHaveBeenCalled();
    const backdrop = screen.getByRole('dialog').parentElement!;
    await user.pointer({ target: backdrop, keys: '[MouseLeft]' });
    expect(close).toHaveBeenCalledTimes(2);
  });
});
