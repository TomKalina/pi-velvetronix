// App frame: header with status bar, tab navigation, screens. Only the visible screen renders.

import type { Ctx } from "./context";
import { createControl } from "./control";
import { h } from "./dom";
import { createInfo } from "./info";
import { createSongs } from "./songs-view";
import { createStatusBar } from "./statusbar";
import { createUpdate } from "./update";
import { createWifi } from "./wifi";

interface Screen {
  id: string;
  title: string;
  /** Tail of the tab title hidden in the compact header. */
  more?: string;
  view: { el: HTMLElement; render(): void; show?(): void; hide?(): void; toolbar?: HTMLElement };
}

export function createShell(ctx: Ctx): { el: HTMLElement; render(): void; destroy(): void } {
  const bar = createStatusBar(ctx);
  const screens: Screen[] = [
    { id: "ovladani", title: "Ovládání", view: createControl(ctx) },
    { id: "pisnicky", title: "Písničky", view: createSongs(ctx) },
    { id: "wifi", title: "Wi-Fi", view: createWifi(ctx) },
    { id: "aktualizace", title: "Aktualizace", more: " firmwaru", view: createUpdate(ctx) },
    { id: "info", title: "Info", view: createInfo(ctx) },
  ];
  const tabs = screens.map((s) =>
    h("a", { href: `#${s.id}`, class: "tab" }, s.title, ...(s.more ? [h("span", { class: "tab-more" }, s.more)] : [])),
  );
  const main = h("main", null, ...screens.map((s) => s.view.el));
  // From 700 px wide the current screen's toolbar sits in the header line; below that, atop its screen.
  const headTools = h("div", { class: "head-tools" });
  const wide = window.matchMedia("(min-width: 700px)");
  function placeTools(): void {
    for (const s of screens) {
      const t = s.view.toolbar;
      if (!t) continue;
      if (wide.matches) headTools.append(t);
      else if (t.parentElement !== s.view.el) s.view.el.prepend(t);
    }
  }
  wide.addEventListener("change", placeTools);
  const el = h(
    "div",
    { class: "app" },
    h("header", { class: "top" }, h("div", { class: "brand" }, "VOX", h("span", { class: "brand-more" }, "Velvetronix")), headTools, bar.el),
    // Outside the header so it alone can stick to the top.
    h("nav", { class: "tabs" }, ...tabs),
    main,
  );

  let current: Screen = screens[0];
  function route(): void {
    const id = location.hash.slice(1);
    const next = screens.find((s) => s.id === id) ?? screens[0];
    if (next !== current) current.view.hide?.();
    current = next;
    screens.forEach((s, i) => {
      s.view.el.hidden = s !== current;
      tabs[i].classList.toggle("active", s === current);
      if (s === current) tabs[i].setAttribute("aria-current", "page");
      else tabs[i].removeAttribute("aria-current");
    });
    headTools.hidden = !current.view.toolbar;
    current.view.show?.();
    render();
  }
  window.addEventListener("hashchange", route);

  function render(): void {
    bar.render();
    current.view.render();
  }

  placeTools();
  route();
  return {
    el,
    render,
    destroy: () => {
      window.removeEventListener("hashchange", route);
      wide.removeEventListener("change", placeTools);
    },
  };
}
