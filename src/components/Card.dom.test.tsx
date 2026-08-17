// @vitest-environment jsdom

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Task } from '../lib/model';
import { Card } from './Card';

const BASE_TASK: Task = {
  id: 'card-1',
  emoji: '🛠️',
  title: 'Repair **sync**',
  desc: 'Read [the runbook](https://example.test/runbook) *carefully*\n- preserve `history`',
  status: 'doing',
  blocked: true,
  prio: 2,
  due: '2099-01-01',
  effort: 'M',
  tags: ['backend', 'type::bug'],
  checks: [
    { text: 'reproduce', done: true },
    { text: 'verify **repair**', done: false },
  ],
  createdAt: '2026-01-01T00:00:00.000Z',
  movedAt: '2026-01-02T00:00:00.000Z',
};

function renderCard(overrides: Partial<Task> = {}) {
  const callbacks = {
    onTick: vi.fn(),
    onEdit: vi.fn(),
    onCardKey: vi.fn(),
    onRestore: vi.fn(),
    onPurge: vi.fn(),
  };
  const task = { ...BASE_TASK, ...overrides };
  const view = render(
    <Card
      task={task}
      index={1}
      total={3}
      keysHintId="keyboard-help"
      {...callbacks}
    />,
  );
  return { ...view, ...callbacks, task };
}

