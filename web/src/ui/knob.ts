// Rotary knob (role="slider"): vertical drag (Shift = fine), wheel while focused, keyboard per WAI-ARIA.

import { h, setText } from "./dom";
import { arcPath, dragValue, keyValue, knobStep, KNOB_MAX_ANGLE, KNOB_MIN_ANGLE, valueToAngle, wheelValue } from "./knob-math";

export interface KnobOptions {
  label: string;
  min: number;
  max: number;
  step?: number;
  size?: "lg" | "md" | "sm";
  /** Readout and aria-valuetext. */
  format(v: number): string;
  /** User changed the value (already snapped and clamped). */
  onInput(v: number): void;
  /** Number of tick marks around the dial (0 = none). */
  ticks?: number;
}

export interface Knob {
  el: HTMLElement;
  set(v: number): void;
  setLabel(text: string): void;
  /** Re-renders readout and aria-valuetext (after the format's output changed, e.g. names switch). */
  refresh(): void;
  setDisabled(disabled: boolean): void;
}

const NS = "http://www.w3.org/2000/svg";
const C = 50;
const R_ARC = 44;

function svgEl(tag: string, attrs: Record<string, string>): SVGElement {
  const e = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  return e;
}

export function createKnob(o: KnobOptions): Knob {
  const step = o.step ?? knobStep(o.min, o.max);
  let value = o.min;
  let disabled = false;

  const svg = svgEl("svg", { viewBox: "0 0 100 100", class: "knob-dial", "aria-hidden": "true" });
  if (o.ticks) {
    for (let i = 0; i < o.ticks; i++) {
      const a = KNOB_MIN_ANGLE + (i * (KNOB_MAX_ANGLE - KNOB_MIN_ANGLE)) / (o.ticks - 1);
      svg.append(svgEl("line", { x1: "50", y1: "1.5", x2: "50", y2: "5.5", class: "knob-tick", transform: `rotate(${a} 50 50)` }));
    }
  }
  svg.append(svgEl("path", { d: arcPath(C, C, R_ARC, KNOB_MIN_ANGLE, KNOB_MAX_ANGLE), class: "knob-track" }));
  const arc = svgEl("path", { d: "", class: "knob-arc" });
  svg.append(arc);
  svg.append(svgEl("circle", { cx: "50", cy: "50", r: "34", class: "knob-skirt" }));
  svg.append(svgEl("circle", { cx: "50", cy: "50", r: "27", class: "knob-cap" }));
  const pointer = svgEl("g", { class: "knob-pointer" });
  pointer.append(svgEl("line", { x1: "50", y1: "20", x2: "50", y2: "36" }));
  svg.append(pointer);

  const readout = h("span", { class: "knob-value" });
  const label = h("span", { class: "knob-label" }, o.label);
  const el = h(
    "div",
    {
      class: `knob knob-${o.size ?? "md"}`,
      role: "slider",
      tabIndex: 0,
      "aria-valuemin": String(o.min),
      "aria-valuemax": String(o.max),
      "aria-label": o.label,
    },
  );
  el.append(svg, readout, label);

  function paint(): void {
    const a = valueToAngle(value, o.min, o.max);
    arc.setAttribute("d", arcPath(C, C, R_ARC, KNOB_MIN_ANGLE, a));
    pointer.setAttribute("transform", `rotate(${a.toFixed(2)} 50 50)`);
    const text = o.format(value);
    setText(readout, text);
    el.setAttribute("aria-valuenow", String(value));
    el.setAttribute("aria-valuetext", text);
  }

  function input(v: number): void {
    if (disabled || v === value) return;
    value = v;
    paint();
    o.onInput(v);
  }

  // Drag: vertical, relative to where it started; the pointer is captured so it can leave the knob.
  let drag: { id: number; y: number; start: number } | null = null;
  svg.addEventListener("pointerdown", (e) => {
    if (disabled || e.button !== 0) return;
    e.preventDefault();
    el.focus({ preventScroll: true });
    drag = { id: e.pointerId, y: e.clientY, start: value };
    svg.setPointerCapture(e.pointerId);
    el.classList.add("knob-active");
  });
  svg.addEventListener("pointermove", (e) => {
    if (!drag || e.pointerId !== drag.id) return;
    input(dragValue(drag.start, e.clientY - drag.y, o.min, o.max, step, e.shiftKey));
  });
  const end = (e: PointerEvent) => {
    if (!drag || e.pointerId !== drag.id) return;
    drag = null;
    el.classList.remove("knob-active");
  };
  svg.addEventListener("pointerup", end);
  svg.addEventListener("pointercancel", end);

  // Wheel only while focused, so scrolling the page over the panel never turns a knob.
  el.addEventListener("wheel", (e) => {
    if (disabled || document.activeElement !== el) return;
    e.preventDefault();
    input(wheelValue(value, e.deltaY, o.min, o.max, step));
  }, { passive: false });

  el.addEventListener("keydown", (e) => {
    if (disabled) return;
    const v = keyValue(e.key, value, o.min, o.max, step);
    if (v === null) return;
    e.preventDefault();
    input(v);
  });

  paint();
  return {
    el,
    set(v) {
      // Values from the amp are shown as they are, only clamped.
      const next = Math.min(o.max, Math.max(o.min, v));
      if (next === value) return;
      value = next;
      paint();
    },
    refresh: paint,
    setLabel(text) {
      setText(label, text);
      el.setAttribute("aria-label", text);
    },
    setDisabled(d) {
      if (d === disabled) return;
      disabled = d;
      el.tabIndex = d ? -1 : 0;
      el.setAttribute("aria-disabled", String(d));
      el.classList.toggle("knob-disabled", d);
    },
  };
}
