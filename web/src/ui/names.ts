// Original gear behind the amp's model and effect names (VOX never names it; see disclaimer).
// Generated from the research file web/research/names.json (2026-10-19, sources per entry; not bundled).
// confidence: "official" = VOX names or unambiguously describes it, "secondary" = third-party source that
// matches VOX's description, "inferred" = guess from VOX's description, from a single or disputed
// third-party source, or from the name only.

export type Confidence = "official" | "secondary" | "inferred";

export interface OriginalName {
  /** The amp's own name, as in tables.ts. */
  code: string;
  /** Full Czech description of the original. */
  original: string;
  /** Compact name for selects and labels; null = no specific original (the code name is shown). */
  short: string | null;
  confidence: Confidence;
  note?: string;
}

export const AMP_NAMES: readonly OriginalName[] = [
  { code: "DELUXE CL VIBRATO", original: "Fender Deluxe Reverb (blackface, '65), kanál Vibrato", short: "Fender Deluxe Reverb (Vibrato)", confidence: "secondary" },
  { code: "DELUXE CL NORMAL", original: "Fender Deluxe Reverb (blackface, '65), kanál Normal", short: "Fender Deluxe Reverb (Normal)", confidence: "secondary", note: "VT-X manuál popisuje jen kanál Vibrato; Normal je kanál stejného ampu (Tone Room)." },
  { code: "TWEED 4x10 BRIGHT", original: "Fender Bassman 4x10 (tweed, 1959), kanál Bright", short: "Fender Bassman 4x10 (Bright)", confidence: "secondary" },
  { code: "TWEED 4x10 NORMAL", original: "Fender Bassman 4x10 (tweed, 1959), kanál Normal", short: "Fender Bassman 4x10 (Normal)", confidence: "secondary" },
  { code: "BOUTIQUE CL", original: "Dumble Overdrive Special, čistý kanál", short: "Dumble ODS (čistý)", confidence: "secondary", note: "VOX uvádí název 'Overdrive Special' bez značky; 'Dumble' je ze sekundárních zdrojů." },
  { code: "BOUTIQUE OD", original: "Dumble Overdrive Special, overdrive kanál", short: "Dumble ODS (overdrive)", confidence: "secondary" },
  { code: "VOX AC30", original: "VOX AC30, kanál Normal (repro Celestion Alnico Blue)", short: "VOX AC30 (Normal)", confidence: "official" },
  { code: "VOX AC30TB", original: "VOX AC30 Top Boost (1964)", short: "VOX AC30 Top Boost", confidence: "official" },
  { code: "BRIT 1959 TREBLE", original: "Marshall 1959 Super Lead 100W (\"Plexi\"), kanál High Treble", short: "Marshall Plexi 1959 (Treble)", confidence: "secondary", note: "Librarian uvádí 'Marshall JTM', ToneBucket 'Super Lead Plexi'; '1959' = typové číslo Marshall 1959 Super Lead." },
  { code: "BRIT 1959 NORMAL", original: "Marshall 1959 Super Lead 100W (\"Plexi\"), kanál Normal", short: "Marshall Plexi 1959 (Normal)", confidence: "secondary" },
  { code: "BRIT 800", original: "Marshall JCM800 (2203, 1983)", short: "Marshall JCM800", confidence: "secondary" },
  { code: "BRIT VM", original: "pravděpodobně Marshall JVM410H", short: "Marshall JVM410H", confidence: "inferred", note: "Jen z neoficiálních zdrojů (Librarian, ToneBucket: kanál OD2); VT-X manuál model nepopisuje." },
  { code: "SL-OD", original: "pravděpodobně Soldano SLO-100, overdrive kanál", short: "Soldano SLO-100", confidence: "inferred", note: "Jen z neoficiálních zdrojů; VT-X manuál model nepopisuje." },
  { code: "DOUBLE REC", original: "Mesa/Boogie Dual Rectifier, high-gain kanál", short: "Mesa/Boogie Dual Rectifier", confidence: "secondary" },
  { code: "CALI ELATION", original: "pravděpodobně Bogner Ecstasy, kanál 3", short: "Bogner Ecstasy", confidence: "inferred", note: "Jediný zdroj (ToneBucket); Librarian značku neuvádí. Brát s rezervou." },
  { code: "ERUPT III CH2", original: "pravděpodobně EVH 5150III, kanál 2", short: "EVH 5150III (kanál 2)", confidence: "inferred", note: "VT-X manuál model nepopisuje. Zdroje se rozcházejí ve značce: ToneBucket EVH, Librarian 'Peavy 5150 III' (5150III vyrábí EVH; Peavey dělal 5150/5150 II)." },
  { code: "ERUPT III CH3", original: "pravděpodobně EVH 5150III, kanál 3", short: "EVH 5150III (kanál 3)", confidence: "inferred", note: "Viz ERUPT III CH2." },
  { code: "BOUTIQUE METAL", original: "Diezel VH4, kanál \"Heavy\"", short: "Diezel VH4", confidence: "secondary" },
  { code: "BRIT OR MKII", original: "Orange Rockerverb 100 MkII, kanál Dirty", short: "Orange Rockerverb MkII", confidence: "secondary", note: "Librarian (L241) uvádí 'Orange Super Crush 100' (tranzistorový, bez lampového reverbu: s popisem VOX nesedí). Rockerverb MkII sedí." },
  { code: "ORIGINAL CL", original: "vlastní čistý zvuk VOX (bez předlohy)", short: "VOX čistý (bez předlohy)", confidence: "secondary", note: "Librarian: 'No additional simulation, just the VTX amp'." },
];