describe('Card DOM behavior', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders card state, formatted content, labels, and a safe external link', () => {
    renderCard();

    const card = screen.getByRole('group', {
      name: /Repair \*\*sync\*\*, Doing, 2 of 3, blocked, 1 of 2 checklist items done/,
    });
    expect(card.classList.contains('card')).toBe(true);
    expect(card.classList.contains('blocked-card')).toBe(true);
    expect(card.getAttribute('aria-describedby')).toBe('keyboard-help');
    expect(screen.getByText('sync', { selector: 'strong' })).toBeTruthy();
    expect(screen.getByText('carefully', { selector: 'em' })).toBeTruthy();
    expect(screen.getByText('history', { selector: 'code' })).toBeTruthy();
    expect(screen.getByRole('progressbar', { name: 'Checklist progress' })
      .getAttribute('aria-valuetext')).toBe('1 of 2 items done');
    expect(screen.getByRole('img', { name: 'Priority 2 · high' })).toBeTruthy();
    expect(screen.getByRole('img', { name: 'Effort M' })).toBeTruthy();
    expect(screen.getByRole('img', { name: 'Label backend' })).toBeTruthy();
    expect(screen.getByRole('img', { name: 'Label type: bug' })).toBeTruthy();
    const link = screen.getByRole('link', { name: 'the runbook' });
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
    expect(link.getAttribute('target')).toBe('_blank');
  });

  it('shows the task number when the server sent one, hides it otherwise', () => {
    const { unmount } = renderCard({ seq: 12 });
    const chip = screen.getByTitle('Task #12');
    expect(chip.textContent).toBe('#12');
    expect(chip.getAttribute('aria-hidden')).toBe('true');
    expect(
      screen.getByRole('group', { name: /Repair \*\*sync\*\*, number 12, Doing/ }),
    ).toBeTruthy();
    unmount();

    renderCard();
    expect(screen.queryByTitle(/Task #/)).toBeNull();
  });

  it('opens the editor from the body but not from links or card controls', async () => {
    const user = userEvent.setup();
    const { onEdit } = renderCard();
    const card = screen.getByRole('group', { name: /Repair \*\*sync\*\*/ });

    await user.click(screen.getByRole('link', { name: 'the runbook' }));
    await user.click(screen.getByRole('button', { name: 'Expand checklist' }));
    expect(onEdit).not.toHaveBeenCalled();

    await user.click(card);
    expect(onEdit).toHaveBeenCalledOnce();
    expect(onEdit).toHaveBeenCalledWith('card-1');
  });

  it('expands, remeasures on resize, collapses, and toggles checklist items', async () => {
    const user = userEvent.setup();
    const { container, onTick } = renderCard();
    const checks = container.querySelector<HTMLElement>('.checks')!;
    let measuredHeight = 84;
    Object.defineProperty(checks, 'scrollHeight', {
      configurable: true,
      get: () => measuredHeight,
    });

    expect(checks.style.maxHeight).toBe('0px');
    expect(checks.hasAttribute('inert')).toBe(true);
    await user.click(screen.getByRole('button', { name: 'Expand checklist' }));
    expect(checks.style.maxHeight).toBe('84px');
    expect(checks.hasAttribute('inert')).toBe(false);
    expect(screen.getByRole('button', { name: 'Collapse checklist' })
      .getAttribute('aria-expanded')).toBe('true');

    measuredHeight = 137;
    fireEvent(window, new Event('resize'));
    expect(checks.style.maxHeight).toBe('137px');

    const unchecked = screen.getByRole('checkbox', { name: /verify.*repair/ });
    vi.spyOn(unchecked, 'getBoundingClientRect').mockReturnValue({
      left: 30,
      top: 50,
      right: 230,
      bottom: 90,
      width: 200,
      height: 40,
      x: 30,
      y: 50,
      toJSON: () => ({}),
    });
    fireEvent.pointerDown(unchecked, { pointerId: 7, clientX: 35, clientY: 55 });
    expect(onTick).toHaveBeenLastCalledWith('card-1', 1, { x: 38, y: 58 });

    unchecked.focus();
    await user.keyboard('x');
    expect(onTick).toHaveBeenCalledOnce();
    await user.keyboard('{Enter}');
    await user.keyboard(' ');
    expect(onTick).toHaveBeenCalledTimes(3);

    await user.click(screen.getByRole('button', { name: 'Collapse checklist' }));
    expect(checks.style.maxHeight).toBe('0px');
    expect(checks.hasAttribute('inert')).toBe(true);
  });

  it('forwards card keyboard events and exposes restore and permanent-delete actions', async () => {
    const user = userEvent.setup();
    const { onCardKey, onRestore, onPurge } = renderCard({
      status: 'cancelled',
      blocked: false,
      checks: [],
    });
    const card = screen.getByRole('group', { name: /Repair \*\*sync\*\*, Cancelled/ });
    expect(card.classList.contains('cancelled-card')).toBe(true);

    card.focus();
    await user.keyboard('{ArrowRight}');
    expect(onCardKey).toHaveBeenCalledOnce();
    expect(onCardKey.mock.calls[0][0]).toBe('card-1');
    expect(onCardKey.mock.calls[0][1].key).toBe('ArrowRight');

    await user.click(screen.getByRole('button', { name: 'Restore' }));
    await user.click(screen.getByRole('button', { name: 'Delete permanently' }));
    expect(onRestore).toHaveBeenCalledWith('card-1');
    expect(onPurge).toHaveBeenCalledWith('card-1');
  });

  it('renders an overdue due chip', () => {
    const { container } = renderCard({ due: '2000-01-01' });
    const due = container.querySelector('.chip.ovd');
    expect(due).not.toBeNull();
    expect(due?.textContent).toContain('overdue');
  });

  it('turns label chips into filter buttons only when onTagClick is wired', async () => {
    const user = userEvent.setup();
    const onTagClick = vi.fn();
    const onEdit = vi.fn();
    render(
      <Card task={BASE_TASK} onTagClick={onTagClick} onEdit={onEdit} />,
    );
    await user.click(screen.getByRole('button', { name: 'Filter by label backend' }));
    await user.click(screen.getByRole('button', { name: 'Filter by label type::bug' }));
    expect(onTagClick.mock.calls).toEqual([['backend'], ['type::bug']]);
    // A label press filters; it must not also open the editor.
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('keeps labels as plain chips without onTagClick', () => {
    renderCard();
    expect(screen.queryByRole('button', { name: /Filter by label/ })).toBeNull();
    expect(screen.getByRole('img', { name: 'Label backend' })).toBeTruthy();
  });
});
