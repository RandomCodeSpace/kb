import {
  Fragment,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type {
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from 'react';
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
  /**
   * Sends a sentence to the app's polite live region. A keyboard move is
   * otherwise silent: the card changes place on screen and nothing is said.
   */
  announce?: (message: string) => void;
}

/**
 * The parts of a drag that change what is *rendered*. The live pointer
 * position is deliberately not here: it changes on every pointermove, and
 * putting it in state would re-render the whole board dozens of times per
 * frame. It lives in a ref and moves the clone with a transform instead.
 */
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
 * Whether a pointer has travelled far enough from where it was pressed for
 * this to be a drag rather than a tap. Both the move handler (which starts
 * the drag) and the release handler (which may have to decide before a single
 * animation frame has run) go through here, so they cannot disagree.
 */
export function pastThreshold(dx: number, dy: number): boolean {
  return Math.hypot(dx, dy) >= DRAG_THRESHOLD;
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

/** A card rendered in a column, as the drop needs to see it. */
export interface CardMid {
  taskId: string;
  /** Vertical midpoint in viewport coordinates. */
  mid: number;
}

/**
 * Slot a card released at `y` takes in a column, given every card currently
 * rendered in it. The dragged card is filtered out here rather than assumed
 * absent: it only leaves its column once the drag goes *active*, which the
 * animation frame sets — and a flick that presses, crosses the threshold and
 * releases inside a single frame drops while the card is still rendered.
 * Counting it there would push every same-column drop one slot too low, since
 * `moveTask` applies the index to a list the card has already left.
 */
export function dropIndex(
  cards: readonly CardMid[],
  draggedId: string,
  y: number,
): number {
  const mids = cards.filter((c) => c.taskId !== draggedId).map((c) => c.mid);
  return insertionIndex(mids, y);
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

/* ---------- keyboard move model (Space to lift, arrows, Enter, Escape) ---------- */

/** Arrow keys the lifted card understands. */
export type LiftKey = 'ArrowLeft' | 'ArrowRight' | 'ArrowUp' | 'ArrowDown';

export function isLiftKey(key: string): key is LiftKey {
  return (
    key === 'ArrowLeft' ||
    key === 'ArrowRight' ||
    key === 'ArrowUp' ||
    key === 'ArrowDown'
  );
}

/** Where a lifted card would land: the same pair a pointer drop produces. */
export interface LiftPos {
  to: Status;
  /** Slot within `to`, counted over that column's *other* cards. */
  index: number;
}

/** How many cards each column holds, the lifted one excluded. */
export type ColumnSizes = Readonly<Partial<Record<Status, number>>>;

/**
 * Cards per column with `without` left out — the counts a slot index is
 * measured against, exactly as `moveTask` measures it.
 */
export function columnSizes(
  tasks: readonly Task[],
  without: string,
): ColumnSizes {
  const sizes: Partial<Record<Status, number>> = {};
  for (const s of STATUSES) sizes[s] = 0;
  for (const t of tasks) {
    if (t.id === without) continue;
    sizes[t.status] = (sizes[t.status] ?? 0) + 1;
  }
  return sizes;
}

/**
 * Where one arrow key press takes a lifted card. Up/Down reorder inside the
 * column, Left/Right cross to the neighbouring *visible* column keeping the
 * slot where it still exists and clamping to the end where it does not. Both
 * ends are walls rather than wraps: a card that silently jumped from the last
 * column back to the first would be a move nobody asked for.
 */
export function moveLift(
  pos: LiftPos,
  key: LiftKey,
  columns: readonly Status[],
  sizes: ColumnSizes,
): LiftPos {
  const size = (s: Status) => sizes[s] ?? 0;
  if (key === 'ArrowUp') return { to: pos.to, index: Math.max(0, pos.index - 1) };
  if (key === 'ArrowDown') {
    return { to: pos.to, index: Math.min(size(pos.to), pos.index + 1) };
  }
  const at = columns.indexOf(pos.to);
  if (at === -1) return pos;
  const next = columns[at + (key === 'ArrowRight' ? 1 : -1)];
  if (next === undefined) return pos;
  return { to: next, index: Math.min(pos.index, size(next)) };
}

/** "position 2 of 4" — 1-based, because that is how a person counts. */
export function positionPhrase(index: number, total: number): string {
  return `position ${index + 1} of ${total}`;
}

/** Said when the card is picked up: it also teaches the keys. */
export function liftAnnouncement(
  title: string,
  pos: LiftPos,
  total: number,
): string {
  return `${title} lifted from ${STATUS_LABEL[pos.to]}, ${positionPhrase(pos.index, total)}. Use the arrow keys to move it, Enter or Space to drop, Escape to cancel.`;
}

/** Said on every arrow press while the card is lifted. */
export function liftMoveAnnouncement(
  title: string,
  pos: LiftPos,
  total: number,
): string {
  return `${title}, ${STATUS_LABEL[pos.to]}, ${positionPhrase(pos.index, total)}`;
}

/** Said once a move is committed — by keyboard, by drag, or by auto-ship. */
export function movedAnnouncement(
  title: string,
  to: Status,
  index: number,
  total: number,
): string {
  return `${title} moved to ${STATUS_LABEL[to]}, ${positionPhrase(index, total)}`;
}

/**
 * Whether a lift survives focus landing somewhere. A lift is a preview of a
 * move nobody has committed: the board draws it, and every column and card
 * name reports it. The card that is being moved is therefore the only place
 * it may live — focus anywhere else (Tab, a link inside the card itself, a
 * click on Settings) leaves an order on screen that no key can commit or
 * cancel any more, because the keys are all on the card.
 *
 * `focusedCardId` is the card the newly focused element belongs to (null when
 * it belongs to none), and `onCardItself` says the focus is on the card
 * element rather than on a control inside it — where the card's own key
 * handler never fires.
 */
export function liftSurvivesFocus(
  liftedId: string,
  focusedCardId: string | null,
  onCardItself: boolean,
): boolean {
  return onCardItself && focusedCardId === liftedId;
}

/** Said when Escape puts the card back where it was picked up. */
export function cancelAnnouncement(
  title: string,
  pos: LiftPos,
  total: number,
): string {
  return `Move cancelled. ${title} is back in ${STATUS_LABEL[pos.to]}, ${positionPhrase(pos.index, total)}.`;
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

/** A card the keyboard has picked up, and where it currently sits. */
interface LiftState extends LiftPos {
  taskId: string;
  /** Where it was picked up from, so Escape can put it back. */
  from: Status;
  fromIndex: number;
}

/** Id of the description that teaches the keyboard move model. */
const KEYS_HINT_ID = 'kb-card-keys';

export function BoardView(props: BoardProps) {
  const { board, onTick, onEdit, onAdd, showCancelled, onRestore, onPurge } = props;
  const rootRef = useRef<HTMLElement>(null);
  const cloneRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<DragState | null>(null);
  const [drag, setDrag] = useState<DragState | null>(null);
  // The keyboard move in progress. Nothing is committed while it runs: the
  // board is previewed locally, so Escape restores the original order by
  // dropping this state and no save/PUT happens per arrow press.
  const [lift, setLift] = useState<LiftState | null>(null);
  const liftRef = useRef<LiftState | null>(null);
  liftRef.current = lift;
  // Card to put focus back on after a render that may have re-parented it —
  // moving a card to another column unmounts and remounts it, which drops
  // focus. A ref, not state: two arrow presses inside one tick would set the
  // same state value twice, React's same-value bailout would skip the second
  // render, and the re-parented card would never be focused again — measured
  // as focus landing on <body> with the card stranded mid-move.
  const refocusRef = useRef<string | null>(null);
  // Live pointer position, written by every pointermove and read once per
  // animation frame. Not state: see DragState.
  const posRef = useRef({ x: 0, y: 0 });
  const onMoveRef = useRef(props.onMove);
  onMoveRef.current = props.onMove;
  const announceRef = useRef(props.announce);
  announceRef.current = props.announce;
  const onEditRef = useRef(onEdit);
  onEditRef.current = onEdit;
  // Read by the key handler, which is created once so Card's memo holds.
  const boardRef = useRef(board);
  boardRef.current = board;

  const update = (d: DragState | null) => {
    dragRef.current = d;
    setDrag(d);
  };

  /** Where the clone sits now, relative to where the card started. */
  const cloneShift = (d: DragState) =>
    `translate3d(${posRef.current.x - d.startX}px, ${posRef.current.y - d.startY}px, 0)`;

  const handlePointerDown = (e: ReactPointerEvent<HTMLElement>) => {
    if (dragRef.current) return;
    // A pointer taking over mid-keyboard-move drops the lift rather than
    // running two moves of the same card at once.
    if (liftRef.current) setLift(null);
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
    posRef.current = { x: e.clientX, y: e.clientY };
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
    const hitTest = (
      x: number,
      y: number,
      draggedId: string,
    ): { to: Status; index: number } | null => {
      const root = rootRef.current;
      if (!root) return null;
      for (const col of root.querySelectorAll<HTMLElement>('.col')) {
        const r = col.getBoundingClientRect();
        if (x < r.left || x > r.right || y < r.top || y > r.bottom) continue;
        const to = col.dataset.status as Status | undefined;
        if (!to) continue;
        // An active drag has already pulled the dragged card out of its
        // column, but a sub-frame flick drops before that happens — so the
        // card is identified and dropIndex excludes it either way.
        const cards: CardMid[] = [];
        col.querySelectorAll<HTMLElement>('.card').forEach((c) => {
          const cr = c.getBoundingClientRect();
          cards.push({ taskId: c.dataset.task ?? '', mid: cr.top + cr.height / 2 });
        });
        return { to, index: dropIndex(cards, draggedId, y) };
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
      // `active` is set by the animation frame, which a quick flick can beat:
      // press, move past the threshold and release inside one frame. Measure
      // the release point too, or that drag would silently land as a click.
      if (!d) return;
      if (!d.active && !pastThreshold(e.clientX - d.startX, e.clientY - d.startY))
        return;
      if (swallow) swallowNextClick();
      if (!drop) return;
      // Released outside every column: the card returns, nothing changes.
      const hit = hitTest(e.clientX, e.clientY, d.taskId);
      if (hit) onMoveRef.current(d.taskId, hit.to, hit.index);
    };

    // One frame's worth of drag work, however many pointermoves arrived: move
    // the clone on its own layer, and hit-test once. hitTest reads layout for
    // every column and card, so doing it per event forced that many layouts.
    let raf = 0;
    const runFrame = () => {
      raf = 0;
      const d = dragRef.current;
      if (!d) return;
      const { x, y } = posRef.current;
      const el = cloneRef.current;
      if (el) el.style.transform = cloneShift(d);
      const hit = hitTest(x, y, d.taskId);
      const over = hit?.to ?? null;
      const overIndex = hit?.index ?? 0;
      // Re-render only when what is drawn actually changes — the placeholder
      // moving, a new column lighting up, or the drag becoming active.
      if (d.active && d.over === over && d.overIndex === overIndex) return;
      update({ ...d, active: true, over, overIndex });
    };
    const schedule = () => {
      if (raf === 0) raf = requestAnimationFrame(runFrame);
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
      posRef.current = { x: e.clientX, y: e.clientY };
      if (!d.active && !pastThreshold(e.clientX - d.startX, e.clientY - d.startY))
        return;
      schedule();
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
      if (raf !== 0) cancelAnimationFrame(raf);
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
  const columns = useMemo(
    () => (showCancelled ? STATUSES : STATUSES.filter((s) => s !== 'cancelled')),
    [showCancelled],
  );
  const columnsRef = useRef(columns);
  columnsRef.current = columns;

  /**
   * The board as the lifted card's current position would leave it. Only a
   * preview: the move is committed once, on drop, so Escape has nothing to
   * undo and an arrow press costs no save.
   */
  const shown = useMemo(() => {
    if (!lift) return board.tasks;
    const t = board.tasks.find((x) => x.id === lift.taskId);
    if (!t) return board.tasks;
    // The card's own movedAt goes back in: previewing a move must not age it.
    return moveTask(board.tasks, lift.taskId, lift.to, lift.index, t.movedAt);
  }, [board.tasks, lift]);

  // A board refresh (server adoption re-mints ids) can take the lifted card
  // out from under the move; there is then nothing left to drop.
  useEffect(() => {
    if (lift && !board.tasks.some((t) => t.id === lift.taskId)) setLift(null);
  }, [board.tasks, lift]);

  // Moving a card into another column re-parents it, which drops DOM focus on
  // the floor. Put it back on the card the user is still moving. No dependency
  // list: this runs after every commit and does nothing unless the key handler
  // asked for it, so however many arrow presses land in one tick, the render
  // they produce is followed by exactly one refocus.
  useLayoutEffect(() => {
    const id = refocusRef.current;
    if (id === null) return;
    refocusRef.current = null;
    rootRef.current
      ?.querySelector<HTMLElement>(`.card[data-task="${CSS.escape(id)}"]`)
      ?.focus();
  });

  /**
   * A lift lives only while its own card has focus. Tab onto a link inside the
   * card, onto the next card, or onto a header button that opens a dialog, and
   * every key of the move model becomes unreachable — the board would otherwise
   * keep drawing and announcing an order that was never committed, with no way
   * back. Cancel it there and say so, which is what Escape would have said.
   */
  useEffect(() => {
    if (!lift) return;
    const onFocusIn = (e: FocusEvent) => {
      const el = e.target instanceof Element ? e.target : null;
      const card = el?.closest<HTMLElement>('.card') ?? null;
      if (liftSurvivesFocus(lift.taskId, card?.dataset.task ?? null, el === card)) {
        return;
      }
      setLift(null);
      const task = boardRef.current.tasks.find((t) => t.id === lift.taskId);
      if (!task) return;
      const sizes = columnSizes(boardRef.current.tasks, lift.taskId);
      announceRef.current?.(
        cancelAnnouncement(
          task.title,
          { to: lift.from, index: lift.fromIndex },
          (sizes[lift.from] ?? 0) + 1,
        ),
      );
    };
    document.addEventListener('focusin', onFocusIn);
    return () => document.removeEventListener('focusin', onFocusIn);
  }, [lift]);

  /**
   * The whole keyboard move model, on the card itself: Space lifts and drops,
   * Enter opens the card (or drops it mid-move), the arrows move it between and
   * within columns, Escape puts it back. Created once — a per-card closure
   * would be a new prop on every render and defeat Card's memo.
   */
  const handleCardKey = useCallback(
    (taskId: string, e: ReactKeyboardEvent<HTMLElement>) => {
      // Only the card's own keys: Space on the chevron or a checklist item is
      // that control's business.
      if (e.target !== e.currentTarget) return;
      const tasks = boardRef.current.tasks;
      const task = tasks.find((t) => t.id === taskId);
      if (!task) return;
      const cur = liftRef.current?.taskId === taskId ? liftRef.current : null;
      const say = (m: string) => announceRef.current?.(m);
      const sizes = columnSizes(tasks, taskId);
      const total = (s: Status) => (sizes[s] ?? 0) + 1;

      if (e.key === 'Escape') {
        if (!cur) return;
        e.preventDefault();
        // The board owns this Escape: no dialog is open behind a lifted card,
        // and the key must not travel on to anything that would also close.
        e.stopPropagation();
        setLift(null);
        refocusRef.current = taskId;
        say(
          cancelAnnouncement(
            task.title,
            { to: cur.from, index: cur.fromIndex },
            total(cur.from),
          ),
        );
        return;
      }

      if (e.key === ' ' || e.key === 'Spacebar' || e.key === 'Enter') {
        e.preventDefault();
        if (cur) {
          // Drop: the one commit of the whole move.
          setLift(null);
          refocusRef.current = taskId;
          onMoveRef.current(taskId, cur.to, cur.index);
          return;
        }
        if (e.key === 'Enter') {
          onEditRef.current(taskId);
          return;
        }
        const column = tasks.filter((t) => t.status === task.status);
        const index = column.findIndex((t) => t.id === taskId);
        setLift({
          taskId,
          from: task.status,
          fromIndex: index,
          to: task.status,
          index,
        });
        say(
          liftAnnouncement(task.title, { to: task.status, index }, column.length),
        );
        return;
      }

      if (!isLiftKey(e.key)) return;
      // Not lifted: the arrows stay the browser's, so the column still scrolls.
      if (!cur) return;
      e.preventDefault();
      const next = moveLift(cur, e.key, columnsRef.current, sizes);
      if (next.to === cur.to && next.index === cur.index) return;
      setLift({ ...cur, ...next });
      refocusRef.current = taskId;
      say(liftMoveAnnouncement(task.title, next, total(next.to)));
    },
    [],
  );

  return (
    // The board is the app's main content; without this landmark every column
    // and card sits outside one.
    <main className="board" ref={rootRef} onPointerDown={handlePointerDown}>
      {/* Describes every card. One node rather than one per card: the text is
          identical, and it is referenced by aria-describedby. */}
      <p id={KEYS_HINT_ID} className="sr-only">
        Press Space to pick this card up, then the arrow keys to move it between
        and within columns, Enter or Space to drop it, and Escape to cancel.
        Press Enter to open the card.
      </p>
      {columns.map((status) => (
        <Column
          key={status}
          status={status}
          tasks={shown.filter((t) => t.status === status && t.id !== dragTask?.id)}
          over={drag?.over === status}
          placeholder={dragTask && drag?.over === status ? drag.overIndex : null}
          placeholderHeight={drag?.height ?? 0}
          liftedId={lift?.taskId}
          onAdd={onAdd}
          onTick={onTick}
          onEdit={onEdit}
          onCardKey={handleCardKey}
          onRestore={onRestore}
          onPurge={onPurge}
        />
      ))}
      {drag && dragTask && (
        // left/top pin the clone where the card was; the live offset is a
        // transform, written straight to the node once per frame (and
        // repeated here so a re-render never puts it back a frame).
        // aria-hidden: it is a picture of the card being dragged, and the card
        // itself is still in the tree.
        <div
          className="dragwrap"
          aria-hidden="true"
          ref={cloneRef}
          style={{
            left: drag.left,
            top: drag.top,
            width: drag.width,
            transform: cloneShift(drag),
          }}
        >
          <Card task={dragTask} />
        </div>
      )}
    </main>
  );
}

interface ColumnProps {
  status: Status;
  tasks: Task[];
  over: boolean;
  /** Slot to draw the insertion placeholder in, or null for none. */
  placeholder: number | null;
  placeholderHeight: number;
  /** The card the keyboard has picked up, if it is in this column. */
  liftedId?: string;
  onAdd: (status: Status) => void;
  onTick: BoardProps['onTick'];
  onEdit: (taskId: string) => void;
  onCardKey: (taskId: string, e: ReactKeyboardEvent<HTMLElement>) => void;
  onRestore: (taskId: string) => void;
  onPurge: (taskId: string) => void;
}

function Column({
  status,
  tasks,
  over,
  placeholder,
  placeholderHeight,
  liftedId,
  onAdd,
  onTick,
  onEdit,
  onCardKey,
  onRestore,
  onPurge,
}: ColumnProps) {
  const slot =
    placeholder === null ? null : (
      <li className="slot" aria-hidden="true" style={{ height: placeholderHeight }} />
    );
  const label = STATUS_LABEL[status];
  return (
    // A named region per column, so a screen reader can jump between columns
    // and always knows which one a card belongs to.
    <section
      className={`col ${status}${over ? ' over' : ''}`}
      data-status={status}
      aria-label={`${label}, ${tasks.length} ${tasks.length === 1 ? 'card' : 'cards'}`}
    >
      <div className="colhead">
        <h2>{label}</h2> <span className="cnt">{tasks.length}</span>
        <button
          type="button"
          className="addbtn"
          aria-label={`Add task to ${label}`}
          onClick={() => onAdd(status)}
        >
          +
        </button>
      </div>
      {/* The handlers go through untouched — wrapping them per card would
          mint a new function on every render and defeat Card's memo. */}
      <ul className="cards">
        {tasks.map((t, i) => (
          <Fragment key={t.id}>
            {placeholder === i && slot}
            <li>
              <Card
                task={t}
                index={i}
                total={tasks.length}
                lifted={t.id === liftedId}
                keysHintId={KEYS_HINT_ID}
                onTick={onTick}
                onEdit={onEdit}
                onCardKey={onCardKey}
                onRestore={status === 'cancelled' ? onRestore : undefined}
                onPurge={status === 'cancelled' ? onPurge : undefined}
              />
            </li>
          </Fragment>
        ))}
        {placeholder === tasks.length && slot}
      </ul>
    </section>
  );
}