export const PEDAL1_NAMES: readonly OriginalName[] = [
  { code: "COMP", original: "obecný kompresor (předloha neuvedena)", short: null, confidence: "inferred" },
  { code: "CHORUS", original: "pravděpodobně analogový chorus typu Boss CE", short: "Boss CE chorus", confidence: "inferred", note: "Starší VOX (VTX150/VT+) mají 'CE CHORUS': 'a standard rich-sounding analog chorus unit'." },
  { code: "TUBE OD", original: "pravděpodobně Ibanez Tube Screamer (TS808/TS9)", short: "Ibanez Tube Screamer", confidence: "inferred" },
  { code: "GOLD DRIVE", original: "pravděpodobně Klon Centaur", short: "Klon Centaur", confidence: "inferred", note: "Popis ze starších VOX manuálů (stejný název efektu); kentaur = Centaur." },
  { code: "TREBLE BOOST", original: "pravděpodobně VOX VBM-1 (treble booster, styl Dallas Rangemaster)", short: "VOX VBM-1 booster", confidence: "inferred", note: "Citace z VT+/VTX150 (stejný název efektu); u VT-X VOX nepotvrzuje." },
  { code: "RC TURBO", original: "neznámá předloha (snad Xotic RC Booster)", short: "Xotic RC Booster?", confidence: "inferred", note: "Žádný oficiální ani sekundární popis nenalezen. Odhad jen podle názvu." },
  { code: "ORANGE DIST", original: "pravděpodobně Boss DS-1", short: "Boss DS-1", confidence: "inferred" },
  { code: "FAT DIST", original: "neznámá předloha (snad distortion ve stylu ProCo RAT)", short: "ProCo RAT?", confidence: "inferred", note: "Žádný popis nenalezen. Odhad slabý." },
  { code: "BRIT LEAD", original: "neznámá předloha (snad Marshall pedál, např. Guv'nor)", short: "Marshall Guv'nor?", confidence: "inferred", note: "Žádný popis nenalezen. Odhad jen podle názvu." },
  { code: "FUZZ", original: "pravděpodobně fuzz ve stylu Fuzz Face", short: "Fuzz Face", confidence: "inferred", note: "Popis ze starších VOX manuálů; předlohu VOX neuvádí." },
];

