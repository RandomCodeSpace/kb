// @vitest-environment jsdom
import { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { CardEditor } from './CardEditor';
import type { CardSave } from './CardEditor';

const task = { id: 't1', emoji: '🔧', title: 'Original', desc: 'old', status: 'todo' as const, blocked: false, prio: 3 as const, due: '2026-08-10', effort: 'M' as const, tags: ['type::bug'], checks: [{ text: 'done check', done: true }, { text: 'open check', done: false }], createdAt: '2026-01-01T00:00:00Z', movedAt: '2026-01-01T00:00:00Z' };

function Harness(props: { mode: 'add' | 'edit'; onSave: (save: CardSave) => void; onDelete?: (id: string) => void; onClose?: () => void }) {
  const [title, setTitle] = useState(props.mode === 'edit' ? task.title : '');
  return <CardEditor state={props.mode === 'edit' ? { mode: 'edit', task } : { mode: 'add', status: 'doing' }} labels={['type::bug', 'env::prod']} title={title} onTitleChange={setTitle} onBusyChange={vi.fn()} cancelRef={{ current: null }} titleExtras={<p>extras</p>} onSave={props.onSave} onDelete={props.onDelete ?? vi.fn()} onClose={props.onClose ?? vi.fn()} />;
}

function AIHarness(props: { onSave: (save: CardSave) => void; aiDraft: (req: import('../lib/api').AIStoryRequest, signal?: AbortSignal) => Promise<import('../lib/api').StoryDraft> }) {
  const [title, setTitle] = useState('seed');
  return <CardEditor state={{ mode: 'add', status: 'todo' }} labels={[]} aiDraft={props.aiDraft} title={title} onTitleChange={setTitle} onBusyChange={vi.fn()} cancelRef={{ current: null }} titleExtras={null} onSave={props.onSave} onDelete={vi.fn()} onClose={vi.fn()} />;
}

describe('CardEditor DOM', () => {
  it('edits every core field, parses checklist lines, saves, deletes, and cancels', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn(), onDelete = vi.fn(), onClose = vi.fn();
    render(<Harness mode="edit" onSave={onSave} onDelete={onDelete} onClose={onClose} />);
    await user.clear(screen.getByLabelText('Title')); await user.type(screen.getByLabelText('Title'), ' Reviewed ');
    await user.clear(screen.getByLabelText('Description')); await user.type(screen.getByLabelText('Description'), ' updated ');
    await user.selectOptions(screen.getByLabelText('Priority'), '1');
    await user.selectOptions(screen.getByLabelText('Effort'), 'L');
    await user.click(screen.getByLabelText(/Blocked/));
    await user.clear(screen.getByLabelText(/Checklist/)); await user.type(screen.getByLabelText(/Checklist/), 'x shipped\nnext');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).toHaveBeenCalledWith({ mode: 'edit', taskId: 't1', patch: expect.objectContaining({ title: 'Reviewed', desc: 'updated', blocked: true, prio: 1, effort: 'L', checks: [{ text: 'shipped', done: true }, { text: 'next', done: false }] }) });
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onDelete).toHaveBeenCalledWith('t1');
    expect(onClose).toHaveBeenCalled();
  });

  it('reports dirty on the first edit, not on mount', async () => {
    const user = userEvent.setup();
    const onDirty = vi.fn();
    render(
      <CardEditor
        state={{ mode: 'edit', task }}
        labels={[]}
        title={task.title}
        onTitleChange={vi.fn()}
        onBusyChange={vi.fn()}
        cancelRef={{ current: null }}
        titleExtras={null}
        onSave={vi.fn()}
        onDelete={vi.fn()}
        onClose={vi.fn()}
        onDirty={onDirty}
      />,
    );
    expect(onDirty).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText('Description'), '!');
    expect(onDirty).toHaveBeenCalled();
  });

  it('requires a title and creates a task in the requested column', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    render(<Harness mode="add" onSave={onSave} />);
    const save = screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await user.type(screen.getByLabelText('Title'), 'New card');
    await user.click(save);
    expect(onSave).toHaveBeenCalledWith({ mode: 'add', draft: expect.objectContaining({ title: 'New card', status: 'doing' }) });
    // The draft carries no id: the server assigns one on create.
    expect(onSave.mock.calls[0]![0].draft).not.toHaveProperty('id');
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  });

  it('applies an AI draft to every editable field before review and save', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    const aiDraft = vi.fn().mockResolvedValue({ title: 'Drafted', emoji: '🔧', desc: 'generated', prio: 1, due: '2026-09-01', effort: 'S', tags: ['ai'], checks: [{ text: 'verify', done: true }] });
    render(<AIHarness onSave={onSave} aiDraft={aiDraft} />);
    await user.type(screen.getByLabelText('✨ Draft with AI'), 'make it better');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    await screen.findByDisplayValue('Drafted');
    expect((screen.getByLabelText('Description') as HTMLTextAreaElement).value).toBe('generated');
    expect((screen.getByLabelText('Priority') as HTMLSelectElement).value).toBe('1');
    expect((screen.getByLabelText('Due') as HTMLInputElement).value).toBe('2026-09-01');
    expect((screen.getByLabelText('Effort') as HTMLSelectElement).value).toBe('S');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(onSave).toHaveBeenCalledWith({ mode: 'add', draft: expect.objectContaining({ title: 'Drafted', emoji: '🔧', desc: 'generated', tags: ['ai'], checks: [{ text: 'verify', done: true }] }) });
  });

  it('keeps the reviewed title when an AI draft omits one and ignores a forced blank submit', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    const aiDraft = vi.fn().mockResolvedValue({ title: '', emoji: '', desc: '', prio: 3, due: '', effort: '', tags: [], checks: [] });
    render(<AIHarness onSave={onSave} aiDraft={aiDraft} />);
    await user.type(screen.getByLabelText('✨ Draft with AI'), 'details only');
    await user.click(screen.getByRole('button', { name: 'Draft' }));
    await screen.findByDisplayValue('seed');
    await user.clear(screen.getByLabelText('Title'));
    fireEvent.submit(screen.getByLabelText('Title').closest('form')!);
    expect(onSave).not.toHaveBeenCalled();
  });
});
