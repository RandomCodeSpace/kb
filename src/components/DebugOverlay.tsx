import { useEffect, useMemo, useRef, useState } from 'react';
import { setFrameCap } from '../lib/confetti';

const DEBUG_KEY = 'kb.debug.v1';
/** Rolling window and readout rate: 60 frames averaged, refreshed 4×/sec. */
const FPS_WINDOW = 60;
const FPS_INTERVAL_MS = 250;

export type FrameTarget = 60 | 90 | 120 | 'uncapped';
const TARGETS: FrameTarget[] = [60, 90, 120, 'uncapped'];

function readFlag(): boolean {
  try {
    return localStorage.getItem(DEBUG_KEY) === '1';
  } catch {
    return false;
  }
}

/** Persist the flag so the overlay survives a reload. Storage may be gone. */
export function setDebugEnabled(on: boolean): void {
  try {
    if (on) localStorage.setItem(DEBUG_KEY, '1');
    else localStorage.removeItem(DEBUG_KEY);
  } catch {
    // Storage unavailable — the flag lives for this session only.
  }
}

/**
 * Whether the overlay should mount. The settings modal writes the stored flag
 * and is the normal way in; `?debug=1` / `?debug=0` stay as a support override
 * for a machine nobody can click through, and win for that load. Both are
 * persisted, so reading has the side effect of making an explicit URL choice
 * survive the next reload.
 */
export function debugEnabled(
  search: string = typeof location === 'undefined' ? '' : location.search,
): boolean {
  const raw = new URLSearchParams(search).get('debug');
  if (raw === null) return readFlag();
  const on = raw !== '0' && raw.toLowerCase() !== 'false';
  setDebugEnabled(on);
  return on;
}

interface RendererCaps {
  webgpu: boolean;
  webgl2: boolean;
}

/**
 * Honest availability probe: whether the browser exposes WebGPU and whether a
 * WebGL2 context can actually be created. Neither is used to draw the board
 * (DOM) or the confetti (canvas 2D) — this only reports what the machine has.
 */
function rendererCaps(): RendererCaps {
  const webgpu = typeof navigator !== 'undefined' && 'gpu' in navigator;
  let webgl2 = false;
  try {
    const gl = document.createElement('canvas').getContext('webgl2');
    webgl2 = gl !== null;
    // Free the context immediately; a probe must not hold a GPU resource.
    (gl?.getExtension('WEBGL_lose_context') as { loseContext(): void } | null)
      ?.loseContext();
  } catch {
    webgl2 = false;
  }
  return { webgpu, webgl2 };
}

export interface DebugOverlayProps {
  onClose: () => void;
  /** Set while a dialog owns the page, so Tab cannot land in here behind it. */
  inert?: boolean;
}