export const PEDAL2_NAMES: readonly OriginalName[] = [
  { code: "FLANGER", original: "pravděpodobně MXR M-117 Flanger (EVH)", short: "MXR M-117 Flanger", confidence: "inferred", note: "Narážka na Eddie Van Halena." },
  { code: "BLACK PHASER", original: "neznámá předloha (analogový phaser v černé krabičce)", short: null, confidence: "inferred", note: "Žádný popis nenalezen." },
  { code: "ORANGE PHASER 1", original: "pravděpodobně MXR Phase 90", short: "MXR Phase 90", confidence: "inferred", note: "Návod k VT100X: 'a popular analog phaser in an orange box'; VT+/VTX150 píšou 'banana-colored box'." },
  { code: "ORANGE PHASER 2", original: "pravděpodobně MXR Phase 90 (jiná varianta, snad script/block)", short: "MXR Phase 90 (jiná verze)", confidence: "inferred", note: "Rozdíl proti ORANGE PHASER 1 nikde nepopsán." },
  { code: "TREMOLO", original: "pravděpodobně tremolo z Fender Twin Reverb", short: "Fender Twin tremolo", confidence: "inferred", note: "VTX150 má stejný popis pod názvem 'TWIN TREM'." },
  { code: "TAPE ECHO", original: "pravděpodobně Roland RE-201 Space Echo", short: "Roland Space Echo", confidence: "inferred", note: "Odhad: zpoždění řízené rychlostí motoru sedí na Space Echo; může jít i o Echoplex." },
  { code: "ANALOG DELAY", original: "analogový BBD delay (např. Boss DM-2)", short: "Boss DM-2", confidence: "inferred", note: "Popis ze starších VOX manuálů; konkrétní předlohu VOX neuvádí." },
];

export const REVERB_NAMES: readonly OriginalName[] = [
  { code: "ROOM", original: "obecný pokojový reverb (bez předlohy)", short: null, confidence: "official" },
  { code: "SPRING", original: "pružinový reverb vestavěný v kytarových comba (obecně)", short: null, confidence: "official" },
  { code: "HALL", original: "obecný sálový reverb (bez předlohy)", short: null, confidence: "official" },
  { code: "PLATE", original: "studiový deskový reverb (pravděpodobně styl EMT 140)", short: "EMT 140 plate", confidence: "inferred", note: "VOX říká jen 'classic plate reverb'; EMT 140 je odhad." },
];

/** VT100X owner's manual p.3, verbatim, plus the Czech line shown in the UI. */
export const NAMES_DISCLAIMER = {
  text: "All product names and company names are the trademarks or registered trademarks of their respective owners.",
  cs: "Názvy výrobků a firem jsou ochranné známky nebo registrované ochranné známky svých vlastníků. Uvedené předlohy jsou neoficiální, slouží jen k popisu charakteru zvuku.",
  note: "VOX ve VT-X manuálech značky předloh nejmenuje, jen je opisuje ('This models ...'). Značky výše jsou dekódované ze sekundárních zdrojů nebo odhadnuté.",
} as const;

export const CONFIDENCE_LABEL: Record<Confidence, string> = {
  official: "potvrzeno VOX",
  secondary: "podle neoficiálních zdrojů",
  inferred: "odhad",
};

export type NameKind = "amp" | "pedal1" | "pedal2" | "reverb";
/** "original" = the gear the model imitates, "code" = the amp's own name. */
export type NameMode = "original" | "code";

const TABLES: Record<NameKind, readonly OriginalName[]> = {
  amp: AMP_NAMES, pedal1: PEDAL1_NAMES, pedal2: PEDAL2_NAMES, reverb: REVERB_NAMES,
};

export interface ShownName {
  text: string;
  /** Tooltip: code name, full original, confidence and note. */
  title: string;
  /** A guess: shown with "~" and toned down. */
  modest: boolean;
  /** From third-party sources only (not VOX): shown with a dotted underline. */
  unofficial: boolean;
}

export function nameEntry(kind: NameKind, id: number): OriginalName | undefined {
  return TABLES[kind][id];
}

export function shownName(kind: NameKind, id: number, mode: NameMode): ShownName {
  const e = TABLES[kind][id];
  if (!e) return { text: `#${id}`, title: "", modest: false, unofficial: false };
  const title = `${e.code}: ${e.original} (${CONFIDENCE_LABEL[e.confidence]})${e.note ? `. ${e.note}` : ""}`;
  if (mode === "code" || e.short === null) return { text: e.code, title, modest: false, unofficial: false };
  const modest = e.confidence === "inferred";
  return { text: modest ? `~${e.short}` : e.short, title, modest, unofficial: e.confidence === "secondary" };
}
