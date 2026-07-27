export const CONFETTI_COLORS = ['#ff7b54', '#4f8ef7', '#3f9d58', '#b98af7', '#ffb020'];

interface Particle {
  x: number; y: number; vx: number; vy: number;
  t: number; life: number; r: number; rot: number; vr: number; c: string;
}

const parts: Particle[] = [];

export function burst(x: number, y: number, n: number): void {
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
}

/** Drives the overlay canvas; called once from the Confetti component. */
export function startLoop(canvas: HTMLCanvasElement): () => void {
  const ctx = canvas.getContext('2d')!;
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
    raf = requestAnimationFrame(loop);
  };
  raf = requestAnimationFrame(loop);
  return () => {
    cancelAnimationFrame(raf);
    window.removeEventListener('resize', resize);
  };
}
