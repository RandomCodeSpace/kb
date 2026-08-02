// @vitest-environment jsdom

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Board, Status, Task } from '../lib/model';
import { BoardView } from './Board';

class TestPointerEvent extends MouseEvent {
  readonly pointerId: number;
  readonly pointerType: string;

  constructor(type: string, init: PointerEventInit = {}) {
    super(type, init);
    this.pointerId = init.pointerId ?? 0;
    this.pointerType = init.pointerType ?? '';
  }
}

const rect = (left: number, top: number, width: number, height: number): DOMRect => ({
  left,
  top,
  right: left + width,
  bottom: top + height,
  width,
  height,
  x: left,
  y: top,
  toJSON: () => ({}),
});

const task = (id: string, title: string, status: Status): Task => ({
  id,
  emoji: '',
  title,
  desc: '',
  status,
  blocked: false,
  prio: 3,
  tags: [],
  checks: [],
  createdAt: '2026-01-01T00:00:00.000Z',
  movedAt: '2026-01-01T00:00:00.000Z',
});

const BOARD: Board = {
  title: 'Coverage board',
  tasks: [
    task('todo-a', 'First task', 'todo'),
    task('todo-b', 'Second task', 'todo'),
    task('doing-a', 'Active task', 'doing'),
    task('done-a', 'Finished task', 'done'),
    task('cancelled-a', 'Discarded task', 'cancelled'),
  ],
};

function renderBoard(showCancelled = false, board = BOARD) {
  const callbacks = {
    onMove: vi.fn(),
    onTick: vi.fn(),
    onEdit: vi.fn(),
    onAdd: vi.fn(),
    onRestore: vi.fn(),
    onPurge: vi.fn(),
    announce: vi.fn(),
  };
  const view = render(
    <BoardView board={board} showCancelled={showCancelled} {...callbacks} />,
  );
  return { ...view, ...callbacks };
}

function pointer(type: string, init: PointerEventInit): PointerEvent {
  return new TestPointerEvent(type, { bubbles: true, cancelable: true, ...init }) as PointerEvent;
}

function installGeometry(container: HTMLElement) {
  const columns = [...container.querySelectorAll<HTMLElement>('.col')];
  columns.forEach((column, i) => {
    vi.spyOn(column, 'getBoundingClientRect').mockReturnValue(rect(i * 200, 0, 180, 600));
    [...column.querySelectorAll<HTMLElement>('.card')].forEach((card, cardIndex) => {
      vi.spyOn(card, 'getBoundingClientRect').mockReturnValue(
        rect(i * 200 + 10, 80 + cardIndex * 120, 160, 80),
      );
    });
  });
}

