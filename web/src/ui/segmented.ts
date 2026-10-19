// Small segmented switch (a group of toggle buttons, one pressed).

import { h } from "./dom";

export interface Segmented<T extends string> {
  el: HTMLElement;
  render(): void;
}

export function segmented<T extends string>(
  label: string, options: readonly (readonly [T, string])[], get: () => T, set: (v: T) => void, cls = "",
): Segmented<T> {
  const buttons = options.map(([value, text]) =>
    h("button", { type: "button", class: "seg-btn", onclick: () => { if (get() !== value) set(value); } }, text),
  );
  const el = h(
    "div",
    { class: `seg ${cls}`.trim(), role: "group", "aria-label": label },
    h("span", { class: "seg-label" }, label),
    h("span", { class: "seg-opts" }, ...buttons),
  );
  function render(): void {
    const cur = get();
    buttons.forEach((b, i) => {
      const on = options[i][0] === cur;
      b.classList.toggle("on", on);
      b.setAttribute("aria-pressed", String(on));
    });
  }
  render();
  return { el, render };
}
