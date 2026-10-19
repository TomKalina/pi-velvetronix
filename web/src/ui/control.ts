// Main control screen: view switches, gate banner, graphical or technical controls, save.

import { gateOf, type Ctx } from "./context";
import { h, setText } from "./dom";
import { createEdits } from "./edits";
import { createGraphic } from "./graphic";
import { namesSwitch } from "./names-switch";
import { prefs, setPref, type DisplayMode } from "./prefs";
import { createSave } from "./save";
import { segmented } from "./segmented";
import { createTechnical } from "./technical";

export function createControl(ctx: Ctx): { el: HTMLElement; render(): void } {
  const edits = createEdits();

  const display = segmented<DisplayMode>("Zobrazení", [["graphic", "Grafický"], ["technical", "Technický"]], () => prefs.display, (v) => {
    setPref("display", v);
    ctx.changed();
  });
  const names = namesSwitch(ctx);
  const toolbar = h("div", { class: "toolbar" }, display.el, names.el);

  // --- gate banner ---
  const gateText = h("p", null);
  const retryRead = h("button", { type: "button", onclick: () => ctx.sync.retry() }, "Načíst znovu");
  const retryWs = h("button", { type: "button", onclick: () => ctx.ws.retry() }, "Zkusit znovu");
  const banner = h("div", { class: "banner", role: "status" }, gateText, retryRead, retryWs);

  const technical = createTechnical(ctx, edits);
  const graphic = createGraphic(ctx, edits);
  const save = createSave(ctx);

  const fieldset = h("fieldset", { class: "controls" }, graphic.el, technical.el, save.el);
  const el = h("div", { class: "screen screen-control" }, toolbar, banner, fieldset);

  function render(): void {
    const gate = gateOf(ctx);
    // A song audition (Písničky) is running: nothing here may interleave with its messages.
    const busy = ctx.client.busy;
    const enabled = gate.enabled && !busy;
    const s = ctx.data.vox;
    display.render();
    names.render();
    banner.hidden = enabled;
    if (!gate.enabled) {
      setText(gateText, gate.reason);
      retryRead.hidden = !gate.canRetryRead;
      retryWs.hidden = !gate.canRetryWs;
      setText(retryWs, ctx.data.ws.kind === "replaced" ? "Převzít zpět" : "Zkusit znovu");
    } else if (busy) {
      setText(gateText, "Právě se nastavuje zvuk z Písniček. Ovládání bude zase dostupné, až to skončí.");
      retryRead.hidden = retryWs.hidden = true;
    }
    fieldset.disabled = !enabled;

    const graphical = prefs.display === "graphic";
    graphic.el.hidden = !graphical;
    technical.el.hidden = graphical;
    // Only the visible view is kept in sync; the other catches up when shown.
    if (graphical) graphic.render(s, enabled);
    else technical.render(s);
    save.render(s);

    const draft = ctx.data.saveDraft;
    if (draft !== null && s.current) {
      ctx.data.saveDraft = null;
      save.prefill(draft);
    }
  }

  return { el, render };
}
