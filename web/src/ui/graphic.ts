// Graphical view: the VT100X top panel (bank/channel buttons, amp selector, knobs, valve section)
// and the three effects as stompboxes. Same client calls as the technical view.

import type { AmpState } from "../protocol/client";
import { SLOT_PARAM_ID, SLOT_SUB, type EffectSlot, type Program } from "../protocol/program";
import { AMP_MODELS, AMP_PARAMS, BIAS_NAMES, CLASS_NAMES, PEDAL1_TYPES, PEDAL2_TYPES, REVERB_TYPES, type EffectType } from "../protocol/tables";
import type { Ctx } from "./context";
import { clear, h, setText, toast } from "./dom";
import {
  BRIGHT_CAP, effectOn, effectType, effectValues, ID_AMP, ID_EFFECT_ON, ID_NR, ID_TYPE, type Bound, type Edits,
} from "./edits";
import { ampErrorText } from "./errors";
import { formatEffectValue, formatKnob, modeLabel } from "./format";
import { createKnob, type Knob } from "./knob";
import { visibleEffectParams } from "./logic";
import { shownName, type NameKind } from "./names";
import { prefs } from "./prefs";

const SLOTS: { slot: EffectSlot; title: string; types: readonly EffectType[] }[] = [
  { slot: "pedal1", title: "Pedál 1", types: PEDAL1_TYPES },
  { slot: "pedal2", title: "Pedál 2", types: PEDAL2_TYPES },
  { slot: "reverb", title: "Reverb", types: REVERB_TYPES },
];

/**
 * Enclosure colour per effect type, loosely after the pedal each type is modelled on. Each colour is
 * tuned so its print (light or dark, see inkFor) reaches at least 4.5:1 under the gloss in styles.css.
 */
const ENCLOSURE: Record<EffectSlot, readonly string[]> = {
  pedal1: ["#28547e", "#4596b7", "#5e9a75", "#b8923a", "#a9adb2", "#d8d2c2", "#e0782a", "#34322f", "#6c492d", "#8a2f3b"],
  pedal2: ["#49515a", "#262422", "#e07a1f", "#cc783a", "#5b437e", "#938f68", "#c97a93"],
  reverb: ["#47525c", "#33574e", "#4c4664", "#918e87"],
};

/** WCAG relative luminance of "#rrggbb". */
function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** Light enclosures (luminance above 0.17) get dark print. */
const darkInk = (hex: string) => luminance(hex) > 0.17;

type View = { el: HTMLElement; render(s: AmpState, enabled: boolean): void };

