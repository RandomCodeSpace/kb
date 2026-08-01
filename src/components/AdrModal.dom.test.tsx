// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { AdrModal } from './AdrModal';

const source = { name: 'work', kind: 'gitlab' as const, base_url: 'gitlab.example', has_token: true };
const story = { title: 'First story', emoji: '🔧', desc: 'desc', prio: 3 as const, due: '', effort: '' as const, tags: ['type::chore'], checks: [] };

describe('AdrModal DOM', () => {
  it('validates, proposes, edits, goes back, and adds selected stories', async () => {
    const user = userEvent.setup();
    const onSplit = vi.fn().mockResolvedValue([story]);
    const onAdd = vi.fn();
    render(<AdrModal sources={[source]} onSplit={onSplit} onAdd={onAdd} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText('Architecture decision record (markdown)'), '# ADR');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    expect(await screen.findByDisplayValue('First story')).toBeTruthy();
    expect(onSplit.mock.calls[0][0]).toEqual({ adr: '# ADR', max: 8 });
    await user.clear(screen.getByLabelText('Story title'));
    await user.type(screen.getByLabelText('Story title'), 'Reviewed');
    await user.selectOptions(screen.getByLabelText('Add to column'), 'doing');
    await user.click(screen.getByRole('button', { name: 'Add selected (1)' }));
    expect(onAdd.mock.calls[0][0][0]).toMatchObject({ title: 'Reviewed', status: 'doing' });
    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByLabelText('Architecture decision record (markdown)')).toBeTruthy();
  });

  it('handles empty, errors, cancellation, and unmount abort without live LLM or forge calls', async () => {
    const user = userEvent.setup();
    let resolve!: (value: typeof story[]) => void;
    const onSplit = vi.fn((_req, signal: AbortSignal) => new Promise<typeof story[]>((r, reject) => { resolve = r; signal.addEventListener('abort', () => reject(new DOMException('abort', 'AbortError'))); }));
    const close = vi.fn();
    const { unmount } = render(<AdrModal sources={[source]} onSplit={onSplit} onAdd={vi.fn()} onClose={close} />);
    await user.type(screen.getByLabelText('Forge issue URL'), 'https://gitlab.example/x/1');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    expect(screen.getByRole('status').textContent).toContain('Splitting');
    await user.click(screen.getByRole('button', { name: 'Cancel split' }));
    await waitFor(() => expect(onSplit.mock.calls[0][1].aborted).toBe(true));
    unmount();
    resolve([]);

    const empty = vi.fn().mockResolvedValue([]);
    render(<AdrModal sources={[source]} onSplit={empty} onAdd={vi.fn()} onClose={close} />);
    await user.type(screen.getByLabelText('Architecture decision record (markdown)'), 'ADR');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    expect((await screen.findByRole('alert')).textContent).toContain('no usable stories');
  });

  it('enforces the byte ceiling, clamps count, reports failures, and reads uploads', async () => {
    const user = userEvent.setup();
    const fail = vi.fn().mockRejectedValue(new Error('LLM unavailable'));
    const { unmount } = render(<AdrModal sources={[source]} onSplit={fail} onAdd={vi.fn()} onClose={vi.fn()} />);
    const adr = screen.getByLabelText('Architecture decision record (markdown)');
    fireEvent.change(adr, { target: { value: 'a'.repeat(65_537) } });
    expect(screen.getByRole('alert').textContent).toContain('over 64 KiB');
    await user.clear(adr); await user.type(adr, 'small');
    const max = screen.getByLabelText('Max stories');
    await user.clear(max); await user.type(max, '99'); await user.tab();
    expect((max as HTMLInputElement).value).toBe('20');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    expect((await screen.findByRole('alert')).textContent).toContain('LLM unavailable');
    unmount();

    const split = vi.fn().mockResolvedValue([story]);
    render(<AdrModal sources={[source]} onSplit={split} onAdd={vi.fn()} onClose={vi.fn()} />);
    const file = new File(['# uploaded'], 'adr.md', { type: 'text/markdown' });
    Object.defineProperty(file, 'text', { value: () => Promise.resolve('# uploaded') });
    await user.upload(screen.getByLabelText('…or upload a file'), file);
    expect((screen.getByLabelText('Architecture decision record (markdown)') as HTMLTextAreaElement).value).toBe('# uploaded');
  });

  it('covers empty-source validation, source arrival, file failure, Escape, backdrop, and multi-row edits', async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    const { rerender, unmount } = render(<AdrModal sources={[]} onSplit={vi.fn()} onAdd={vi.fn()} onClose={close} />);
    await user.type(screen.getByLabelText('Forge issue URL'), 'https://example/1');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    expect((await screen.findByRole('alert')).textContent).toContain('select a forge source');
    rerender(<AdrModal sources={[source]} onSplit={vi.fn()} onAdd={vi.fn()} onClose={close} />);
    expect((screen.getByLabelText('Source') as HTMLSelectElement).value).toBe('work');
    await user.keyboard('{Escape}');
    expect(close).toHaveBeenCalled();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'x' }));
    const backdrop = screen.getByRole('dialog').parentElement!;
    await user.pointer({ target: backdrop, keys: '[MouseLeft]' });
    expect(close).toHaveBeenCalledTimes(2);
    const broken = new File(['x'], 'broken.md');
    Object.defineProperty(broken, 'text', { value: () => Promise.reject(new Error('read')) });
    await user.clear(screen.getByLabelText('Forge issue URL'));
    await user.upload(screen.getByLabelText('…or upload a file'), broken);
    expect((await screen.findByRole('alert')).textContent).toContain('could not read');
    unmount();

    const split = vi.fn().mockResolvedValue([story, { ...story, title: 'Second' }]);
    render(<AdrModal sources={[source, { ...source, name: 'other' }]} onSplit={split} onAdd={vi.fn()} onClose={close} />);
    await user.selectOptions(screen.getByLabelText('Source'), 'other');
    await user.type(screen.getByLabelText('Architecture decision record (markdown)'), 'ADR');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    const includes = await screen.findAllByLabelText(/Include/);
    await user.click(includes[1]!);
    await user.selectOptions(screen.getAllByLabelText('Priority')[0]!, '1');
    await user.selectOptions(screen.getAllByLabelText('Effort')[0]!, 'L');
    expect(screen.getByRole('button', { name: 'Add selected (1)' })).toBeTruthy();
  });

  it('aborts an active split with Escape and maps unknown failures to the fallback', async () => {
    const user = userEvent.setup();
    let reject!: (reason: unknown) => void;
    const onSplit = vi.fn((_req, signal: AbortSignal) => new Promise<typeof story[]>((_resolve, r) => { reject = r; signal.addEventListener('abort', () => r(new DOMException('abort', 'AbortError'))); }));
    const first = render(<AdrModal sources={[source]} onSplit={onSplit} onAdd={vi.fn()} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText('Architecture decision record (markdown)'), 'ADR');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    await user.keyboard('{Escape}');
    expect(onSplit.mock.calls[0]![1].aborted).toBe(true);
    reject(new DOMException('abort', 'AbortError'));
    first.unmount();

    render(<AdrModal sources={[source]} onSplit={vi.fn().mockRejectedValue('bad')} onAdd={vi.fn()} onClose={vi.fn()} />);
    await user.type(screen.getByLabelText('Architecture decision record (markdown)'), 'ADR');
    await user.click(screen.getByRole('button', { name: 'Propose stories' }));
    expect((await screen.findByRole('alert')).textContent).toContain('split failed');
  });
});
