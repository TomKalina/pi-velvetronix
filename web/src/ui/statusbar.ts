// Always-visible status bar: Wi-Fi, addresses, USB/amp, controller connection.

import type { Ctx } from "./context";
import { h, setText } from "./dom";
import { addressList, aliasLabel, ampStateLabel, wifiSummary, wsLabel } from "./format";

type Level = "ok" | "warn" | "err" | "unknown";

function chip(title: string): { el: HTMLElement; set(text: string, level: Level, hint?: string): void } {
  const value = h("span", { class: "chip-value" });
  const el = h("div", { class: "chip chip-unknown" }, h("span", { class: "dot", "aria-hidden": "true" }), h("span", { class: "chip-title" }, title), value);
  return {
    el,
    set(text, level, hint) {
      setText(value, text);
      const cls = `chip chip-${level}`;
      if (el.className !== cls) el.className = cls;
      el.title = hint ?? "";
    },
  };
}

export function createStatusBar(ctx: Ctx): { el: HTMLElement; render(): void } {
  const wifi = chip("Wi-Fi");
  const addr = chip("Adresa");
  const amp = chip("Kombo (USB)");
  const ctl = chip("Ovladač");
  const stale = h("div", { class: "stale", hidden: true }, "Zařízení neodpovídá, zobrazený stav může být starý.");
  const el = h("div", { class: "statusbar" }, h("div", { class: "chips" }, wifi.el, addr.el, amp.el, ctl.el), stale);

  function render(): void {
    const d = ctx.data;
    stale.hidden = !d.statusError;
    const st = d.status;
    if (!st) {
      wifi.set("načítám…", "unknown");
      addr.set("načítám…", "unknown");
    } else {
      const w = st.wifi;
      const lvl: Level = d.statusError ? "unknown" : w.mode === "sta" ? "ok" : w.mode === "ap" || w.mode === "connecting" ? "warn" : "err";
      wifi.set(wifiSummary(w), lvl, w.last_error ?? undefined);
      const list = addressList(st);
      const aliasHint = st.mdns.alias ? `${st.mdns.alias}: ${aliasLabel(st.mdns.alias_state)}` : undefined;
      addr.set(list.join(" · ") || "žádná", d.statusError ? "unknown" : st.mdns.alias_state === "collision" ? "warn" : "ok", aliasHint);
    }
    if (!d.amp) amp.set("načítám…", "unknown");
    else {
      const s = d.amp.state;
      const lvl: Level = d.statusError ? "unknown" : s === "connected" ? "ok" : s === "detected" ? "warn" : "err";
      const text = d.amp.state === "error" && d.amp.error ? `${ampStateLabel(s)}: ${d.amp.error}` : ampStateLabel(s);
      amp.set(text, lvl, d.amp.name ?? undefined);
    }
    const k = d.ws.kind;
    ctl.set(wsLabel(d.ws), k === "open" ? "ok" : k === "busy" ? "err" : "warn");
  }

  return { el, render };
}