export function createGraphic(ctx: Ctx, edits: Edits): View {
  const { touched, guarded } = edits;
  const bound: Bound[] = [];
  const knobs: Knob[] = [];
  const send = (id: number, sub: number, v: number) => ctx.client.setParam(id, sub, v);
  /** Last `enabled` from render(); knobs built later (effect type change) start in this state. */
  let enabledNow = false;

  function knob(key: string, label: string, min: number, max: number, size: "lg" | "md" | "sm", fmt: (v: number) => string, get: (p: Program) => number, onSend: (v: number) => void, list = knobs, into = bound) {
    const k = createKnob({
      label, min, max, size, format: fmt,
      onInput: (v) => {
        touched(key);
        onSend(v);
      },
    });
    k.setDisabled(!enabledNow);
    list.push(k);
    into.push({ set: (p) => { if (!guarded(key)) k.set(get(p)); } });
    return k;
  }

  /** A latching panel button with an LED (BRIGHT CAP, LOW CUT, ...). */
  function latch(key: string, label: string, get: (p: Program) => boolean, onSend: (on: boolean) => void, into = bound) {
    const b = h("button", { type: "button", class: "latch" }, h("span", { class: "led", "aria-hidden": "true" }), h("span", { class: "latch-label" }, label));
    let on = false;
    const show = (v: boolean) => {
      on = v;
      b.classList.toggle("on", v);
      b.setAttribute("aria-pressed", String(v));
    };
    b.addEventListener("click", () => {
      touched(key);
      show(!on);
      onSend(on);
    });
    into.push({ set: (p) => { if (!guarded(key)) show(get(p)); } });
    return b;
  }

  /** Multi-position switch (BIAS, CLASS, COMP VOICE). */
  function selector(key: string, label: string, options: readonly string[], get: (p: Program) => number, onSend: (v: number) => void, into = bound) {
    const buttons = options.map((o, i) => h("button", { type: "button", class: "pos" }, o));
    let cur = -1;
    const show = (v: number) => {
      cur = v;
      buttons.forEach((b, i) => {
        b.classList.toggle("on", i === v);
        b.setAttribute("aria-pressed", String(i === v));
      });
    };
    buttons.forEach((b, i) =>
      b.addEventListener("click", () => {
        if (i === cur) return;
        touched(key);
        show(i);
        onSend(i);
      }),
    );
    into.push({ set: (p) => { if (!guarded(key)) show(get(p)); } });
    return h("div", { class: "selector-sw", role: "group", "aria-label": label }, h("span", { class: "sw-label" }, label), h("span", { class: "sw-pos" }, ...buttons));
  }

  // --- programs: BANK flips the shown bank (no message), CH1-CH4 select bank*4 + ch ---
  let bankOverride: number | null = null;
  let lastActive: number | null | undefined;
  let shownBank = 0;
  const bankName = h("span", { class: "bank-name" }, "A");
  const bankBtn = h(
    "button",
    { type: "button", class: "bank", "aria-label": "Přepnout banku" },
    h("span", { class: "led", "aria-hidden": "true" }),
    h("span", { class: "btn-cap" }, "BANK"),
    bankName,
  );
  bankBtn.addEventListener("click", () => {
    bankOverride = shownBank === 0 ? 1 : 0;
    ctx.changed();
  });
  const chNames = [0, 1, 2, 3].map(() => h("span", { class: "ch-name" }));
  const chButtons = [0, 1, 2, 3].map((i) =>
    h(
      "button",
      {
        type: "button", class: "ch",
        onclick: () => ctx.client.select(shownBank * 4 + i).catch((e) => toast(ampErrorText(e))),
      },
      h("span", { class: "led", "aria-hidden": "true" }),
      h("span", { class: "btn-cap" }, `CH${i + 1}`),
      chNames[i],
    ),
  );
  const modeNote = h("p", { class: "panel-mode" });
  const programs = h("div", { class: "programs", role: "group", "aria-label": "Programy" }, bankBtn, ...chButtons);

  // --- amp selector ---
  const modelName = h("span", { class: "amp-window-name" });
  const modelCode = h("span", { class: "amp-window-code" });
  const modelSel = h("select", { class: "overlay-select", "aria-label": "Model zesilovače" }, ...AMP_MODELS.map((m) => h("option", { value: String(m.id) }, m.name)));
  const modelWindow = h("label", { class: "amp-window" }, modelName, modelCode, h("span", { class: "window-caret", "aria-hidden": "true" }), modelSel);
  const showModel = (id: number) => {
    const main = shownName("amp", id, prefs.names);
    const other = shownName("amp", id, prefs.names === "original" ? "code" : "original");
    setText(modelName, main.text);
    setText(modelCode, other.text === main.text ? "" : other.text);
    modelName.classList.toggle("modest", main.modest);
    modelName.classList.toggle("unofficial", main.unofficial);
    modelCode.classList.toggle("modest", other.modest);
    modelCode.classList.toggle("unofficial", other.unofficial);
    modelWindow.title = main.title;
    if (modelSel.value !== String(id)) modelSel.value = String(id);
  };
  const modelKnob = createKnob({
    label: "AMP", min: 0, max: AMP_MODELS.length - 1, step: 1, size: "lg", ticks: AMP_MODELS.length,
    format: (v) => shownName("amp", v, prefs.names).text,
    onInput: (v) => {
      touched("model");
      showModel(v);
      send(ID_TYPE, 0, v);
    },
  });
  modelKnob.el.classList.add("knob-selector");
  knobs.push(modelKnob);
  modelSel.addEventListener("change", () => {
    const v = Number(modelSel.value);
    touched("model");
    modelKnob.set(v);
    showModel(v);
    send(ID_TYPE, 0, v);
  });
  bound.push({
    set(p) {
      if (guarded("model")) return;
      modelKnob.set(p.ampModel);
      showModel(p.ampModel);
    },
  });

  // --- amp knobs ---
  const ampKnobs = AMP_PARAMS.filter((a) => a.kind === "knob").map((spec) => ({
    index: spec.index,
    k: knob(`amp${spec.index}`, spec.name, spec.min, spec.max, "md", formatKnob, (p) => p.amp[spec.index], (v) => send(ID_AMP, spec.index, v)),
  }));
  const toneKnob = ampKnobs.find((a) => a.index === 5)!.k;
  const nrKnob = knob("nr", "NR", 0, 100, "md", formatKnob, (p) => p.nr, (v) => send(ID_NR, 0, v));
  nrKnob.el.title = "Noise reduction";

  // --- valve section ---
  const latches = AMP_PARAMS.filter((a) => a.kind === "switch").map((spec) => ({
    index: spec.index,
    el: latch(`amp${spec.index}`, spec.name, (p) => p.amp[spec.index] !== 0, (on) => send(ID_AMP, spec.index, on ? 1 : 0)),
  }));
  const brightLatch = latches.find((l) => l.index === BRIGHT_CAP)!.el;
  const bias = selector("amp10", "BIAS", BIAS_NAMES, (p) => p.amp[10], (v) => send(ID_AMP, 10, v));
  const cls = selector("amp11", "CLASS", CLASS_NAMES, (p) => p.amp[11], (v) => send(ID_AMP, 11, v));

  const panel = h(
    "section",
    { class: "amp-panel", "aria-label": "Panel zesilovače" },
    h(
      "div",
      { class: "panel-head" },
      h("div", { class: "logo", "aria-hidden": "true" }, h("span", { class: "logo-vox" }, "VOX"), h("span", { class: "logo-sub" }, "Valvetronix VT100X")),
      programs,
    ),
    modeNote,
    h(
      "div",
      { class: "panel-body" },
      h("div", { class: "amp-select" }, modelKnob.el, modelWindow),
      h("div", { class: "knobrow" }, ...ampKnobs.map((a) => a.k.el), nrKnob.el),
    ),
    h(
      "div",
      { class: "valve" },
      h("div", { class: "latches" }, ...latches.map((l) => l.el)),
      bias,
      cls,
    ),
  );

  // --- stompboxes ---
  const stomps = SLOTS.map(({ slot, title, types }) => {
    const typeKey = `${slot}Type`;
    const onKey = `${slot}On`;
    const led = h("span", { class: "stomp-led", "aria-hidden": "true" });
    const name = h("span", { class: "stomp-name" });
    const code = h("span", { class: "stomp-code" });
    const typeSel = h("select", { class: "overlay-select", "aria-label": `${title}: typ efektu` }, ...types.map((t) => h("option", { value: String(t.id) }, t.name)));
    const typeLabel = h("label", { class: "stomp-type" }, name, code, h("span", { class: "window-caret", "aria-hidden": "true" }), typeSel);
    const params = h("div", { class: "stomp-knobs" });
    const fsLabel = h("span", { class: "fs-label" });
    const footswitch = h("button", { type: "button", class: "footswitch" }, h("span", { class: "fs-cap", "aria-hidden": "true" }), fsLabel);
    const box = h(
      "section",
      { class: "stomp", "aria-label": title },
      h("div", { class: "stomp-top" }, led, h("span", { class: "stomp-slot" }, title)),
      typeLabel,
      params,
      footswitch,
    );

    let isOn = false;
    const showOn = (v: boolean) => {
      isOn = v;
      box.classList.toggle("stomp-on", v);
      footswitch.setAttribute("aria-pressed", String(v));
      setText(fsLabel, v ? "Zapnuto" : "Vypnuto");
      footswitch.setAttribute("aria-label", `${title}: ${v ? "zapnuto" : "vypnuto"}`);
    };
    footswitch.addEventListener("click", () => {
      touched(onKey);
      showOn(!isOn);
      send(ID_EFFECT_ON, SLOT_SUB[slot], isOn ? 1 : 0);
    });

    const showType = (t: number) => {
      const kind: NameKind = slot;
      const main = shownName(kind, t, prefs.names);
      const other = shownName(kind, t, prefs.names === "original" ? "code" : "original");
      setText(name, main.text);
      setText(code, other.text === main.text ? "" : other.text);
      name.classList.toggle("modest", main.modest);
      name.classList.toggle("unofficial", main.unofficial);
      code.classList.toggle("modest", other.modest);
      code.classList.toggle("unofficial", other.unofficial);
      typeLabel.title = main.title;
      const color = ENCLOSURE[slot][t] ?? "#4a4743";
      box.style.setProperty("--enc", color);
      box.classList.toggle("ink-dark", darkInk(color));
      if (typeSel.value !== String(t)) typeSel.value = String(t);
    };
    typeSel.addEventListener("change", () => {
      const t = Number(typeSel.value);
      touched(typeKey);
      showType(t);
      ctx.client.setEffectType(slot, t).catch((e) => {
        edits.forget(typeKey);
        toast(ampErrorText(e));
        ctx.changed();
      });
    });

    let shownType = -1;
    let shownNames = "";
    let paramBound: Bound[] = [];
    let paramKnobs: Knob[] = [];

    function buildParams(type: number): void {
      clear(params);
      paramBound = [];
      paramKnobs = [];
      for (const { n, spec } of visibleEffectParams(slot, type)) {
        const key = `${slot}:${type}:${n}`;
        const get = (p: Program) => effectValues(p, slot)[n];
        const sendV = (v: number) => send(SLOT_PARAM_ID[slot], n, v);
        if (spec.unit === "bool") params.append(latch(key, spec.name, (p) => get(p) !== 0, (b) => sendV(b ? 1 : 0), paramBound));
        else if (spec.unit === "voice") params.append(selector(key, spec.name, ["1", "2", "3"], get, sendV, paramBound));
        else params.append(knob(key, spec.name, spec.min, spec.max, "sm", (v) => formatEffectValue(spec, v), get, sendV, paramKnobs, paramBound).el);
      }
    }

    return {
      box,
      knobs: () => paramKnobs,
      set(p: Program) {
        const type = effectType(p, slot);
        const namesChanged = shownNames !== prefs.names;
        if (namesChanged) {
          shownNames = prefs.names;
          Array.from(typeSel.options).forEach((o, i) => (o.textContent = shownName(slot, i, prefs.names).text));
        }
        if (!guarded(typeKey) && (type !== Number(typeSel.value) || namesChanged || shownType === -1)) showType(type);
        if (type !== shownType) {
          shownType = type;
          buildParams(type);
        }
        if (!guarded(onKey)) showOn(effectOn(p, slot));
        for (const b of paramBound) b.set(p);
      },
    };
  });
  const board = h("div", { class: "pedalboard" }, ...stomps.map((s) => s.box));

  const el = h("div", { class: "rig" }, panel, board);
  let namesShown = "";

  function render(s: AmpState, enabled: boolean): void {
    enabledNow = enabled;
    // Programs
    const active = s.mode === "user" ? s.program : null;
    if (active !== lastActive) {
      lastActive = active;
      bankOverride = null;
    }
    shownBank = bankOverride ?? (active !== null && active >= 4 ? 1 : 0);
    setText(bankName, shownBank === 0 ? "A" : "B");
    bankBtn.classList.toggle("bank-b", shownBank === 1);
    bankBtn.classList.toggle("lit", active !== null);
    panel.classList.toggle("bank-b", shownBank === 1);
    chButtons.forEach((b, i) => {
      const slot = shownBank * 4 + i;
      const on = active === slot;
      const p = s.slots[slot];
      setText(chNames[i], p ? p.name || "(bez názvu)" : "—");
      b.classList.toggle("on", on);
      b.setAttribute("aria-pressed", String(on));
      b.setAttribute("aria-label", `${shownBank === 0 ? "A" : "B"}${i + 1}${p ? `: ${p.name || "bez názvu"}` : ""}`);
    });
    setText(modeNote, s.mode === "user" ? "" : `Režim: ${modeLabel(s.mode, s.program)}`);
    modeNote.hidden = s.mode === "user" || s.mode === null;

    for (const k of [...knobs, ...stomps.flatMap((st) => st.knobs())]) k.setDisabled(!enabled);

    const cur = s.current;
    // Never show control defaults that the amp did not report.
    for (const part of [panel.querySelector(".panel-body"), panel.querySelector(".valve"), board]) (part as HTMLElement).hidden = !cur;
    if (!cur) return;
    const m = AMP_MODELS[cur.ampModel];
    toneKnob.setLabel(m?.toneLabel ?? "PRESENCE");
    const brightOk = m?.brightCap ?? true;
    brightLatch.disabled = !brightOk;
    brightLatch.title = brightOk ? "" : "Tento model BRIGHT CAP nemá";
    if (namesShown !== prefs.names) {
      namesShown = prefs.names;
      Array.from(modelSel.options).forEach((o, i) => (o.textContent = shownName("amp", i, prefs.names).text));
      modelKnob.refresh();
      if (!guarded("model")) showModel(cur.ampModel);
    }
    for (const b of bound) b.set(cur);
    for (const st of stomps) st.set(cur);
  }

  return { el, render };
}
