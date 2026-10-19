// Dev only: screenshots of the preview (npm run preview first) with headless Google Chrome over
// the DevTools pipe (no network port). Usage: node dev/shots.mjs [out-dir] [name-filter]
// (default out-dir: dist/preview/shots).

import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const page = fileURLToPath(new URL("../dist/preview/index.html", import.meta.url));
const out = process.argv[2] || fileURLToPath(new URL("../dist/preview/shots", import.meta.url));
const only = process.argv[3];
mkdirSync(out, { recursive: true });

const DESKTOP = { width: 1280, height: 900, scale: 1, mobile: false };
const PHONE = { width: 390, height: 844, scale: 2, mobile: true };

/** name, viewport, query+hash, full page? */
const SHOTS = [
  ["graphic-desktop", DESKTOP, "?display=graphic&names=original&scheme=light#ovladani", false],
  ["graphic-desktop-full", DESKTOP, "?display=graphic&names=original&scheme=light#ovladani", true],
  ["graphic-phone", PHONE, "?display=graphic&names=original&scheme=light#ovladani", false],
  ["graphic-phone-full", { ...PHONE, scale: 1 }, "?display=graphic&names=original&scheme=light#ovladani", true],
  ["graphic-phone-effects", PHONE, "?display=graphic&names=original&scheme=light&scroll=.pedalboard#ovladani", false],
  ["technical-phone", PHONE, "?display=technical&names=original&scheme=light#ovladani", false],
  ["technical-phone-full", { ...PHONE, scale: 1 }, "?display=technical&names=code&scheme=light#ovladani", true],
  ["songs-phone", PHONE, "?names=original&scheme=light#pisnicky", false],
  ["songs-phone-played", PHONE, "?names=original&scheme=light&play=ramones-blitzkrieg-bop#pisnicky", false],
  ["save-prefill-phone", PHONE, "?names=original&scheme=light&play=plexis-svet-jsou-bary&save=1#pisnicky", false],
  ["songs-desktop-dark", DESKTOP, "?names=code&scheme=dark#pisnicky", false],
  ["graphic-desktop-dark", DESKTOP, "?display=graphic&names=code&scheme=dark#ovladani", false],
  ["graphic-phone-dark", PHONE, "?display=graphic&names=original&scheme=dark#ovladani", false],
  ["technical-phone-dark", PHONE, "?display=technical&scheme=dark#ovladani", false],
  ["info-phone-dark", PHONE, "?scheme=dark#info", false],
  ["disconnected-phone", PHONE, "?amp=disconnected&display=graphic&scheme=light#ovladani", false],
  ["graphic-phone-360", { ...PHONE, width: 360 }, "?display=graphic&names=original&scheme=light#ovladani", false],
  ["fx-orange-chorus-phone", PHONE, "?display=graphic&names=original&scheme=light&fx=6,3,3&scroll=.pedalboard#ovladani", false],
  ["fx-chorus-desktop", DESKTOP, "?display=graphic&names=original&scheme=light&fx=1,5,1&scroll=.pedalboard#ovladani", false],
  ["fx-light-desktop", DESKTOP, "?display=graphic&names=original&scheme=dark&fx=5,6,2&scroll=.pedalboard#ovladani", false],
  ["fx-misc-desktop", DESKTOP, "?display=graphic&names=original&scheme=light&fx=2,2,0&scroll=.pedalboard#ovladani", false],
];

const profile = mkdtempSync(join(out, ".chrome-"));
const chrome = spawn(CHROME, [
  "--headless=new", "--remote-debugging-pipe", `--user-data-dir=${profile}`, "--no-first-run",
  "--no-default-browser-check", "--disable-extensions", "--hide-scrollbars", "--mute-audio", "about:blank",
], { stdio: ["ignore", "ignore", "pipe", "pipe", "pipe"] });

let seq = 0;
const waiting = new Map();
let buf = "";
chrome.stdio[4].on("data", (d) => {
  buf += d.toString();
  let i;
  while ((i = buf.indexOf("\0")) >= 0) {
    const msg = JSON.parse(buf.slice(0, i));
    buf = buf.slice(i + 1);
    if (msg.id && waiting.has(msg.id)) {
      const { resolve, reject } = waiting.get(msg.id);
      waiting.delete(msg.id);
      if (msg.error) reject(new Error(JSON.stringify(msg.error)));
      else resolve(msg.result);
    }
  }
});
function cdp(method, params = {}, sessionId) {
  const id = ++seq;
  chrome.stdio[3].write(JSON.stringify({ id, method, params, sessionId }) + "\0");
  return new Promise((resolve, reject) => waiting.set(id, { resolve, reject }));
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

try {
  for (const [name, vp, query, full] of SHOTS) {
    if (only && !name.includes(only)) continue;
    const { targetId } = await cdp("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await cdp("Target.attachToTarget", { targetId, flatten: true });
    const s = (m, p) => cdp(m, p, sessionId);
    await s("Page.enable");
    await s("Emulation.setDeviceMetricsOverride", { width: vp.width, height: vp.height, deviceScaleFactor: vp.scale, mobile: vp.mobile });
    await s("Page.navigate", { url: `file://${page}${query}` });
    for (let i = 0; i < 100; i++) {
      const r = await s("Runtime.evaluate", { expression: "document.body && document.body.dataset.ready === '1'", returnByValue: true });
      if (r.result.value) break;
      await sleep(50);
    }
    await sleep(query.includes("play=") ? 2500 : 400);
    if (full) {
      const m = await s("Page.getLayoutMetrics");
      const height = Math.ceil(m.cssContentSize.height);
      await s("Emulation.setDeviceMetricsOverride", { width: vp.width, height, deviceScaleFactor: vp.scale, mobile: vp.mobile });
      await sleep(200);
    }
    const shot = await s("Page.captureScreenshot", { format: "png" });
    const file = join(out, `${name}.png`);
    writeFileSync(file, Buffer.from(shot.data, "base64"));
    console.log(file);
    await cdp("Target.closeTarget", { targetId });
  }
} finally {
  const exited = new Promise((r) => chrome.on("exit", r));
  chrome.kill();
  await exited;
  rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
}
