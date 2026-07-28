export const CONFETTI_COLORS = ['#ff7b54', '#4f8ef7', '#3f9d58', '#b98af7', '#ffb020'];

interface Particle {
  x: number; y: number; vx: number; vy: number;
  t: number; life: number; r: number; rot: number; vr: number; c: string;
}

const parts: Particle[] = [];

/** Re-arms the parked animation loop; set by startLoop while it is mounted. */
let wake: (() => void) | null = null;

/**
 * Whether the user has asked for reduced motion. Read per call rather than
 * cached: the setting can change while the page is open, and a stale answer
 * would either keep animating at someone who asked us not to, or silently
 * disable the effect for the rest of the session.
 */
export function reducedMotion(): boolean {
  try {
    return (
      typeof matchMedia === 'function' &&
      matchMedia('(prefers-reduced-motion: reduce)').matches
    );
  } catch {
    // No matchMedia (tests, very old browsers): assume no preference.
    return false;
  }
}

export function burst(x: number, y: number, n: number): void {
  // Confetti is decoration and nothing else, so reduced motion means none of
  // it — and with no particles the loop never wakes, which costs nothing.
  if (reducedMotion()) return;
  for (let i = 0; i < n; i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = 100 + Math.random() * 430;
    parts.push({
      x, y,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 150,
      t: 0, life: 0.5 + Math.random() * 0.7,
      r: 2.5 + Math.random() * 4,
      rot: Math.random() * 6, vr: (Math.random() - 0.5) * 16,
      c: CONFETTI_COLORS[Math.floor(Math.random() * CONFETTI_COLORS.length)],
    });
  }
  wake?.();
}

/** Particles still in flight — the loop parks itself when this reaches 0. */
export function pending(): number {
  return parts.length;
}

let minFrameMs = 0;

/**
 * Cap the animation loop's frame rate (debug overlay). A cap can only *limit*
 * the rate — it can never raise it above the display refresh. `null` (or a
 * non-positive target) means uncapped.
 */
export function setFrameCap(fps: number | null): void {
  minFrameMs = fps !== null && fps > 0 ? 1000 / fps : 0;
}

/**
 * Whether a frame arriving at `now` may render given the cap and the time of
 * the last rendered frame. The 1ms slack keeps a 60Hz display capped at 60
 * from halving to 30 on ordinary frame jitter.
 */
export function frameDue(now: number, last: number): boolean {
  return minFrameMs <= 0 || now - last >= minFrameMs - 1;
}

/**
 * Drives the overlay canvas; called once from the Confetti component. The
 * loop only runs while particles exist: an idle board must not spend a
 * requestAnimationFrame callback and a full-viewport clearRect on every
 * display refresh for the rest of the session. burst() wakes it again.
 */
export function startLoop(canvas: HTMLCanvasElement): () => void {
  const ctx = canvas.getContext('2d')!;
  // 0 means parked — no frame is scheduled.
  let raf = 0;
  let last = performance.now();
  const dpr = Math.min(window.devicePixelRatio || 1, 2);

  const resize = () => {
    canvas.width = window.innerWidth * dpr;
    canvas.height = window.innerHeight * dpr;
    canvas.style.width = `${window.innerWidth}px`;
    canvas.style.height = `${window.innerHeight}px`;
  };
  resize();
  window.addEventListener('resize', resize);

  const loop = (now: number) => {
    raf = 0;
    // Frame skipped by the cap: `last` stays put, so the next rendered frame
    // integrates the full elapsed time and the motion keeps its real speed.
    if (!frameDue(now, last)) {
      raf = requestAnimationFrame(loop);
      return;
    }
    const dt = Math.min((now - last) / 1000, 0.05);
    last = now;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, window.innerWidth, window.innerHeight);
    for (let i = parts.length - 1; i >= 0; i--) {
      const p = parts[i];
      p.t += dt;
      if (p.t > p.life) {
        parts.splice(i, 1);
        continue;
      }
      p.vy += 900 * dt;
      p.x += p.vx * dt;
      p.y += p.vy * dt;
      p.rot += p.vr * dt;
      ctx.globalAlpha = Math.max(0, 1 - p.t / p.life);
      ctx.fillStyle = p.c;
      ctx.save();
      ctx.translate(p.x, p.y);
      ctx.rotate(p.rot);
      ctx.fillRect(-p.r, -p.r * 0.6, p.r * 2, p.r * 1.2);
      ctx.restore();
    }
    ctx.globalAlpha = 1;
    // The frame that removed the last particle has already cleared the
    // canvas, so parking here leaves nothing drawn behind.
    if (parts.length > 0) raf = requestAnimationFrame(loop);
  };

  const schedule = () => {
    if (raf !== 0) return;
    // A parked loop has no meaningful `last`; restart the clock so the first
    // frame after a wake integrates one frame's worth of time, not the gap.
    last = performance.now();
    raf = requestAnimationFrame(loop);
  };
  wake = schedule;
  // Particles may already be in flight if the canvas remounted mid-burst.
  if (parts.length > 0) schedule();

  return () => {
    if (raf !== 0) cancelAnimationFrame(raf);
    raf = 0;
    if (wake === schedule) wake = null;
    window.removeEventListener('resize', resize);
  };
}
