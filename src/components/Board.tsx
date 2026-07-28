import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { PointerEvent as ReactPointerEvent } from 'react';
import type { Board, Status, Task } from '../lib/model';
import { STATUSES, STATUS_LABEL } from '../lib/model';
import { Card } from './Card';

export interface BoardProps {
  board: Board;
  onMove: (taskId: string, to: Status) => void;
  onTick: (taskId: string, checkIdx: number, pos: { x: number; y: number }) => void;
  onEdit: (taskId: string) => void;
  onAdd: (status: Status) => void;
}

interface DragState {
  taskId: string;
  from: Status;
  startX: number;
  startY: number;
  left: number;
  top: number;
  width: number;
  x: number;
  y: number;
  active: boolean;
  over: Status | null;
}

const DRAG_THRESHOLD = 9;

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
  const { board, onTick, onEdit, onAdd } = props;
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
    const cardEl = target.closest<HTMLElement>('.card');
    const taskId = cardEl?.dataset.task;
    if (!cardEl || !taskId) return;
    const task = board.tasks.find((t) => t.id === taskId);
    if (!task) return;
    const r = cardEl.getBoundingClientRect();
    update({
      taskId,
      from: task.status,
      startX: e.clientX,
      startY: e.clientY,
      left: r.left,
      top: r.top,
      width: r.width,
      x: e.clientX,
      y: e.clientY,
      active: false,
      over: null,
    });
  };

  const dragging = drag !== null;
  useEffect(() => {
    if (!dragging) return;

    const colUnder = (x: number, y: number): Status | null => {
      const root = rootRef.current;
      if (!root) return null;
      let hit: Status | null = null;
      root.querySelectorAll<HTMLElement>('.col').forEach((c) => {
        const r = c.getBoundingClientRect();
        const inX = x > r.left && x < r.right;
        const inY = y > r.top && y < r.bottom;
        if (inX && inY) hit = (c.dataset.status as Status | undefined) ?? null;
      });
      return hit;
    };

    const finish = (e: PointerEvent, drop: boolean) => {
      const d = dragRef.current;
      update(null);
      if (!d || !d.active) return;
      swallowNextClick();
      if (!drop) return;
      const to = colUnder(e.clientX, e.clientY);
      if (to && to !== d.from) onMoveRef.current(d.taskId, to);
    };

    const onPointerMove = (e: PointerEvent) => {
      const d = dragRef.current;
      if (!d) return;
      // A pointerup missed while unfocused leaves a stale drag; treat a
      // button-less mouse move as the release we never received.
      if (e.pointerType === 'mouse' && e.buttons === 0) {
        finish(e, false);
        return;
      }
      const dx = e.clientX - d.startX;
      const dy = e.clientY - d.startY;
      if (!d.active && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      update({
        ...d,
        active: true,
        x: e.clientX,
        y: e.clientY,
        over: colUnder(e.clientX, e.clientY),
      });
      e.preventDefault();
    };

    const onPointerUp = (e: PointerEvent) => finish(e, true);
    const onPointerCancel = (e: PointerEvent) => finish(e, false);
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

  return (
    <div className="board" ref={rootRef} onPointerDown={handlePointerDown}>
      {STATUSES.map((status) => (
        <Column
          key={status}
          status={status}
          tasks={board.tasks.filter((t) => t.status === status)}
          over={drag?.over === status}
          ghostId={drag?.active ? drag.taskId : null}
          onAdd={onAdd}
          onTick={onTick}
          onEdit={onEdit}
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
  ghostId: string | null;
  onAdd: (status: Status) => void;
  onTick: BoardProps['onTick'];
  onEdit: (taskId: string) => void;
}

function Column({ status, tasks, over, ghostId, onAdd, onTick, onEdit }: ColumnProps) {
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
      {tasks.map((t) => (
        <Card
          key={t.id}
          task={t}
          ghost={t.id === ghostId}
          onTick={(checkIdx, pos) => onTick(t.id, checkIdx, pos)}
          onEdit={() => onEdit(t.id)}
        />
      ))}
    </div>
  );
}
