// Dev only: the real UI wired to a simulated amp (fake-amp.ts). Built by `npm run preview` into
// dist/preview/index.html; never part of the production bundle.
//
// Query parameters (all optional):
//   display=graphic|technical   names=original|code   scheme=dark|light (forces the colour scheme)
//   amp=connected|disconnected  (amp USB state)       play=<song id> (auto-plays a song on #pisnicky)
//   save=1 (with play: then opens the save form)   scroll=<css selector>       (scrolls that element to the top once the amp state is read)
//   fx=<pedal1>,<pedal2>,<reverb> (effect type ids of the current program, all on, default params)

import { Api } from "../src/api";
import { runApp } from "../src/app";
import { prefs, setPref } from "../src/ui/prefs";
import { WsTransport } from "../src/ws";
import { effectDefaults, fakeFetch, FakeSocket } from "./fake-amp";

export const PREVIEW_MARKER = "pi-velvetronix-preview";

const q = new URLSearchParams(location.search);
const display = q.get("display");
if (display === "graphic" || display === "technical") setPref("display", display);
const names = q.get("names");
if (names === "original" || names === "code") setPref("names", names);
const ampState = q.get("amp") === "disconnected" ? "disconnected" : "connected";

// Forced colour scheme: rewrite the prefers-color-scheme media rules of the inlined stylesheet.
const scheme = q.get("scheme");
if (scheme === "dark" || scheme === "light") {
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (rule instanceof CSSMediaRule && rule.conditionText.includes("prefers-color-scheme: dark")) {
        rule.media.mediaText = scheme === "dark" ? "all" : "not all";
      }
    }
  }
  document.documentElement.style.colorScheme = scheme;
}

let socket: FakeSocket | null = null;
const ws = new WsTransport({
  url: () => "ws://preview/ws",
  createSocket: () => {
    socket = new FakeSocket(ampState);
    const fx = q.get("fx")?.split(",").map(Number);
    if (fx?.length === 3) {
      const [p1, p2, rev] = fx;
      Object.assign(socket.amp.current, {
        pedal1Type: p1, pedal1On: true, pedal1: effectDefaults("pedal1", p1),
        pedal2Type: p2, pedal2On: true, pedal2: effectDefaults("pedal2", p2),
        reverbType: rev, reverbOn: true, reverb: effectDefaults("reverb", rev),
      });
    }
    return socket;
  },
});
const root = document.getElementById("app")!;
root.dataset.preview = PREVIEW_MARKER;
runApp(root, new Api({ fetch: fakeFetch(ampState) }), ws);
(window as unknown as { preview: unknown }).preview = { get amp() { return socket?.amp; }, prefs };

// Scripted steps for screenshots: wait until the amp state was read (CH buttons show names).
function whenReady(cb: () => void, tries = 100): void {
  const ready = document.querySelector(".ch-name")?.textContent && document.querySelector(".ch-name")?.textContent !== "—";
  if (ready || tries <= 0) return void setTimeout(cb, 150);
  setTimeout(() => whenReady(cb, tries - 1), 50);
}
whenReady(() => {
  const play = q.get("play");
  if (play) {
    document.querySelector<HTMLButtonElement>(`.song[data-id="${CSS.escape(play)}"] .play`)?.click();
    // save=1: once played, press "Uložit do komba…" in the result bar.
    if (q.get("save")) {
      const press = (tries = 100) => {
        const b = document.querySelector<HTMLButtonElement>(".song-dock .primary");
        if (b && !b.hidden) b.click();
        else if (tries > 0) setTimeout(() => press(tries - 1), 50);
      };
      press();
    }
  }
  const scroll = q.get("scroll");
  if (scroll) document.querySelector(scroll)?.scrollIntoView({ block: "start" });
  document.body.dataset.ready = "1";
});
