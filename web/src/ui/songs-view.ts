// "Písničky": song presets, tried live on the amp (Current Buffer only, never saved automatically).

import type { AuditionResult } from "../protocol/client";
import { gateOf, type Ctx } from "./context";
import { h, setText } from "./dom";
import { ampErrorText } from "./errors";
import { filterSongs, presetName } from "./logic";
import { shownName, type NameKind } from "./names";
import { namesSwitch } from "./names-switch";
import { prefs } from "./prefs";
import { GENRES, SONGS, type Genre, type SongPreset } from "./songs";

const GENRE_LABEL: Record<Genre, string> = {
  punk: "punk", "pop-punk": "pop-punk", "skate-punk": "skate-punk", hardcore: "hardcore",
  "ska-punk": "ska-punk", "czech-punk": "český punk", rock: "rock",
};

type Outcome = { song: SongPreset; result: AuditionResult } | { song: SongPreset; error: string };

/** "~Boss DS-1, SPRING" for the effects that are on. */
function effectsLine(s: SongPreset): { text: string; title: string } {
  const on = (["pedal1", "pedal2", "reverb"] as const).filter((k) => s[k].on);
  if (on.length === 0) return { text: "bez efektů", title: "" };
  const names = on.map((k) => shownName(k as NameKind, s[k].type, prefs.names));
  return { text: names.map((n) => n.text).join(", "), title: names.map((n) => n.title).join("\n") };
}

