import { Fragment, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { PointerEvent as ReactPointerEvent } from 'react';
import type { Board, Status, Task } from '../lib/model';
import { STATUSES, STATUS_LABEL } from '../lib/model';
import { Card } from './Card';

export interface BoardProps {
  board: Board;
  /** `index` is the slot within `to`, counted over that column's other cards. */
  onMove: (taskId: string, to: Status, index: number) => void;
  onTick: (taskId: string, checkIdx: number, pos: { x: number; y: number }) => void;
  onEdit: (taskId: string) => void;
  onAdd: (status: Status) => void;
  /** Whether the cancelled column is shown (F11). */
  showCancelled: boolean;
  onRestore: (taskId: string) => void;
  onPurge: (taskId: string) => void;
}

interface DragState {
  /** The pointer that started this drag; every other pointer is ignored. */
  pointerId: number;
  taskId: string;
  from: Status;
  startX: number;
  startY: number;
  left: number;
  top: number;
  width: number;
  height: number;
  x: number;
  y: number;
  active: boolean;
  /** Column under the pointer, highlighted as the drop target. */
  over: Status | null;
  /** Slot within `over` where the card would land. */
  overIndex: number;
}

const DRAG_THRESHOLD = 9;
const SHOW_CANCELLED_KEY = 'kb.showCancelled.v1';

/** Whether the cancelled column is shown; persisted across reloads. */
export function showCancelledFlag(): boolean {
  try {
    return localStorage.getItem(SHOW_CANCELLED_KEY) === '1';
  } catch {
    return false;
  }
}

export function setShowCancelledFlag(on: boolean): void {
  try {
    if (on) localStorage.setItem(SHOW_CANCELLED_KEY, '1');
    else localStorage.removeItem(SHOW_CANCELLED_KEY);
  } catch {
    // Storage unavailable — the choice lives for this session only.
  }
}

/**
 * Slot a card released at `y` should take, given the vertical midpoints of
 * the cards already in that column (ascending, dragged card excluded).
 */
export function insertionIndex(mids: readonly number[], y: number): number {
  let i = 0;
  while (i < mids.length && y >= mids[i]) i++;
  return i;
}

/**
 * Move `taskId` into `to` at slot `index`, counted over the other cards of
 * that status. Order within a status is plain array order — exactly what the
 * markdown codec writes and reads back — so the position survives the round
 * trip without a new wire field. `movedAt` is only stamped when the status
 * actually changes: reordering inside a column must not reset a card's age.
 */
export function moveTask(
  tasks: readonly Task[],
  taskId: string,
  to: Status,
  index: number,
  movedAt: string,
): Task[] {
  const moving = tasks.find((t) => t.id === taskId);
  if (!moving) return [...tasks];
  const rest = tasks.filter((t) => t.id !== taskId);
  const next = moving.status === to ? moving : { ...moving, status: to, movedAt };
  const column = rest.filter((t) => t.status === to);
  const slot = Math.max(0, Math.min(index, column.length));
  // Translate the per-column slot into a position in the flat task list; an
  // empty destination column can go anywhere, so it goes last.
  const at = slot === column.length ? rest.length : rest.indexOf(column[slot]);
  rest.splice(at, 0, next);
  return rest;
}

/**
 * Whether a pointerdown may begin a drag. Touch and pen honour `touch-action`,
 * and the card body keeps `pan-y` so a finger can still scroll the column —
 * which means a vertical press-and-drag there belongs to the browser (it takes
 * the gesture and sends us pointercancel), and no amount of preventDefault
 * takes it back. The grip opts out with `touch-action: none`, so on those
 * pointer types an in-column reorder has to start there. A mouse is not
 * governed by touch-action, so it still drags from anywhere on the card.
 */
export function canStartDrag(pointerType: string, onGrip: boolean): boolean {
  return pointerType === 'mouse' || onGrip;
}

/** Swallow the click generated at the end of a drag so it doesn't open the editor. */
function swallowNextClick(): void {
  const swallow = (e: MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
  };
  window.addEventListener('click', swallow, { capture: true, once: true });
  setTimeout(() => window.removeEventListener('click', swallow, { capture: true }), 350);
}

export function BoardView(props: BoardProps) {
  const { board, onTick, onEdit, onAdd, showCancelled, onRestore, onPurge } = props;
  const rootRef = useRef<HTMLDivElement>(null);
  const cloneRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<DragState | null>(null);
  const [drag, setDrag] = useState<DragState | null>(null);
  const onMoveRef = useRef(props.onMove);
  onMoveRef.current = props.onMove;

  const update = (d: DragState | null) => {
    dragRef.current = d;
    setDrag(d);
  };

  const handlePointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (dragRef.current) return;
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    const target = e.target as Element;
    if (target.closest('.chev,.check,.bx,button,input,select,textarea')) return;
    if (!canStartDrag(e.pointerType, target.closest('.grip') !== null)) return;
    const cardEl = target.closest<HTMLElement>('.card');
    const taskId = cardEl?.dataset.task;
    if (!cardEl || !taskId) return;
    const task = board.tasks.find((t) => t.id === taskId);
    if (!task) return;
    const r = cardEl.getBoundingClientRect();
    update({
      pointerId: e.pointerId,
      taskId,
      from: task.status,
      startX: e.clientX,
      startY: e.clientY,
      left: r.left,
      top: r.top,
      width: r.width,
      height: r.height,
      x: e.clientX,
      y: e.clientY,
      active: false,
      over: null,
      overIndex: 0,
    });
  };

  const dragging = drag !== null;
  useEffect(() => {
    if (!dragging) return;

    /**
     * Column under the pointer plus the slot the card would take in it, or
     * null when the pointer is outside every column (a release there cancels).
     */
    const hitTest = (x: number, y: number): { to: Status; index: number } | null => {
      const root = rootRef.current;
      if (!root) return null;
      for (const col of root.querySelectorAll<HTMLElement>('.col')) {
        const r = col.getBoundingClientRect();
        if (x < r.left || x > r.right || y < r.top || y > r.bottom) continue;
        const to = col.dataset.status as Status | undefined;
        if (!to) continue;
        // The dragged card is not rendered in its column while dragging (the
        // clone lives outside the board), so these are exactly the cards the
        // drop is positioned against.
        const mids: number[] = [];
        col.querySelectorAll<HTMLElement>('.card').forEach((c) => {
          const cr = c.getBoundingClientRect();
          mids.push(cr.top + cr.height / 2);
        });
        return { to, index: insertionIndex(mids, y) };
      }
      return null;
    };

    // `swallow` is false for a cancelled pointer sequence: that produces no
    // click at all, so arming the swallower would only eat the user's next
    // real tap (within 350ms) instead.
    const finish = (e: PointerEvent, drop: boolean, swallow = true) => {
      const d = dragRef.current;
      // A second touch must not finish (or hijack) a drag it did not start.
      if (d && e.pointerId !== d.pointerId) return;
      update(null);
      if (!d || !d.active) return;
      if (swallow) swallowNextClick();
      if (!drop) return;
      // Released outside every column: the card returns, nothing changes.
      const hit = hitTest(e.clientX, e.clientY);
      if (hit) onMoveRef.current(d.taskId, hit.to, hit.index);
    };

    const onPointerMove = (e: PointerEvent) => {
      const d = dragRef.current;
      if (!d || e.pointerId !== d.pointerId) return;
      // A pointerup missed while unfocused leaves a stale drag; treat a
      // button-less mouse move as the release we never received.
      if (e.pointerType === 'mouse' && e.buttons === 0) {
        finish(e, false);
        return;
      }
      const dx = e.clientX - d.startX;
      const dy = e.clientY - d.startY;
      if (!d.active && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      const hit = hitTest(e.clientX, e.clientY);
      update({
        ...d,
        active: true,
        x: e.clientX,
        y: e.clientY,
        over: hit?.to ?? null,
        overIndex: hit?.index ?? 0,
      });
      e.preventDefault();
    };

    const onPointerUp = (e: PointerEvent) => finish(e, true);
    const onPointerCancel = (e: PointerEvent) => finish(e, false, false);
    const onWindowBlur = () => update(null);

    window.addEventListener('pointermove', onPointerMove, { passive: false });
    window.addEventListener('pointerup', onPointerUp);
    window.addEventListener('pointercancel', onPointerCancel);
    window.addEventListener('blur', onWindowBlur);
    return () => {
      window.removeEventListener('pointermove', onPointerMove);
      window.removeEventListener('pointerup', onPointerUp);
      window.removeEventListener('pointercancel', onPointerCancel);
      window.removeEventListener('blur', onWindowBlur);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dragging]);

  // The clone is a plain <Card>; stamp the .dragclone class on its root element.
  useLayoutEffect(() => {
    cloneRef.current?.querySelector('.card')?.classList.add('dragclone');
  });

  const dragTask = drag?.active
    ? board.tasks.find((t) => t.id === drag.taskId)
    : undefined;
  const columns = showCancelled
    ? STATUSES
    : STATUSES.filter((s) => s !== 'cancelled');

  return (
    <div className="board" ref={rootRef} onPointerDown={handlePointerDown}>
      {columns.map((status) => (
        <Column
          key={status}
          status={status}
          tasks={board.tasks.filter(
            (t) => t.status === status && t.id !== dragTask?.id,
          )}
          over={drag?.over === status}
          placeholder={dragTask && drag?.over === status ? drag.overIndex : null}
          placeholderHeight={drag?.height ?? 0}
          onAdd={onAdd}
          onTick={onTick}
          onEdit={onEdit}
          onRestore={onRestore}
          onPurge={onPurge}
        />
      ))}
      {drag && dragTask && (
        <div
          className="dragwrap"
          ref={cloneRef}
          style={{
            left: drag.left + (drag.x - drag.startX),
            top: drag.top + (drag.y - drag.startY),
            width: drag.width,
          }}
        >
          <Card task={dragTask} />
        </div>
      )}
    </div>
  );
}

interface ColumnProps {
  status: Status;
  tasks: Task[];
  over: boolean;
  /** Slot to draw the insertion placeholder in, or null for none. */
  placeholder: number | null;
  placeholderHeight: number;
  onAdd: (status: Status) => void;
  onTick: BoardProps['onTick'];
  onEdit: (taskId: string) => void;
  onRestore: (taskId: string) => void;
  onPurge: (taskId: string) => void;
}

function Column({
  status,
  tasks,
  over,
  placeholder,
  placeholderHeight,
  onAdd,
  onTick,
  onEdit,
  onRestore,
  onPurge,
}: ColumnProps) {
  const slot =
    placeholder === null ? null : (
      <div className="slot" style={{ height: placeholderHeight }} />
    );
  return (
    <div className={`col ${status}${over ? ' over' : ''}`} data-status={status}>
      <div className="colhead">
        {STATUS_LABEL[status]} <span className="cnt">{tasks.length}</span>
        <button
          type="button"
          className="addbtn"
          aria-label={`Add task to ${STATUS_LABEL[status]}`}
          onClick={() => onAdd(status)}
        >
          +
        </button>
      </div>
      {tasks.map((t, i) => (
        <Fragment key={t.id}>
          {placeholder === i && slot}
          <Card
            task={t}
            onTick={(checkIdx, pos) => onTick(t.id, checkIdx, pos)}
            onEdit={() => onEdit(t.id)}
            onRestore={status === 'cancelled' ? () => onRestore(t.id) : undefined}
            onPurge={status === 'cancelled' ? () => onPurge(t.id) : undefined}
          />
        </Fragment>
      ))}
      {placeholder === tasks.length && slot}
    </div>
  );
}
