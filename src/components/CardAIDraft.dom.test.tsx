// @vitest-environment jsdom
import { createRef } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { CardAIDraft } from './CardAIDraft';

const draft = { title: 'AI title', emoji: '✨', desc: 'desc', prio: 2 as const, due: '', effort: 'S' as const, tags: ['ai'], checks: [] };
const base = { state: { mode: 'add' as const, status: 'todo' as const }, title: '', desc: '', prio: 3 as const, due: '', effort: '' as const, tags: [], checks: [], onApply: vi.fn(), onBusyChange: vi.fn(), cancelRef: createRef<(() => void) | null>() };

describe('CardAIDraft DOM', () => {
  it('drafts create and update requests and applies the result', async () => {
    const user = userEvent.setup();
    const aiDraft = vi.fn().mockResolvedValue(draft);
    const onApply = vi.fn();
    const { rerender } = render(<CardAIDraft {...base} aiDraft={aiDraft} onApply={onApply} />);
    await user.type(screen.getByLabelText('✨ Draft with AI'), ' build it ');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith(draft));
    expect(aiDraft.mock.calls[0][0]).toEqual({ mode: 'create', prompt: 'build it' });

    rerender(<CardAIDraft {...base} state={{ mode: 'edit', task: { id: '1', title: 'old', emoji: '', desc: '', status: 'todo', blocked: false, prio: 3, tags: [], checks: [], createdAt: '', movedAt: '' } }} title="old" aiDraft={aiDraft} onApply={onApply} />);
    await user.clear(screen.getByLabelText('✨ Draft with AI'));
    await user.type(screen.getByLabelText('✨ Draft with AI'), 'change');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    await waitFor(() => expect(aiDraft).toHaveBeenCalledTimes(2));
    expect(aiDraft.mock.calls[1][0]).toMatchObject({ mode: 'update', prompt: 'change', task: { title: 'old' } });
  });

  it('shows errors, ignores aborts, cancels, and aborts on unmount', async () => {
    const user = userEvent.setup();
    let reject!: (reason: unknown) => void;
    let seenSignal: AbortSignal | undefined;
    const aiDraft = vi.fn((_req: unknown, signal?: AbortSignal) => new Promise<typeof draft>((_, r) => { seenSignal = signal; reject = r; }));
    const cancelRef = createRef<(() => void) | null>();
    const { unmount } = render(<CardAIDraft {...base} cancelRef={cancelRef} aiDraft={aiDraft} />);
    await user.type(screen.getByLabelText('✨ Draft with AI'), 'work');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    expect(screen.getByRole('status').textContent).toContain('Drafting');
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(seenSignal?.aborted).toBe(true);
    reject(new DOMException('aborted', 'AbortError'));
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
    unmount();
    expect(cancelRef.current).toBeNull();

    const failing = vi.fn().mockRejectedValue('bad');
    render(<CardAIDraft {...base} aiDraft={failing} />);
    await user.type(screen.getByLabelText('✨ Draft with AI'), 'fail');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    expect((await screen.findByRole('alert')).textContent).toContain('draft failed');
  });

  it('reports Error messages and keeps a forced blank prompt inert', async () => {
    const user = userEvent.setup();
    const failing = vi.fn().mockRejectedValue(new Error('model offline'));
    const { unmount } = render(<CardAIDraft {...base} aiDraft={failing} />);
    await user.type(screen.getByLabelText('✨ Draft with AI'), 'fail');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    expect((await screen.findByRole('alert')).textContent).toContain('model offline');
    unmount();
    const never = vi.fn();
    render(<CardAIDraft {...base} aiDraft={never} />);
    const button = screen.getByRole('button', { name: 'Draft' });
    button.removeAttribute('disabled');
    fireEvent.click(button);
    expect(never).not.toHaveBeenCalled();
  });

  it('preserves add/edit prompts and reports every busy transition', async () => {
    const user = userEvent.setup();
    let resolveDraft!: (value: typeof draft) => void;
    const aiDraft = vi.fn(() => new Promise<typeof draft>((resolve) => { resolveDraft = resolve; }));
    const onBusyChange = vi.fn();
    const { rerender } = render(
      <CardAIDraft {...base} aiDraft={aiDraft} onBusyChange={onBusyChange} />,
    );

    const prompt = screen.getByLabelText('✨ Draft with AI');
    expect(prompt.getAttribute('placeholder')).toBe('Describe the task to draft…');
    await user.type(prompt, 'draft this');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    await waitFor(() => expect(onBusyChange).toHaveBeenLastCalledWith(true));
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    resolveDraft(draft);
    await waitFor(() => expect(onBusyChange).toHaveBeenLastCalledWith(false));
    expect(onBusyChange.mock.calls.map(([busy]) => busy)).toEqual([false, true, false]);

    rerender(
      <CardAIDraft
        {...base}
        state={{ mode: 'edit', task: { id: '1', title: 'old', emoji: '', desc: '', status: 'todo', blocked: false, prio: 3, tags: [], checks: [], createdAt: '', movedAt: '' } }}
        aiDraft={aiDraft}
        onBusyChange={onBusyChange}
      />,
    );
    expect(prompt.getAttribute('placeholder')).toBe('How should this card change?');
    expect(screen.getByText('fills the form below — review, then Save')).toBeTruthy();
  });
});
