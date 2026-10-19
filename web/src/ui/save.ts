// Save the Current Buffer into a user slot (explicit confirm dialog, design §9.2).

import type { AmpState } from "../protocol/client";
import { USER_SLOT_NAMES } from "../protocol/tables";
import type { Ctx } from "./context";
import { h } from "./dom";
import { ampErrorText } from "./errors";
import { slotName } from "./format";
import { PROGRAM_NAME_MAX, validateProgramName } from "./logic";

export interface SaveCard {
  el: HTMLElement;
  render(s: AmpState): void;
  /** Puts a name into the field (kept until saved), scrolls to the card and focuses the name. */
  prefill(name: string): void;
}

export function createSave(ctx: Ctx): SaveCard {
  const saveSlot = h("select", { id: "save-slot" }, ...USER_SLOT_NAMES.map((n, i) => h("option", { value: String(i) }, n)));
  const saveName = h("input", { type: "text", id: "save-name", maxLength: PROGRAM_NAME_MAX, autocomplete: "off", spellcheck: false });
  const nameErr = h("p", { class: "msg msg-err", hidden: true });
  const saveBtn = h("button", { type: "button", class: "primary" }, "Uložit do slotu…");
  const saveResult = h("p", { class: "msg", hidden: true });
  let nameDirty = false;
  let slotDirty = false;

  function checkName(): void {
    const err = validateProgramName(saveName.value);
    nameErr.textContent = err ?? "";
    nameErr.hidden = !err;
  }
  saveName.addEventListener("input", () => {
    nameDirty = true;
    checkName();
  });
  saveSlot.addEventListener("change", () => (slotDirty = true));

  const dlgText = h("p", null);
  const dlg = h(
    "dialog",
    { class: "dialog" },
    h(
      "form",
      { method: "dialog" },
      h("h2", null, "Uložit do paměti komba?"),
      dlgText,
      h("div", { class: "actions" }, h("button", { value: "cancel" }, "Zrušit"), h("button", { value: "ok", class: "primary" }, "Uložit")),
    ),
  );
  let pending: { slot: number; name: string } | null = null;

  function showResult(text: string, kind: "ok" | "warn" | "err"): void {
    saveResult.textContent = text;
    saveResult.className = `msg msg-${kind}`;
    saveResult.hidden = false;
  }

  saveBtn.addEventListener("click", () => {
    const err = validateProgramName(saveName.value);
    if (err) {
      nameErr.textContent = err;
      nameErr.hidden = false;
      return;
    }
    const slot = Number(saveSlot.value);
    const name = saveName.value;
    const old = ctx.data.vox.slots[slot]?.name;
    pending = { slot, name };
    dlgText.textContent =
      `Aktuální nastavení se uloží do slotu ${slotName(slot)} pod názvem „${name.trimEnd() || "(bez názvu)"}“. ` +
      (old !== undefined ? `Program „${old || "(bez názvu)"}“ v tomto slotu se přepíše. ` : "") +
      `Slot ${slotName(slot)} se poté stane aktivním programem.`;
    dlg.returnValue = "";
    dlg.showModal();
  });

  dlg.addEventListener("close", async () => {
    const job = pending;
    pending = null;
    if (dlg.returnValue !== "ok" || !job) return;
    const cur = ctx.data.vox.current;
    if (!cur) return showResult("Aktuální program není načtený.", "err");
    saveBtn.disabled = true;
    showResult("Ukládám…", "warn");
    try {
      const r = await ctx.client.save(job.slot, { ...cur, name: job.name });
      const inactive = r.endsWith("-inactive");
      const check = inactive ? r.slice(0, -"-inactive".length) : r;
      const where = slotName(job.slot);
      const note = inactive ? " Slot se nepodařilo aktivovat." : "";
      if (check === "verified" && !inactive) showResult(`Uloženo do ${where} a ověřeno.`, "ok");
      else if (check === "verified") showResult(`Uloženo do ${where} a ověřeno, ale slot se nepodařilo aktivovat.`, "warn");
      else if (check === "mismatch") showResult(`Uloženo do ${where}, ale kontrola nesouhlasí. Zkontrolujte program na kombu.${note}`, "warn");
      else showResult(`Uloženo do ${where}, ale ověření se nepodařilo.${note}`, "warn");
      nameDirty = false;
      slotDirty = false;
    } catch (e) {
      showResult(`Uložení selhalo: ${ampErrorText(e)}`, "err");
    } finally {
      saveBtn.disabled = false;
    }
  });

  const el = h(
    "section",
    { class: "card save", id: "ulozit" },
    h("h2", null, "Uložit"),
    h("p", { class: "muted small" }, "Změny se projeví hned, ale do paměti komba se uloží jen tlačítkem Uložit."),
    h("div", { class: "row" }, h("label", { htmlFor: saveSlot.id }, "Slot"), saveSlot),
    h("div", { class: "row" }, h("label", { htmlFor: saveName.id }, "Název (max. 16 znaků)"), saveName),
    nameErr,
    saveBtn,
    saveResult,
    dlg,
  );

  function render(s: AmpState): void {
    const cur = s.current;
    el.hidden = !cur;
    if (!cur) return;
    if (!nameDirty) saveName.value = cur.name;
    if (!slotDirty && s.mode === "user" && s.program !== null) saveSlot.value = String(s.program);
  }

  function prefill(name: string): void {
    saveName.value = name;
    nameDirty = true;
    saveResult.hidden = true;
    checkName();
    requestAnimationFrame(() => {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
      saveName.focus({ preventScroll: true });
    });
  }

  return { el, render, prefill };
}
