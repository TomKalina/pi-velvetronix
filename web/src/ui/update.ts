// Firmware update screen.

import type { UpdateInfo } from "../api";
import type { Ctx } from "./context";
import { h, setText } from "./dom";
import { lastResultText, updateErrorText } from "./errors";
import { formatBytes } from "./format";
import { precheckUpdate, updateInProgress, updatePollStep } from "./logic";

const POLL_MS = 2000;

const STATE_LABEL: Record<UpdateInfo["state"], string> = {
  idle: "připraveno",
  staged: "balíček připraven k instalaci",
  applying: "instaluji",
};

export function createUpdate(ctx: Ctx): { el: HTMLElement; render(): void } {
  const info = h("dl", { class: "kv" });
  const file = h("input", { type: "file", id: "upd-file", accept: ".tar.gz,.tgz,application/gzip" });
  const fileMsg = h("p", { class: "msg", hidden: true });
  const btn = h("button", { type: "button", class: "primary", disabled: true }, "Nahrát a nainstalovat");
  const progress = h("progress", { max: 100, value: 0, hidden: true });
  const phase = h("p", { class: "msg", hidden: true, role: "status" });
  let busy = false;

  const show = (el: HTMLElement, text: string, kind: "ok" | "warn" | "err") => {
    el.textContent = text;
    el.className = `msg msg-${kind}`;
    el.hidden = false;
  };

  file.addEventListener("change", async () => {
    btn.disabled = true;
    fileMsg.hidden = true;
    const f = file.files?.[0];
    if (!f) return;
    const head = new Uint8Array(await f.slice(0, 8).arrayBuffer());
    const err = precheckUpdate(f.name, f.size, head);
    if (err) return show(fileMsg, err, "err");
    show(fileMsg, `${f.name} (${formatBytes(f.size)})`, "ok");
    btn.disabled = busy;
  });

  btn.addEventListener("click", async () => {
    const f = file.files?.[0];
    if (!f || busy) return;
    if (!confirm("Nainstalovat aktualizaci? Zařízení se restartuje a ovládání se asi na minutu odpojí. Nastavení komba se nemění.")) return;
    busy = true;
    btn.disabled = true;
    const before: UpdateInfo | null = ctx.data.update ?? (await ctx.api.updateInfo().catch(() => null));
    progress.hidden = false;
    progress.value = 0;
    show(phase, "Nahrávám…", "warn");
    try {
      const r = await ctx.api.uploadUpdate(f, (loaded, total) => {
        progress.value = total > 0 ? Math.round((loaded / total) * 100) : 0;
      });
      progress.value = 100;
      show(phase, `Instaluji a restartuji… (verze ${r.version})`, "warn");
      await waitForRestart(before);
    } catch (ex) {
      show(phase, updateErrorText(ex), "err");
    } finally {
      busy = false;
      progress.hidden = true;
      btn.disabled = !file.files?.[0];
    }
  });

  async function waitForRestart(before: UpdateInfo | null): Promise<void> {
    ctx.data.restarting = true;
    const start = Date.now();
    let busyAt = start;
    let last: UpdateInfo | null = null;
    let seen: UpdateInfo | null = null;
    try {
      for (;;) {
        await new Promise((r) => setTimeout(r, POLL_MS));
        last = await ctx.api.updateInfo().catch(() => null);
        if (last) {
          seen = last;
          await ctx.refreshStatus();
        }
        const t = Date.now();
        if (updateInProgress(last)) busyAt = t;
        const step = before ? updatePollStep(before, last, t - start, t - busyAt) : last ? "done" : "wait";
        if (step === "wait") continue;
        if (step === "timeout") {
          const secs = Math.round((t - start) / 1000);
          if (updateInProgress(seen)) {
            show(phase, `Aktualizace ani po ${secs} s neskončila (zařízení naposledy hlásilo, že stále probíhá). Výsledek zkontrolujte později na této stránce.`, "err");
          } else {
            show(phase, `Zařízení se ani po ${secs} s neohlásilo s novou verzí. Poslední výsledek: ${lastResultText(seen?.last_result ?? null)}.`, "err");
          }
          return;
        }
        if (last && before && last.current_version !== before.current_version) {
          show(phase, `Hotovo, běží verze ${last.current_version}. Výsledek: ${lastResultText(last.last_result)}.`, "ok");
        } else {
          show(phase, `Aktualizace se nepovedla: ${lastResultText(last?.last_result ?? null)}. Běží verze ${last?.current_version ?? "?"}.`, "err");
        }
        return;
      }
    } finally {
      ctx.data.restarting = false;
      ctx.ws.retry();
      void ctx.refreshStatus();
    }
  }

  const el = h(
    "div",
    { class: "screen" },
    h("section", { class: "card" }, h("h2", null, "Nainstalovaná verze"), info),
    h(
      "section",
      { class: "card" },
      h("h2", null, "Aktualizace firmwaru"),
      h("p", { class: "muted small" }, "Nahrajte balíček pi-velvetronix-<verze>-linux-arm64.tar.gz. Obraz systému (.img, .img.xz) ani .zip se sem nenahrává."),
      h("div", { class: "row" }, h("label", { htmlFor: file.id }, "Soubor"), file),
      fileMsg,
      btn,
      progress,
      phase,
    ),
  );

  function render(): void {
    const u = ctx.data.update;
    info.replaceChildren();
    const rows: [string, string][] = u
      ? [
          ["Verze", u.current_version],
          ["Předchozí verze", u.previous_version ?? "—"],
          ["Stav", STATE_LABEL[u.state] ?? u.state],
          ["Poslední výsledek", lastResultText(u.last_result)],
        ]
      : [["Stav", "načítám…"]];
    for (const [k, v] of rows) info.append(h("dt", null, k), h("dd", null, v));
    const other = !busy && u !== null && u.state !== "idle";
    setText(btn, other ? "Probíhá jiná aktualizace" : "Nahrát a nainstalovat");
    if (other) btn.disabled = true;
    else if (!busy) btn.disabled = !file.files?.[0] || !fileMsg.classList.contains("msg-ok");
  }

  return { el, render };
}