export function createSongs(ctx: Ctx): { el: HTMLElement; render(): void } {
  let query = "";
  let genre: Genre | null = null;
  let running: { song: SongPreset; done: number; total: number } | null = null;
  let outcome: Outcome | null = null;

  const names = namesSwitch(ctx);
  const search = h("input", {
    type: "search", class: "song-search", placeholder: "Hledat písničku nebo kapelu", "aria-label": "Hledat písničku nebo kapelu",
    autocomplete: "off", spellcheck: false,
  });
  search.addEventListener("input", () => {
    query = search.value;
    renderList();
  });
  const chips = [null, ...GENRES].map((g) =>
    h("button", { type: "button", class: "chip-btn", onclick: () => { genre = g; renderList(); } }, g === null ? "vše" : GENRE_LABEL[g]),
  );
  const count = h("p", { class: "muted small song-count", "aria-live": "polite" });
  const gateNote = h("p", { class: "msg msg-warn", hidden: true });

  const head = h(
    "section",
    { class: "card songs-head" },
    h("div", { class: "songs-title" }, h("h2", null, "Písničky"), names.el),
    h("p", { class: "muted" }, "Tlačítko Zahrát nastaví zvuk v kombu živě. Hlasitost programu (VOLUME) aplikace neposílá a nic se neukládá."),
    search,
    h("div", { class: "chips-row", role: "group", "aria-label": "Žánr" }, ...chips),
    count,
    gateNote,
  );

  // Audition progress and result, kept in view at the bottom of the screen.
  const dockText = h("p", null);
  const dockBar = h("progress", { max: 1, value: 0 });
  const dockSave = h("button", { type: "button", class: "primary" }, "Uložit do komba…");
  const dockClose = h("button", { type: "button", class: "dock-close", "aria-label": "Zavřít zprávu" }, "×");
  const dock = h("div", { class: "song-dock", role: "status", "aria-live": "polite", hidden: true }, dockText, dockBar, h("div", { class: "dock-actions" }, dockSave, dockClose));
  dockSave.addEventListener("click", () => {
    if (!outcome) return;
    ctx.data.saveDraft = presetName(outcome.song.title);
    outcome = null;
    location.hash = "#ovladani";
  });
  dockClose.addEventListener("click", () => {
    outcome = null;
    renderDock();
  });

  const list = h("div", { class: "song-list" });
  const empty = h("p", { class: "muted", hidden: true }, "Nic nenalezeno. Zkuste jiné slovo nebo žánr „vše“.");
  const el = h("div", { class: "screen" }, head, list, empty, dock);

  const cards = SONGS.map((s) => {
    const amp = h("span", { class: "song-amp-name" });
    const fx = h("span", { class: "song-fx-name" });
    const play = h("button", { type: "button", class: "primary play" }, "Zahrát");
    play.addEventListener("click", () => void audition(s));
    const meta = [s.artist, s.year === null ? null : String(s.year)].filter(Boolean).join(", ");
    const source = s.source
      ? h("a", { href: s.source, target: "_blank", rel: "noopener noreferrer", class: "small" }, "Zdroj vybavení")
      : h("span", { class: "small muted" }, "Nastaveno podle stylu kapely");
    const card = h(
      "article",
      { class: "card song", "data-id": s.id },
      h("div", { class: "song-head" }, h("div", null, h("h3", null, s.title), h("p", { class: "song-meta" }, meta)), h("span", { class: "tag" }, GENRE_LABEL[s.genre])),
      h("p", { class: "song-desc" }, s.description),
      h("dl", { class: "song-gear" }, h("dt", null, "Zesilovač"), h("dd", null, amp), h("dt", null, "Efekty"), h("dd", null, fx)),
      h("div", { class: "song-foot" }, source, play),
    );
    return { s, card, amp, fx, play };
  });
  list.append(...cards.map((c) => c.card));

  async function audition(song: SongPreset): Promise<void> {
    if (running || !gateOf(ctx).enabled) return;
    running = { song, done: 0, total: 0 };
    outcome = null;
    render();
    try {
      const result = await ctx.client.audition(song, (done, total) => {
        if (!running) return;
        running.done = done;
        running.total = total;
        renderDock();
      });
      outcome = { song, result };
    } catch (e) {
      outcome = { song, error: ampErrorText(e) };
    } finally {
      running = null;
      render();
    }
  }

  function renderDock(): void {
    dock.hidden = !running && !outcome;
    dockBar.hidden = !running;
    dockSave.hidden = true;
    dockClose.hidden = !!running;
    dock.className = "song-dock";
    if (running) {
      setText(dockText, `Nastavuji „${running.song.title}“…`);
      dockBar.max = Math.max(1, running.total);
      dockBar.value = running.done;
      return;
    }
    if (!outcome) return;
    if ("error" in outcome) {
      dock.classList.add("dock-err");
      setText(dockText, `„${outcome.song.title}“ se nenastavil: ${outcome.error}`);
      return;
    }
    const r = outcome.result;
    if (r.aborted) {
      dock.classList.add("dock-err");
      const at = r.failed.at(-1)?.step;
      setText(dockText, `Nastavení „${outcome.song.title}“ se přerušilo po ${r.done} z ${r.total} krocích${at ? ` (u kroku ${at})` : ""}: ${ampErrorText(r.aborted)} Zvuk v kombu je teď jen zčásti změněný.`);
      return;
    }
    dockSave.hidden = false;
    const volume = r.volume ? ` Hlasitost programu (VOLUME) se v kombu změnila z ${r.volume.from} na ${r.volume.to}, aplikace ji neposílala.` : "";
    const save = " Do paměti komba ho uložíte tlačítkem Uložit do komba.";
    if (r.failed.length || r.volume) {
      dock.classList.add("dock-warn");
      const failed = r.failed.length ? ` Kombo nepotvrdilo: ${r.failed.map((f) => f.step).join(", ")}.` : "";
      setText(dockText, `Zvuk je nastavený živě.${failed}${volume}${save}`);
    } else {
      dock.classList.add("dock-ok");
      setText(dockText, `Zvuk je nastavený živě.${save}`);
    }
  }

  let namesShown = "";
  function renderList(): void {
    chips.forEach((c, i) => {
      const on = (i === 0 ? null : GENRES[i - 1]) === genre;
      c.classList.toggle("on", on);
      c.setAttribute("aria-pressed", String(on));
    });
    const shown = new Set(filterSongs(SONGS, query, genre));
    for (const c of cards) c.card.hidden = !shown.has(c.s);
    empty.hidden = shown.size > 0;
    setText(count, shown.size === SONGS.length ? `${SONGS.length} písniček` : `${shown.size} z ${SONGS.length} písniček`);
  }

  function render(): void {
    names.render();
    const gate = gateOf(ctx);
    gateNote.hidden = gate.enabled;
    if (!gate.enabled) setText(gateNote, `Zahrát půjde, až bude kombo připojené. ${gate.reason}`);
    if (namesShown !== prefs.names) {
      namesShown = prefs.names;
      for (const c of cards) {
        const a = shownName("amp", c.s.amp.model, prefs.names);
        setText(c.amp, a.text);
        c.amp.title = a.title;
        c.amp.classList.toggle("modest", a.modest);
        c.amp.classList.toggle("unofficial", a.unofficial);
        const f = effectsLine(c.s);
        setText(c.fx, f.text);
        c.fx.title = f.title;
      }
    }
    for (const c of cards) {
      c.play.disabled = !gate.enabled || running !== null;
      setText(c.play, running?.song === c.s ? "Nastavuji…" : "Zahrát");
    }
    renderDock();
  }

  renderList();
  return { el, render };
}
