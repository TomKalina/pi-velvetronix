// Exports the protocol tables, the song presets and test vectors from the TypeScript sources for the
// Go MCP endpoint (internal/voxproto, docs/design.md §7.1). The Go code embeds the data files and its
// tests replay the vectors, so both implementations stay in step. Output is deterministic.
//   node scripts/go-data.mjs           write the files
//   node scripts/go-data.mjs --check   exit 1 if a file is out of date (scripts/test.sh)

import { build } from "esbuild";
import { readdir, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const web = (p) => fileURLToPath(new URL(`../${p}`, import.meta.url));
const check = process.argv.includes("--check");

const bundled = await build({
  stdin: {
    contents: `
      export * as tables from "./src/protocol/tables";
      export { auditionPlan } from "./src/protocol/audition";
      export { msg, parse } from "./src/protocol/messages";
      export { encodeProgram, applyParam } from "./src/protocol/program";
      export { pack7 } from "./src/protocol/encoding";
      export { parseVtxprog, programToSettings } from "./src/protocol/vtxprog";
      export { SONGS } from "./src/ui/songs";
      export { VTX_PRESETS } from "./src/ui/vtx-presets";`,
    resolveDir: web(""), loader: "ts",
  },
  bundle: true, format: "esm", platform: "node", write: false, logLevel: "warning",
});
const P = await import(`data:text/javascript;base64,${Buffer.from(bundled.outputFiles[0].text).toString("base64")}`);
const T = P.tables;

const hex = (b) => Buffer.from(b).toString("hex");

const tables = {
  ampModels: T.AMP_MODELS,
  ampParams: T.AMP_PARAMS,
  pedal1Types: T.PEDAL1_TYPES,
  pedal2Types: T.PEDAL2_TYPES,
  reverbTypes: T.REVERB_TYPES,
  effectParams: T.EFFECT_PARAMS,
  biasNames: T.BIAS_NAMES,
  classNames: T.CLASS_NAMES,
  modeNames: T.MODE_NAMES,
  userSlotNames: T.USER_SLOT_NAMES,
};

const songs = [...P.SONGS, ...P.VTX_PRESETS].map((s) => ({
  id: s.id, title: s.title, artist: s.artist, year: s.year, genre: s.genre, description: s.description,
  amp: s.amp, nr: s.nr, pedal1: s.pedal1, pedal2: s.pedal2, reverb: s.reverb,
}));

/** Program in the Go JSON shape (raw as hex). */
const prog = (p) => ({
  raw: hex(p.raw), name: p.name, nr: p.nr, pedal1On: p.pedal1On, pedal2On: p.pedal2On, reverbOn: p.reverbOn,
  ampModel: p.ampModel, amp: p.amp, pedal1Type: p.pedal1Type, pedal1: p.pedal1, pedal2Type: p.pedal2Type,
  pedal2: p.pedal2, reverbType: p.reverbType, reverb: p.reverb,
});

const audition = songs.map((s) => ({
  id: s.id,
  steps: P.auditionPlan(s).map((st) => ({
    label: st.label, frame: hex(st.frame), hold: st.hold ?? null, slot: st.slot ?? null, isType: st.isType ?? false,
  })),
}));

// Live edits applied to every valid program of the bundled .vtxprog files.
const EDITS = [[0x00, 3, 0x41], [0x00, 15, 0x5a], [0x01, 0, 77], [0x02, 1, 0], [0x02, 2, 1], [0x02, 4, 1],
  [0x03, 0, 11], [0x03, 1, 9], [0x03, 2, 6], [0x03, 4, 2], [0x04, 0, 99], [0x04, 11, 1], [0x05, 0, 1234],
  [0x05, 5, 7], [0x06, 0, 4321], [0x08, 4, 3], [0x09, 0, 1], [0x04, 12, 5]];

const vtxprog = [];
const dir = web("research/vtxprog/marcosmoura");
const files = [];
for (const file of (await readdir(dir)).filter((f) => f.endsWith(".vtxprog")).sort()) {
  files.push({ file, bytes: new Uint8Array(await readFile(`${dir}/${file}`)) });
}
// A broken copy of the first file: GAIN 101 in program 0, an unknown amp model in program 1.
const broken = Uint8Array.from(files[0].bytes);
broken[32 + 0x13] = 101;
broken[32 + 62 + 0x12] = 0x30;
files.push({ file: "broken.vtxprog", bytes: broken });
for (const { file, bytes } of files) {
  vtxprog.push({
    file,
    bytes: hex(bytes),
    entries: P.parseVtxprog(bytes).map((e) => {
      if ("error" in e) return { index: e.index, error: e.error };
      return {
        index: e.index,
        program: prog(e.program),
        settings: P.programToSettings(e.program),
        packed: hex(P.pack7(P.encodeProgram(e.program))),
        write: hex(P.msg.writeProgram(e.index % 8, e.program)),
        // Edits on the first program of each file only, to keep the vectors small.
        edits: e.index > 0 ? [] : EDITS.map(([id, sub, value]) => ({ id, sub, value, program: prog(P.applyParam(e.program, id, sub, value)) })),
      };
    }),
  });
}

const frames = [
  "f0423000013423f7", "f0423000013424f7", "f0423000013426f7", "f04230000134210003f7", "f04230000134220009f7",
  "f04230000134420103f7", "f042300001344e0205f7", "f042300001344e0309f7", "f04230000134410400320000f7",
  "f042300001344105015409f7", "f07e000602423401000000000100f7", "f0423000013499f7", "f0f7",
];
const parse = frames.map((f) => {
  const p = P.parse(Uint8Array.from(Buffer.from(f, "hex")));
  const out = { ...p };
  if (p.kind === "current_program" || p.kind === "program") out.program = prog(p.program);
  if (p.kind === "unknown") delete out.frame;
  return { frame: f, parsed: out };
});
for (const v of vtxprog) {
  for (const e of v.entries.filter((x) => x.packed)) {
    parse.push({ frame: `f0423000013440${e.packed}f7`, parsed: { kind: "current_program", program: e.program } });
  }
}

const msgs = {
  deviceInquiry: hex(P.msg.deviceInquiry()), modeRequest: hex(P.msg.modeRequest()),
  currentProgramRequest: hex(P.msg.currentProgramRequest()),
  programRequest: [0, 7].map((s) => hex(P.msg.programRequest(s))),
  selectProgram: [0, 7].map((s) => hex(P.msg.selectProgram(s))),
};

const json = (v) => `${JSON.stringify(v, null, 1)}\n`;
const outputs = {
  "../internal/voxproto/data/tables.json": json(tables),
  "../internal/voxproto/data/songs.json": json(songs),
  "../internal/voxproto/testdata/vectors.json": `${JSON.stringify({ audition, vtxprog, parse, msgs })}\n`,
};

let stale = false;
for (const [path, text] of Object.entries(outputs)) {
  const file = web(path);
  if (check) {
    const old = await readFile(file, "utf8").catch(() => "");
    if (old !== text) {
      console.error(`go-data: ${path} is out of date; run node scripts/go-data.mjs`);
      stale = true;
    }
  } else {
    await writeFile(file, text);
  }
}
if (stale) process.exit(1);
if (!check) console.log(`go-data: ${songs.length} songs, ${vtxprog.length} .vtxprog files, ${parse.length} parse vectors`);