/** The four numbers an overlap needs; DOMRect satisfies it. */
export interface Box {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

/**
 * Whether the panel would sit on top of the focused element. The panel is
 * opaque, so anything under it is not merely dimmed but gone — which is exactly
 * what 2.4.11 Focus Not Obscured forbids for the element that has focus.
 */
export function overlaps(a: Box, b: Box): boolean {
  return (
    a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top
  );
}

/** Distance from the viewport edges, matching `.debug` in the stylesheet. */
const DEBUG_INSET = 12;

/**
 * Where the panel sits when it is not stepping aside — bottom right. Computed
 * rather than measured so the decision is always about the *resting* place:
 * measuring the moved panel would find no overlap, move it back, and flap.
 */
export function restingBox(
  width: number,
  height: number,
  viewport: { width: number; height: number },
  inset: number = DEBUG_INSET,
): Box {
  return {
    left: viewport.width - inset - width,
    right: viewport.width - inset,
    top: viewport.height - inset - height,
    bottom: viewport.height - inset,
  };
}

/**
 * Development heads-up display: frame rate, renderer availability and a frame
 * cap for the confetti loop. Mounted only when the flag is on, so a disabled
 * overlay costs nothing — no requestAnimationFrame runs.
 */
export function DebugOverlay({ onClose, inert }: DebugOverlayProps) {
  const [fps, setFps] = useState<number | null>(null);
  const [target, setTarget] = useState<FrameTarget>('uncapped');
  // Moved to the opposite corner while it would otherwise cover the focused
  // element (the cancelled column's Restore / Delete permanently buttons sit
  // right under its resting place).
  const [aside, setAside] = useState(false);
  const boxRef = useRef<HTMLElement>(null);
  const caps = useMemo(rendererCaps, []);

  useEffect(() => {
    const check = () => {
      const box = boxRef.current;
      const el = document.activeElement;
      if (!box || !(el instanceof HTMLElement) || el === document.body) return;
      if (box.contains(el)) return;
      const r = box.getBoundingClientRect();
      const rest = restingBox(r.width, r.height, {
        width: window.innerWidth,
        height: window.innerHeight,
      });
      setAside(overlaps(rest, el.getBoundingClientRect()));
    };
    window.addEventListener('focusin', check);
    window.addEventListener('focusout', check);
    return () => {
      window.removeEventListener('focusin', check);
      window.removeEventListener('focusout', check);
    };
  }, []);

  useEffect(() => {
    setFrameCap(target === 'uncapped' ? null : target);
    // Leaving the overlay must not leave the app throttled.
    return () => setFrameCap(null);
  }, [target]);

  useEffect(() => {
    const deltas: number[] = [];
    let last = performance.now();
    let shownAt = last;
    let raf = requestAnimationFrame(function tick(now: number) {
      deltas.push(now - last);
      last = now;
      if (deltas.length > FPS_WINDOW) deltas.shift();
      if (now - shownAt >= FPS_INTERVAL_MS) {
        shownAt = now;
        const avg = deltas.reduce((a, b) => a + b, 0) / deltas.length;
        setFps(avg > 0 ? Math.round(1000 / avg) : null);
      }
      raf = requestAnimationFrame(tick);
    });
    return () => cancelAnimationFrame(raf);
  }, []);

  return (
    // Escape is handled on the panel rather than on window so it cannot steal
    // the key from an open modal; the close button keeps it reachable by tab.
    <aside
      className="debug"
      aria-label="Debug overlay"
      ref={boxRef}
      inert={inert}
      // Out of the focused element's way; `bottom: auto` releases the resting
      // corner declared in the stylesheet.
      style={aside ? { top: DEBUG_INSET, bottom: 'auto' } : undefined}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onClose();
      }}
    >
      <div className="debug-row">
        <span className="debug-title">debug</span>
        <button
          type="button"
          className="debug-x"
          aria-label="Hide debug overlay"
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <div className="debug-row">
        <span className="k">display</span>
        <span className="v">{fps === null ? 'measuring…' : `${fps} fps`}</span>
      </div>
      <div className="debug-row">
        <span className="k">WebGPU</span>
        <span className={`v ${caps.webgpu ? 'yes' : 'no'}`}>
          {caps.webgpu ? 'available' : 'unavailable'}
        </span>
      </div>
      <div className="debug-row">
        <span className="k">WebGL2</span>
        <span className={`v ${caps.webgl2 ? 'yes' : 'no'}`}>
          {caps.webgl2 ? 'available' : 'unavailable'}
        </span>
      </div>
      <label htmlFor="debug-target">Frame cap</label>
      <select
        id="debug-target"
        value={String(target)}
        onChange={(e) => {
          const v = e.target.value;
          setTarget(v === 'uncapped' ? 'uncapped' : (Number(v) as FrameTarget));
        }}
      >
        {TARGETS.map((t) => (
          <option key={String(t)} value={String(t)}>
            {t === 'uncapped' ? 'uncapped' : `${t} fps`}
          </option>
        ))}
      </select>
      <p className="debug-note">
        The cap only limits the app's animation loop (canvas 2D confetti); it
        cannot raise your display's refresh rate. The board itself is DOM — it
        uses neither WebGPU nor WebGL.
      </p>
    </aside>
  );
}
