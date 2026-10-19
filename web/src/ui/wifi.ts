// Wi-Fi settings screen.

import type { Network, Saved } from "../api";
import type { Ctx } from "./context";
import { clear, h, setText } from "./dom";
import { wifiErrorText, wifiLastErrorText } from "./errors";
import { formatSignal, localName, wifiModeLabel } from "./format";
import { validateHotspotMinutes, validateWifiForm } from "./logic";

export function createWifi(ctx: Ctx): { el: HTMLElement; render(): void; show(): void } {
  // --- current state ---
  const stateList = h("dl", { class: "kv" });
  const lastErr = h("p", { class: "msg msg-warn", hidden: true });

  // --- scan ---
  const netList = h("ul", { class: "list" });
  const savedList = h("ul", { class: "list" });
  const listMsg = h("p", { class: "msg", hidden: true });
  const scanBtn = h("button", { type: "button", onclick: () => load(true) }, "Hledat sítě");

  // --- connect form ---
  const ssid = h("input", { type: "text", id: "wifi-ssid", autocomplete: "off", spellcheck: false, required: true });
  const pass = h("input", { type: "password", id: "wifi-pass", autocomplete: "new-password", required: true });
  const connMsg = h("p", { class: "msg", hidden: true });
  const connBtn = h("button", { type: "submit", class: "primary" }, "Připojit");
  const connectForm = h(
    "form",
    {
      onsubmit: async (e: Event) => {
        e.preventDefault();
        const err = validateWifiForm(ssid.value, pass.value);
        if (err) return msg(connMsg, err, "err");
        connBtn.disabled = true;
        try {
          await ctx.api.wifiConnect(ssid.value, pass.value);
          pass.value = "";
          msg(connMsg, `Připojuji k síti „${ssid.value}“… Výsledek uvidíte nahoře ve stavu Wi-Fi.`, "ok");
          void ctx.refreshStatus();
        } catch (ex) {
          msg(connMsg, wifiErrorText(ex), "err");
        } finally {
          connBtn.disabled = false;
        }
      },
    },
    h("div", { class: "row" }, h("label", { htmlFor: ssid.id }, "Název sítě (SSID)"), ssid),
    h("div", { class: "row" }, h("label", { htmlFor: pass.id }, "Heslo"), pass),
    h("p", { class: "muted small" }, "Pokud jste teď připojeni přes hotspot zařízení, spojení se po přepnutí přeruší. Zařízení pak najdete v domácí síti na adrese velvetronix.local."),
    connBtn,
    connMsg,
  );

  // --- hotspot ---
  const minutes = h("input", { type: "number", id: "hs-min", min: 1, max: 30, step: 1, value: "10", inputMode: "numeric" });
  const hsBtn = h("button", { type: "button" });
  const hsMsg = h("p", { class: "msg", hidden: true });
  const hsInfo = h("p", { class: "muted small" });
  const hsLabel = () => {
    const n = minutes.valueAsNumber;
    setText(hsBtn, validateHotspotMinutes(n) ? "Spustit hotspot" : `Spustit hotspot na ${n} min`);
  };
  minutes.addEventListener("input", hsLabel);
  hsLabel();
  hsBtn.addEventListener("click", async () => {
    const n = minutes.valueAsNumber;
    const err = validateHotspotMinutes(n);
    if (err) return msg(hsMsg, err, "err");
    const w = ctx.data.status?.wifi;
    if (!confirm(`Spustit hotspot na ${n} min? Aktuální připojení k Wi-Fi se přeruší a tato stránka přestane odpovídat.`)) return;
    hsBtn.disabled = true;
    try {
      await ctx.api.wifiHotspot(n);
      msg(hsMsg, `Hotspot se spouští. Připojte se k síti „${w?.ap_ssid ?? "VOX-Velvetronix"}“ a otevřete http://${w?.ap_ip ?? "10.42.0.1"}/.`, "ok");
    } catch (ex) {
      msg(hsMsg, wifiErrorText(ex), "err");
    } finally {
      hsBtn.disabled = false;
    }
  });

  const el = h(
    "div",
    { class: "screen" },
    h("section", { class: "card" }, h("h2", null, "Stav Wi-Fi"), stateList, lastErr),
    h("section", { class: "card" }, h("div", { class: "card-head" }, h("h2", null, "Dostupné sítě"), scanBtn), listMsg, netList),
    h("section", { class: "card" }, h("h2", null, "Připojit k síti"), connectForm),
    h("section", { class: "card" }, h("h2", null, "Uložené sítě"), savedList),
    h(
      "section",
      { class: "card" },
      h("h2", null, "Hotspot"),
      hsInfo,
      h("div", { class: "row" }, h("label", { htmlFor: minutes.id }, "Doba (minuty, 1–30)"), minutes),
      hsBtn,
      hsMsg,
    ),
  );

  let loading = false;
  async function load(rescan: boolean): Promise<void> {
    if (loading) return;
    loading = true;
    scanBtn.disabled = true;
    if (rescan) msg(listMsg, "Hledám sítě…", "warn");
    try {
      const r = await ctx.api.wifiNetworks(rescan);
      listMsg.hidden = true;
      renderNetworks(r.networks);
      renderSaved(r.saved);
    } catch (ex) {
      msg(listMsg, wifiErrorText(ex), "err");
    } finally {
      loading = false;
      scanBtn.disabled = false;
    }
  }

  function renderNetworks(nets: Network[]): void {
    clear(netList);
    if (nets.length === 0) netList.append(h("li", { class: "muted" }, "Žádné sítě nenalezeny."));
    for (const n of [...nets].sort((a, b) => b.signal - a.signal)) {
      const pick = () => {
        ssid.value = n.ssid;
        pass.value = "";
        pass.focus();
      };
      netList.append(
        h(
          "li",
          null,
          h(
            "button",
            { type: "button", class: "list-item", onclick: pick },
            h("span", { class: "grow" }, n.ssid || "(skrytá síť)"),
            n.in_use && h("span", { class: "tag tag-ok" }, "připojeno"),
            n.saved && !n.in_use && h("span", { class: "tag" }, "uložená"),
            h("span", { class: "muted small" }, `${formatSignal(n.signal)} · ${n.security || "otevřená"}`),
          ),
        ),
      );
    }
  }

  function renderSaved(saved: Saved[]): void {
    clear(savedList);
    if (saved.length === 0) savedList.append(h("li", { class: "muted" }, "Žádné uložené sítě."));
    for (const s of saved) {
      const forget = h("button", { type: "button", class: "danger" }, "Zapomenout");
      forget.addEventListener("click", async () => {
        if (!confirm(`Zapomenout síť „${s.ssid || s.name}“? Uložené heslo se smaže.`)) return;
        forget.disabled = true;
        try {
          await ctx.api.wifiForget(s.uuid);
          await load(false);
        } catch (ex) {
          msg(listMsg, wifiErrorText(ex), "err");
          forget.disabled = false;
        }
      });
      savedList.append(
        h(
          "li",
          { class: "list-row" },
          h("span", { class: "grow" }, s.ssid || s.name, s.name !== s.ssid && h("span", { class: "muted small" }, ` (${s.name})`)),
          s.active ? h("span", { class: "tag tag-ok" }, "aktivní") : forget,
        ),
      );
    }
  }

  function render(): void {
    const st = ctx.data.status;
    clear(stateList);
    if (!st) {
      stateList.append(h("dt", null, "Stav"), h("dd", null, "načítám…"));
      return;
    }
    const w = st.wifi;
    const rows: [string, string][] = [["Režim", wifiModeLabel(w.mode)]];
    if (w.mode === "ap") rows.push(["Hotspot", w.ap_ssid], ["IP", w.ap_ip]);
    else {
      rows.push(["Síť", w.ssid ?? "—"], ["IP", w.ip ?? "—"]);
      if (w.signal !== null) rows.push(["Signál", formatSignal(w.signal)]);
    }
    rows.push(["Adresa", localName(st.mdns.hostname || st.hostname)]);
    for (const [k, v] of rows) stateList.append(h("dt", null, k), h("dd", null, v));
    lastErr.hidden = !w.last_error;
    if (w.last_error) setText(lastErr, wifiLastErrorText(w.last_error));
    setText(
      hsInfo,
      `Zařízení vytvoří vlastní síť „${w.ap_ssid}“ (adresa http://${w.ap_ip}/). Heslo k ní najdete ve výstupu instalátoru. ` +
        "Po uplynutí doby se zařízení vrátí do domácí sítě, pokud je dostupná. Aktuální připojení se při spuštění přeruší.",
    );
  }

  return { el, render, show: () => void load(false) };
}

function msg(el: HTMLElement, text: string, kind: "ok" | "warn" | "err"): void {
  el.textContent = text;
  el.className = `msg msg-${kind}`;
  el.hidden = false;
}
