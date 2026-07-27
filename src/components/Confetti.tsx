import { useEffect, useRef } from 'react';
import { startLoop } from '../lib/confetti';

/** Fixed full-screen overlay canvas driven by the shared confetti loop. */
export function Confetti() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    return startLoop(canvas);
  }, []);

  return <canvas ref={canvasRef} className="fx" aria-hidden="true" />;
}
