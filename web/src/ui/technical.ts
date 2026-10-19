// Technical view: programs, amp and effects as plain form controls.

import type { AmpState } from "../protocol/client";
import { SLOT_PARAM_ID, SLOT_SUB, type EffectSlot, type Program } from "../protocol/program";
import {
  AMP_MODELS, AMP_PARAMS, BIAS_NAMES, CLASS_NAMES, PEDAL1_TYPES, PEDAL2_TYPES, REVERB_TYPES, USER_SLOT_NAMES,
  type EffectType,
} from "../protocol/tables";
import type { Ctx } from "./context";
import { clear, h, setText, toast } from "./dom";
import {
  BRIGHT_CAP, effectOn, effectType, effectValues, ID_AMP, ID_EFFECT_ON, ID_NR, ID_TYPE, type Bound, type Edits,
} from "./edits";
import { ampErrorText } from "./errors";
import { formatEffectValue, formatKnob, modeLabel } from "./format";
import { visibleEffectParams } from "./logic";
import { shownName, type NameKind } from "./names";
import { prefs } from "./prefs";

const SLOTS: { slot: EffectSlot; title: string; types: readonly EffectType[] }[] = [
  { slot: "pedal1", title: "Pedál 1", types: PEDAL1_TYPES },
  { slot: "pedal2", title: "Pedál 2", types: PEDAL2_TYPES },
  { slot: "reverb", title: "Dozvuk (reverb)", types: REVERB_TYPES },
];

