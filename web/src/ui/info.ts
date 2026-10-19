// Info screen: versions, uptime, hostname, addresses, USB details.

import type { Ctx } from "./context";
import { h } from "./dom";
import { aliasLabel, ampStateLabel, formatUptime, localName } from "./format";
import { AMP_NAMES, CONFIDENCE_LABEL, NAMES_DISCLAIMER, PEDAL1_NAMES, PEDAL2_NAMES, REVERB_NAMES, type OriginalName } from "./names";

function namesTable(title: string, rows: readonly OriginalName[]): HTMLElement {
  return h(
    "div",
    { class: "names-group" },
    h("h3", null, title),
    h(
      "dl",
      { class: "kv names-kv" },
      ...rows.flatMap((n) => [
        h("dt", null, n.code),
        h("dd", { title: n.note }, h("span", { class: n.confidence === "inferred" ? "modest" : n.confidence === "secondary" ? "unofficial" : undefined }, `${n.confidence === "inferred" ? "~" : ""}${n.original}`), h("span", { class: "muted small" }, ` (${CONFIDENCE_LABEL[n.confidence]})`), n.note ? h("span", { class: "name-note muted small" }, n.note) : null),
      ]),
    ),
  );
}

function namesCard(): HTMLElement {
  return h(
    "section",
    { class: "card" },
    h("h2", null, "Názvy předloh"),
    h("p", null, NAMES_DISCLAIMER.cs),
    h("blockquote", { class: "quote", lang: "en" }, NAMES_DISCLAIMER.text, h("span", { class: "muted small" }, " (VOX, návod k VT100X, str. 3)")),
    h("p", { class: "muted small" }, "VOX předlohy v návodech nejmenuje, jen je popisuje. Tečkovaně podtržený název je podle neoficiálních zdrojů, ~ označuje odhad."),
    h(
      "details",
      null,
      h("summary", null, "Přehled všech modelů a efektů"),
      namesTable("Zesilovače", AMP_NAMES),
      namesTable("Pedál 1", PEDAL1_NAMES),
      namesTable("Pedál 2", PEDAL2_NAMES),
      namesTable("Reverb", REVERB_NAMES),
    ),
  );
}

export function createInfo(ctx: Ctx): { el: HTMLElement; render(): void } {
  const list = h("dl", { class: "kv" });
  const logout = h("button", { type: "button", onclick: () => ctx.logout() }, "Odhlásit");
  const el = h("div", { class: "screen" }, h("section", { class: "card" }, h("h2", null, "Informace"), list), namesCard(), h("section", { class: "card" }, logout));

  function render(): void {
    const st = ctx.data.status;
    list.replaceChildren();
    if (!st) {
      list.append(h("dt", null, "Stav"), h("dd", null, "načítám…"));
      return;
    }
    const a = ctx.data.amp ?? st.amp;
    const rows: [string, string][] = [
      ["Verze", st.version],
      ["Předchozí verze", st.update.previous_version ?? "—"],
      ["Doba běhu", formatUptime(st.uptime_s)],
      ["Hostname", st.hostname],
      ["mDNS", localName(st.mdns.hostname || st.hostname)],
      ["Alias", st.mdns.alias ? `${localName(st.mdns.alias)} (${aliasLabel(st.mdns.alias_state)})` : "—"],
      ["IP adresy", st.addresses.join(", ") || "—"],
      ["Kombo", ampStateLabel(a.state)],
    ];
    if (a.name) rows.push(["USB zařízení", [a.name, a.usb_id].filter(Boolean).join(" · ")]);
    if (a.device) rows.push(["MIDI port", a.device]);
    if (a.identity) rows.push(["Identita", a.identity]);
    if (a.error) rows.push(["Chyba USB", a.error]);
    for (const [k, v] of rows) list.append(h("dt", null, k), h("dd", null, v));
  }

  return { el, render };
}
