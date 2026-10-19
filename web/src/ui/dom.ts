// Minimal DOM helpers.

type Child = Node | string | null | undefined | false;
type Props = Record<string, unknown>;

/** h("button", { class: "x", onclick: fn, disabled: true }, "text") */
export function h<K extends keyof HTMLElementTagNameMap>(tag: K, props: Props | null = null, ...children: Child[]): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  if (props) {
    for (const [k, v] of Object.entries(props)) {
      if (v === undefined || v === null || v === false) continue;
      if (k === "class") el.className = String(v);
      else if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v as EventListener);
      else if (k in el) (el as unknown as Record<string, unknown>)[k] = v;
      else el.setAttribute(k, v === true ? "" : String(v));
    }
  }
  append(el, ...children);
  return el;
}

export function append(el: Element, ...children: Child[]): void {
  for (const c of children) if (c !== null && c !== undefined && c !== false) el.append(c);
}

export function clear(el: Element): void {
  el.replaceChildren();
}

/** Sets text only when it changed (avoids layout work on frequent renders). */
export function setText(el: Element, text: string): void {
  if (el.textContent !== text) el.textContent = text;
}

let toastBox: HTMLElement | null = null;
const recent = new Map<string, number>();

/** Short notification; the same text within 2 s is shown once. */
export function toast(text: string, kind: "info" | "error" = "error"): void {
  const now = Date.now();
  if ((recent.get(text) ?? 0) > now - 2000) return;
  recent.set(text, now);
  if (!toastBox) {
    toastBox = h("div", { class: "toasts", role: "status", "aria-live": "polite" });
    document.body.append(toastBox);
  }
  const t = h("div", { class: `toast toast-${kind}` }, text);
  toastBox.append(t);
  setTimeout(() => t.remove(), 5000);
}