export function createTechnical(ctx: Ctx, edits: Edits): { el: HTMLElement; render(s: AmpState): void } {
  const { touched, guarded } = edits;
  const bound: Bound[] = [];
  /** Selects whose option labels follow the names switch. */
  const named: { sel: HTMLSelectElement; kind: NameKind }[] = [];
  let namesShown = "";

  const send = (id: number, sub: number, v: number) => ctx.client.setParam(id, sub, v);

  function slider(key: string, label: string, min: number, max: number, fmt: (v: number) => string, get: (p: Program) => number, onSend: (v: number) => void) {
    const input = h("input", { type: "range", min, max, step: 1, id: `c-${key}` });
    const out = h("output", { htmlFor: input.id });
    const name = h("label", { htmlFor: input.id }, label);
    input.addEventListener("input", () => {
      touched(key);
      out.textContent = fmt(input.valueAsNumber);
      onSend(input.valueAsNumber);
    });
    bound.push({
      set(p) {
        if (guarded(key)) return;
        const v = get(p);
        if (input.valueAsNumber !== v) input.value = String(v);
        setText(out, fmt(v));
      },
    });
    return { row: h("div", { class: "row slider" }, name, input, out), name };
  }

  function toggle(key: string, label: string, get: (p: Program) => boolean, onSend: (on: boolean) => void) {
    const input = h("input", { type: "checkbox", id: `c-${key}` });
    input.addEventListener("change", () => {
      touched(key);
      onSend(input.checked);
    });
    bound.push({
      set(p) {
        if (!guarded(key)) input.checked = get(p);
      },
    });
    return { row: h("label", { class: "row toggle", htmlFor: input.id }, input, h("span", null, label)), input };
  }

  function choice(key: string, label: string, options: string[], get: (p: Program) => number, onSend: (v: number) => void) {
    const sel = h("select", { id: `c-${key}` }, ...options.map((o, i) => h("option", { value: String(i) }, o)));
    sel.addEventListener("change", () => {
      touched(key);
      onSend(Number(sel.value));
    });
    bound.push({
      set(p) {
        if (guarded(key)) return;
        const v = String(get(p));
        if (sel.value !== v) sel.value = v;
      },
    });
    return { row: h("div", { class: "row" }, h("label", { htmlFor: sel.id }, label), sel), sel };
  }

  // --- programs ---
  const modeText = h("p", { class: "mode" });
  const progNames = USER_SLOT_NAMES.map(() => h("span", { class: "prog-name" }));
  const progButtons = USER_SLOT_NAMES.map((slot, i) =>
    h(
      "button",
      { type: "button", class: "prog", onclick: () => ctx.client.select(i).catch((e) => toast(ampErrorText(e))) },
      h("span", { class: "prog-slot" }, slot),
      progNames[i],
    ),
  );
  const programs = h("section", { class: "card" }, h("h2", null, "Programy"), modeText, h("div", { class: "progs" }, ...progButtons));

  // --- amp ---
  const model = choice("model", "Model zesilovače", AMP_MODELS.map((m) => m.name), (p) => p.ampModel, (v) => send(ID_TYPE, 0, v));
  named.push({ sel: model.sel, kind: "amp" });
  const knobRows: HTMLElement[] = [];
  let toneName: HTMLElement | null = null;
  for (const spec of AMP_PARAMS.filter((a) => a.kind === "knob")) {
    const label = spec.name === "VOLUME" ? "VOLUME (hlasitost programu)" : spec.name;
    const s = slider(`amp${spec.index}`, label, spec.min, spec.max, formatKnob, (p) => p.amp[spec.index], (v) => send(ID_AMP, spec.index, v));
    if (spec.index === 5) toneName = s.name;
    knobRows.push(s.row);
  }
  const nr = slider("nr", "NOISE REDUCTION", 0, 100, formatKnob, (p) => p.nr, (v) => send(ID_NR, 0, v));
  const switches = AMP_PARAMS.filter((a) => a.kind === "switch").map((spec) => ({
    index: spec.index,
    ...toggle(`amp${spec.index}`, spec.name, (p) => p.amp[spec.index] !== 0, (on) => send(ID_AMP, spec.index, on ? 1 : 0)),
  }));
  const brightCap = switches.find((s) => s.index === BRIGHT_CAP)!;
  const brightNote = h("span", { class: "muted small", hidden: true }, " (u tohoto modelu není)");
  brightCap.row.append(brightNote);
  const bias = choice("amp10", "BIAS", [...BIAS_NAMES], (p) => p.amp[10], (v) => send(ID_AMP, 10, v));
  const cls = choice("amp11", "CLASS", [...CLASS_NAMES], (p) => p.amp[11], (v) => send(ID_AMP, 11, v));
  const amp = h(
    "section",
    { class: "card" },
    h("h2", null, "Zesilovač"),
    h("p", { class: "muted small" }, "Power Level je analogový a nelze ho ovládat na dálku."),
    model.row,
    ...knobRows,
    h("div", { class: "switches" }, ...switches.map((s) => s.row)),
    bias.row,
    cls.row,
    nr.row,
  );

  // --- effects ---
  const effectCards = SLOTS.map(({ slot, title, types }) => {
    const on = toggle(`${slot}On`, "zapnuto", (p) => effectOn(p, slot), (v) => send(ID_EFFECT_ON, SLOT_SUB[slot], v ? 1 : 0));
    const typeKey = `${slot}Type`;
    const typeSel = h("select", { id: `c-${typeKey}` }, ...types.map((t) => h("option", { value: String(t.id) }, t.name)));
    named.push({ sel: typeSel, kind: slot });
    typeSel.addEventListener("change", () => {
      touched(typeKey);
      ctx.client.setEffectType(slot, Number(typeSel.value)).catch((e) => {
        edits.forget(typeKey);
        toast(ampErrorText(e));
        ctx.changed();
      });
    });
    const params = h("div", { class: "params" });
    let shownType = -1;
    let paramBound: Bound[] = [];

    function buildParams(type: number): void {
      clear(params);
      paramBound = [];
      for (const { n, spec } of visibleEffectParams(slot, type)) {
        const key = `${slot}:${type}:${n}`;
        const sendV = (v: number) => send(SLOT_PARAM_ID[slot], n, v);
        const before = bound.length;
        if (spec.unit === "bool") params.append(toggle(key, spec.name, (p) => effectValues(p, slot)[n] !== 0, (b) => sendV(b ? 1 : 0)).row);
        else if (spec.unit === "voice") params.append(choice(key, spec.name, ["1", "2", "3"], (p) => effectValues(p, slot)[n], sendV).row);
        else params.append(slider(key, spec.name, spec.min, spec.max, (v) => formatEffectValue(spec, v), (p) => effectValues(p, slot)[n], sendV).row);
        // Param controls live only as long as this type is shown.
        paramBound.push(...bound.splice(before));
      }
    }

    const card = h(
      "section",
      { class: "card effect" },
      h("div", { class: "effect-head" }, h("h2", null, title), on.row),
      h("div", { class: "row effect-type" }, h("label", { htmlFor: typeSel.id }, "Typ"), typeSel),
      params,
    );
    return {
      card,
      set(p: Program) {
        const type = effectType(p, slot);
        if (!guarded(typeKey) && typeSel.value !== String(type)) typeSel.value = String(type);
        if (type !== shownType) {
          shownType = type;
          buildParams(type);
        }
        card.classList.toggle("effect-off", !effectOn(p, slot));
        for (const b of paramBound) b.set(p);
      },
    };
  });
  const effects = h("div", { class: "effects" }, ...effectCards.map((c) => c.card));

  const el = h("div", { class: "technical" }, programs, amp, effects);

  function renderNames(): void {
    if (namesShown === prefs.names) return;
    namesShown = prefs.names;
    for (const { sel, kind } of named) {
      Array.from(sel.options).forEach((opt, i) => {
        const n = shownName(kind, i, prefs.names);
        opt.textContent = n.text;
        opt.title = n.title;
      });
    }
  }

  function render(s: AmpState): void {
    renderNames();
    setText(modeText, `Režim: ${modeLabel(s.mode, s.program)}`);
    progButtons.forEach((b, i) => {
      const active = s.mode === "user" && s.program === i;
      const p = s.slots[i];
      setText(progNames[i], p ? p.name || "(bez názvu)" : "—");
      b.classList.toggle("active", active);
      b.setAttribute("aria-pressed", String(active));
    });

    const cur = s.current;
    // Never show control defaults that the amp did not report.
    amp.hidden = effects.hidden = !cur;
    if (!cur) return;
    const m = AMP_MODELS[cur.ampModel];
    if (toneName) setText(toneName, m?.toneLabel ?? "PRESENCE");
    const brightOk = m?.brightCap ?? true;
    brightCap.input.disabled = !brightOk;
    brightNote.hidden = brightOk;
    for (const b of bound) b.set(cur);
    for (const c of effectCards) c.set(cur);
    for (const { sel, kind } of named) sel.title = shownName(kind, Number(sel.value), prefs.names).title;
  }

  return { el, render };
}