describe('BoardView DOM behavior', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    Object.defineProperty(globalThis, 'PointerEvent', {
      configurable: true,
      value: TestPointerEvent,
    });
  });

  it('renders ordered columns and cards, hides cancelled by default, and wires column actions', async () => {
    const user = userEvent.setup();
    const { onAdd, onEdit } = renderBoard();

    expect(screen.getAllByRole('region').map((column) => column.dataset.status)).toEqual([
      'todo',
      'doing',
      'done',
    ]);
    expect(screen.getByRole('region', { name: 'To Do, 2 cards' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: /Cancelled/ })).toBeNull();
    const todo = screen.getByRole('region', { name: 'To Do, 2 cards' });
    expect(within(todo).getAllByRole('group').map((card) => card.dataset.task)).toEqual([
      'todo-a',
      'todo-b',
    ]);

    await user.click(screen.getByRole('button', { name: 'Add task to Doing' }));
    expect(onAdd).toHaveBeenCalledWith('doing');
    await user.click(screen.getByRole('group', { name: /Active task/ }));
    expect(onEdit).toHaveBeenCalledWith('doing-a');
  });

  it('shows cancelled cards with restore and purge controls when requested', async () => {
    const user = userEvent.setup();
    const { onRestore, onPurge } = renderBoard(true);
    const cancelled = screen.getByRole('region', { name: 'Cancelled, 1 card' });
    expect(within(cancelled).getByRole('group', { name: /Discarded task/ })
      .classList.contains('cancelled-card')).toBe(true);

    await user.click(within(cancelled).getByRole('button', { name: 'Restore' }));
    await user.click(within(cancelled).getByRole('button', { name: 'Delete permanently' }));
    expect(onRestore).toHaveBeenCalledWith('cancelled-a');
    expect(onPurge).toHaveBeenCalledWith('cancelled-a');
  });

  it('lifts, previews, moves, drops, refocuses, and announces a keyboard move', async () => {
    const user = userEvent.setup();
    const { onMove, announce } = renderBoard();
    const first = screen.getByRole('group', { name: /First task, To Do, 1 of 2/ });
    first.focus();

    await user.keyboard(' ');
    expect(announce).toHaveBeenLastCalledWith(expect.stringContaining(
      'First task lifted from To Do, position 1 of 2',
    ));
    expect(document.activeElement).toBe(
      screen.getByRole('group', { name: /First task, To Do, 1 of 2, lifted/ }),
    );

    await user.keyboard('{ArrowRight}{ArrowDown}');
    const preview = screen.getByRole('group', { name: /First task, Doing, 2 of 2, lifted/ });
    expect(document.activeElement).toBe(preview);
    expect(announce).toHaveBeenLastCalledWith(
      'First task, Doing, position 2 of 2',
    );
    expect(onMove).not.toHaveBeenCalled();

    await user.keyboard('{Enter}');
    expect(onMove).toHaveBeenCalledOnce();
    expect(onMove).toHaveBeenCalledWith('todo-a', 'doing', 1);
    expect(document.activeElement).toBe(
      screen.getByRole('group', { name: /First task, To Do/ }),
    );
  });

  it('cancels keyboard previews on Escape and when focus leaves the lifted card', async () => {
    const user = userEvent.setup();
    const { onMove, announce } = renderBoard();
    const first = screen.getByRole('group', { name: /First task/ });
    first.focus();
    await user.keyboard(' ');
    await user.keyboard('{ArrowRight}');
    await user.keyboard('{Escape}');
    expect(onMove).not.toHaveBeenCalled();
    expect(announce).toHaveBeenLastCalledWith(
      'Move cancelled. First task is back in To Do, position 1 of 2.',
    );

    screen.getByRole('group', { name: /First task, To Do/ }).focus();
    await user.keyboard(' ');
    await user.keyboard('{ArrowRight}');
    await screen.findByRole('group', { name: /First task, Doing.*lifted/ });
    screen.getByRole('button', { name: 'Add task to To Do' }).focus();
    await waitFor(() => {
      expect(announce).toHaveBeenLastCalledWith(
        'Move cancelled. First task is back in To Do, position 1 of 2.',
      );
      expect(screen.getByRole('group', { name: /First task, To Do/ })
        .classList.contains('lifted')).toBe(false);
    });
  });

  it('keeps taps and cancelled drags inert, then drops a threshold-crossing flick by geometry', async () => {
    const { container, onMove, onEdit } = renderBoard();
    installGeometry(container);
    const first = screen.getByRole('group', { name: /First task/ });

    fireEvent(first, pointer('pointerdown', {
      pointerId: 1,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointerup', {
      pointerId: 1,
      pointerType: 'mouse',
      clientX: 25,
      clientY: 104,
    }));
    expect(onMove).not.toHaveBeenCalled();

    fireEvent(first, pointer('pointerdown', {
      pointerId: 2,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointercancel', {
      pointerId: 2,
      pointerType: 'mouse',
      clientX: 220,
      clientY: 250,
    }));
    expect(onMove).not.toHaveBeenCalled();

    fireEvent(first, pointer('pointerdown', {
      pointerId: 3,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointerup', {
      pointerId: 3,
      pointerType: 'mouse',
      clientX: 220,
      clientY: 250,
    }));
    expect(onMove).toHaveBeenCalledWith('todo-a', 'doing', 1);
    fireEvent.click(first);
    expect(onEdit).not.toHaveBeenCalled();
    await new Promise((resolve) => setTimeout(resolve, 360));
  });

  it('renders an active drag target and placeholder, ignores other pointers, and cancels on blur', async () => {
    const { container, onMove } = renderBoard();
    installGeometry(container);
    const first = screen.getByRole('group', { name: /First task/ });

    fireEvent(first, pointer('pointerdown', {
      pointerId: 11,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointermove', {
      pointerId: 99,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 220,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointerup', {
      pointerId: 99,
      pointerType: 'mouse',
      clientX: 220,
      clientY: 100,
    }));
    expect(onMove).not.toHaveBeenCalled();

    fireEvent(window, pointer('pointermove', {
      pointerId: 11,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 220,
      clientY: 100,
    }));
    await waitFor(() => expect(container.querySelector('.dragwrap')).not.toBeNull());
    expect(screen.getByRole('region', { name: /Doing/ }).classList.contains('over')).toBe(true);
    expect((container.querySelector<HTMLElement>('.slot'))?.style.height).toBe('80px');
    expect(container.querySelector('.dragclone')).not.toBeNull();

    act(() => window.dispatchEvent(new Event('blur')));
    expect(container.querySelector('.dragwrap')).toBeNull();
    expect(onMove).not.toHaveBeenCalled();
  });

  it('requires the grip for touch and ignores non-primary mouse buttons and controls', () => {
    const { container } = renderBoard();
    installGeometry(container);
    const first = screen.getByRole('group', { name: /First task/ });
    const grip = first.querySelector<HTMLElement>('.grip')!;
    const add = screen.getByRole('button', { name: 'Add task to To Do' });

    fireEvent(first, pointer('pointerdown', {
      pointerId: 20,
      pointerType: 'touch',
      clientX: 20,
      clientY: 100,
    }));
    expect(container.querySelector('.dragwrap')).toBeNull();
    fireEvent(first, pointer('pointerdown', {
      pointerId: 21,
      pointerType: 'mouse',
      button: 2,
      clientX: 20,
      clientY: 100,
    }));
    expect(container.querySelector('.dragwrap')).toBeNull();
    fireEvent(add, pointer('pointerdown', {
      pointerId: 22,
      pointerType: 'mouse',
      button: 0,
      clientX: 20,
      clientY: 20,
    }));
    expect(container.querySelector('.dragwrap')).toBeNull();

    fireEvent(grip, pointer('pointerdown', {
      pointerId: 23,
      pointerType: 'touch',
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointercancel', {
      pointerId: 23,
      pointerType: 'touch',
      clientX: 20,
      clientY: 100,
    }));
  });

  it('ignores invalid pointer origins and covers inert keyboard paths before a valid lift', async () => {
    const user = userEvent.setup();
    const { container, onEdit, onMove, announce } = renderBoard();
    installGeometry(container);
    const main = container.querySelector<HTMLElement>('main')!;
    const first = screen.getByRole('group', { name: /First task/ });

    fireEvent(main, pointer('pointerdown', {
      pointerId: 30,
      pointerType: 'mouse',
      button: 0,
      clientX: 700,
      clientY: 700,
    }));
    expect(container.querySelector('.dragwrap')).toBeNull();

    const originalID = first.dataset.task;
    delete first.dataset.task;
    fireEvent(first, pointer('pointerdown', {
      pointerId: 31,
      pointerType: 'mouse',
      button: 0,
      clientX: 20,
      clientY: 100,
    }));
    first.dataset.task = 'not-on-board';
    fireEvent(first, pointer('pointerdown', {
      pointerId: 32,
      pointerType: 'mouse',
      button: 0,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent.keyDown(first, { key: 'Enter' });
    first.dataset.task = originalID;
    expect(container.querySelector('.dragwrap')).toBeNull();

    fireEvent.keyDown(first.querySelector('.grip')!, { key: 'Enter' });
    first.focus();
    await user.keyboard('{Escape}a{ArrowLeft}');
    expect(onMove).not.toHaveBeenCalled();
    expect(onEdit).toHaveBeenCalledOnce();
    onEdit.mockClear();

    await user.keyboard('{Enter}');
    expect(onEdit).toHaveBeenCalledWith('todo-a');
    fireEvent.keyDown(first, { key: 'Spacebar' });
    expect(announce).toHaveBeenLastCalledWith(expect.stringContaining('First task lifted'));
    await user.keyboard('{ArrowLeft}');
    expect(announce).toHaveBeenCalledTimes(1);

    const lifted = screen.getByRole('group', { name: /First task.*lifted/ });
    fireEvent(lifted, pointer('pointerdown', {
      pointerId: 33,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    const second = screen.getByRole('group', { name: /Second task/ });
    fireEvent(second, pointer('pointerdown', {
      pointerId: 34,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 220,
    }));
    fireEvent(window, pointer('pointercancel', {
      pointerId: 33,
      pointerType: 'mouse',
      clientX: 20,
      clientY: 100,
    }));
  });

  it('coalesces active drag frames, moves the clone, and cancels outside a column', async () => {
    const { container, onMove } = renderBoard();
    installGeometry(container);
    const first = screen.getByRole('group', { name: /First task/ });

    fireEvent(first, pointer('pointerdown', {
      pointerId: 40,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointermove', {
      pointerId: 40,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 24,
      clientY: 103,
    }));
    expect(container.querySelector('.dragwrap')).toBeNull();

    fireEvent(window, pointer('pointermove', {
      pointerId: 40,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 220,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointermove', {
      pointerId: 40,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 225,
      clientY: 105,
    }));
    await waitFor(() => expect(container.querySelector('.dragclone')).not.toBeNull());

    fireEvent(window, pointer('pointermove', {
      pointerId: 40,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 230,
      clientY: 240,
    }));
    await waitFor(() => {
      expect((container.querySelector<HTMLElement>('.dragwrap'))?.style.transform)
        .toBe('translate3d(210px, 140px, 0)');
      expect(container.querySelector('.slot')).not.toBeNull();
    });
    fireEvent(window, pointer('pointermove', {
      pointerId: 40,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 230,
      clientY: 240,
    }));
    await new Promise((resolve) => setTimeout(resolve, 20));
    fireEvent(window, pointer('pointermove', {
      pointerId: 40,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 900,
      clientY: 900,
    }));
    await waitFor(() => expect(screen.queryByRole('region', { name: /Doing/ })
      ?.classList.contains('over')).toBe(false));
    fireEvent(window, pointer('pointerup', {
      pointerId: 40,
      pointerType: 'mouse',
      clientX: 900,
      clientY: 900,
    }));
    expect(onMove).not.toHaveBeenCalled();
  });

  it('clears a drag when a mouse move reports no pressed buttons', () => {
    const { container, onMove } = renderBoard();
    installGeometry(container);
    const first = screen.getByRole('group', { name: /First task/ });
    fireEvent(first, pointer('pointerdown', {
      pointerId: 50,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointermove', {
      pointerId: 50,
      pointerType: 'mouse',
      buttons: 0,
      clientX: 220,
      clientY: 100,
    }));
    expect(container.querySelector('.dragwrap')).toBeNull();
    expect(onMove).not.toHaveBeenCalled();
  });

  it('handles missing hit-test metadata without inventing a drop target', async () => {
    const { container, onMove } = renderBoard();
    installGeometry(container);
    const first = screen.getByRole('group', { name: /First task/ });
    const doing = screen.getByRole('region', { name: /Doing/ });
    const targetCard = within(doing).getByRole('group', { name: /Active task/ });
    delete doing.dataset.status;

    fireEvent(first, pointer('pointerdown', {
      pointerId: 60,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointermove', {
      pointerId: 60,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 220,
      clientY: 100,
    }));
    await new Promise((resolve) => setTimeout(resolve, 20));
    fireEvent(window, pointer('pointercancel', {
      pointerId: 60,
      pointerType: 'mouse',
      clientX: 220,
      clientY: 100,
    }));
    expect(onMove).not.toHaveBeenCalled();
    await waitFor(() => expect(container.querySelector('.dragwrap')).toBeNull());

    doing.dataset.status = 'doing';
    delete targetCard.dataset.task;
    const remountedFirst = screen.getByRole('group', { name: /First task/ });
    vi.spyOn(remountedFirst, 'getBoundingClientRect').mockReturnValue(rect(10, 80, 160, 80));
    fireEvent(remountedFirst, pointer('pointerdown', {
      pointerId: 61,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    fireEvent(window, pointer('pointermove', {
      pointerId: 61,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 220,
      clientY: 100,
    }));
    await waitFor(() => expect(doing.classList.contains('over')).toBe(true));
    fireEvent(window, pointer('pointercancel', {
      pointerId: 61,
      pointerType: 'mouse',
      clientX: 220,
      clientY: 100,
    }));
  });

  it('lets a non-element focus target cancel a keyboard preview', async () => {
    const user = userEvent.setup();
    const { announce, onMove } = renderBoard();
    const first = screen.getByRole('group', { name: /First task/ });
    first.focus();
    await user.keyboard(' ');
    expect(screen.getByRole('group', { name: /First task.*lifted/ })).toBeTruthy();
    act(() => document.dispatchEvent(new FocusEvent('focusin', { bubbles: true })));
    await waitFor(() => expect(screen.queryByRole('group', { name: /lifted/ })).toBeNull());
    expect(announce).toHaveBeenLastCalledWith(
      'Move cancelled. First task is back in To Do, position 1 of 2.',
    );
    expect(onMove).not.toHaveBeenCalled();
  });

  it('ignores a stale pointerup after cancellation in the same event turn', () => {
    const { container, onMove } = renderBoard();
    installGeometry(container);
    const first = screen.getByRole('group', { name: /First task/ });
    fireEvent(first, pointer('pointerdown', {
      pointerId: 62,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    act(() => {
      window.dispatchEvent(pointer('pointercancel', {
        pointerId: 62,
        pointerType: 'mouse',
        clientX: 220,
        clientY: 100,
      }));
      window.dispatchEvent(pointer('pointerup', {
        pointerId: 62,
        pointerType: 'mouse',
        clientX: 220,
        clientY: 100,
      }));
    });
    expect(onMove).not.toHaveBeenCalled();
  });

  it('drops a keyboard preview when a board refresh removes the lifted task', async () => {
    const user = userEvent.setup();
    const { rerender, ...callbacks } = renderBoard();
    const first = screen.getByRole('group', { name: /First task/ });
    first.focus();
    await user.keyboard(' ');
    expect(screen.getByRole('group', { name: /First task.*lifted/ })).toBeTruthy();

    const refreshed = { ...BOARD, tasks: BOARD.tasks.filter((task) => task.id !== 'todo-a') };
    rerender(<BoardView board={refreshed} showCancelled={false} {...callbacks} />);
    await waitFor(() => expect(screen.queryByRole('group', { name: /First task/ })).toBeNull());
    expect(callbacks.onMove).not.toHaveBeenCalled();
  });

  it('safely runs a queued drag frame after unmount and cancels a queued frame on blur', () => {
    let queued: FrameRequestCallback | null = null;
    const raf = vi.spyOn(globalThis, 'requestAnimationFrame').mockImplementation((callback) => {
      queued = callback;
      return 77;
    });
    const cancel = vi.spyOn(globalThis, 'cancelAnimationFrame');

    const firstView = renderBoard();
    installGeometry(firstView.container);
    fireEvent(screen.getByRole('group', { name: /First task/ }), pointer('pointerdown', {
      pointerId: 70,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointermove', {
      pointerId: 70,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 220,
      clientY: 100,
    }));
    firstView.unmount();
    expect(cancel).toHaveBeenCalledWith(77);
    const afterUnmount = queued as FrameRequestCallback | null;
    if (afterUnmount) afterUnmount(1);

    raf.mockClear();
    queued = null;
    const secondView = renderBoard();
    installGeometry(secondView.container);
    fireEvent(screen.getByRole('group', { name: /First task/ }), pointer('pointerdown', {
      pointerId: 71,
      pointerType: 'mouse',
      button: 0,
      buttons: 1,
      clientX: 20,
      clientY: 100,
    }));
    fireEvent(window, pointer('pointermove', {
      pointerId: 71,
      pointerType: 'mouse',
      buttons: 1,
      clientX: 220,
      clientY: 100,
    }));
    fireEvent(window, new Event('blur'));
    const afterBlur = queued as FrameRequestCallback | null;
    if (afterBlur) afterBlur(2);
    expect(secondView.container.querySelector('.dragwrap')).toBeNull();
  });
});
