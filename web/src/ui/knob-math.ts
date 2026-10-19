// Pure knob math: angles, drag, wheel and keyboard steps (no DOM).

/** The pointer sweeps 270 degrees, from -135 (min, about 7 o'clock) to +135 (max, about 5 o'clock). */
export const KNOB_MIN_ANGLE = -135;
export const KNOB_MAX_ANGLE = 135;
/** Pixels of vertical drag for the full range; Shift makes it 4x finer. */
export const DRAG_FULL_PX = 220;
export const FINE_FACTOR = 4;

export function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}

/** Arrow-key step for a range: 1 for 0..100, coarser for wide ranges (ms, mHz). */
export function knobStep(min: number, max: number): number {
  const span = max - min;
  if (span <= 100) return 1;
  if (span <= 2000) return 5;
  return 50;
}

/** Rounds to the step grid anchored at min, then clamps. */
export function snap(v: number, min: number, max: number, step: number): number {
  const s = min + Math.round((v - min) / step) * step;
  return clamp(s, min, max);
}

export function valueToAngle(v: number, min: number, max: number): number {
  const f = max === min ? 0 : (clamp(v, min, max) - min) / (max - min);
  return KNOB_MIN_ANGLE + f * (KNOB_MAX_ANGLE - KNOB_MIN_ANGLE);
}

/** Value after dragging dy px from where the drag started (up = negative dy = louder). */
export function dragValue(start: number, dy: number, min: number, max: number, step: number, fine: boolean): number {
  const perPx = (max - min) / (DRAG_FULL_PX * (fine ? FINE_FACTOR : 1));
  return snap(start - dy * perPx, min, max, step);
}

/** One wheel notch = one step; wheel up (negative deltaY) increases. */
export function wheelValue(v: number, deltaY: number, min: number, max: number, step: number): number {
  if (deltaY === 0) return v;
  return snap(v + (deltaY < 0 ? step : -step), min, max, step);
}

/**
 * Keyboard per WAI-ARIA slider: arrows +-step, PageUp/PageDown +-10 steps, Home/End = min/max.
 * Returns null for keys the knob does not handle.
 */
export function keyValue(key: string, v: number, min: number, max: number, step: number): number | null {
  switch (key) {
    case "ArrowUp":
    case "ArrowRight": return snap(v + step, min, max, step);
    case "ArrowDown":
    case "ArrowLeft": return snap(v - step, min, max, step);
    case "PageUp": return snap(v + step * 10, min, max, step);
    case "PageDown": return snap(v - step * 10, min, max, step);
    case "Home": return min;
    case "End": return max;
    default: return null;
  }
}

/** SVG arc path on a circle (angles in degrees, 0 = 12 o'clock, clockwise). */
export function arcPath(cx: number, cy: number, r: number, from: number, to: number): string {
  const pt = (a: number) => {
    const rad = ((a - 90) * Math.PI) / 180;
    return `${(cx + r * Math.cos(rad)).toFixed(2)} ${(cy + r * Math.sin(rad)).toFixed(2)}`;
  };
  if (to - from < 0.01) return `M ${pt(from)}`;
  const large = to - from > 180 ? 1 : 0;
  return `M ${pt(from)} A ${r} ${r} 0 ${large} 1 ${pt(to)}`;
}
