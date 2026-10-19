// "Názvy: originál / z komba" switch; the choice applies to every screen.

import type { Ctx } from "./context";
import type { NameMode } from "./names";
import { prefs, setPref } from "./prefs";
import { segmented } from "./segmented";

export function namesSwitch(ctx: Ctx) {
  const s = segmented<NameMode>("Názvy", [["original", "originál"], ["code", "z komba"]], () => prefs.names, (v) => {
    setPref("names", v);
    ctx.changed();
  });
  s.el.title = "Originál = zařízení, které model napodobuje (neoficiální: tečkovaně podtržené = podle neoficiálních zdrojů, ~ = odhad). Z komba = názvy, jak je píše VOX.";
  return s;
}
