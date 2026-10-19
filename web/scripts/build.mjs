// Builds the single-file UI: src/index.html with inline CSS + JS, gzip -9, written to
// ../internal/web/dist/index.html.gz (docs/design.md §9). Output is deterministic.

import { build, transform } from "esbuild";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { constants, gzipSync } from "node:zlib";

const web = (p) => fileURLToPath(new URL(`../${p}`, import.meta.url));
const outFile = web("../internal/web/dist/index.html.gz");

const js = await build({
  entryPoints: [web("src/main.ts")],
  bundle: true,
  minify: true,
  format: "iife",
  target: ["es2022"],
  platform: "browser",
  write: false,
  legalComments: "none",
  charset: "utf8",
  logLevel: "warning",
});
const css = await transform(await readFile(web("src/styles.css"), "utf8"), {
  loader: "css",
  minify: true,
  legalComments: "none",
  charset: "utf8",
});

const script = js.outputFiles[0].text.replaceAll("</script", "<\\/script");
const style = css.code.replaceAll("</style", "<\\/style");

let html = await readFile(web("src/index.html"), "utf8");
for (const marker of ["<!--INLINE_CSS-->", "<!--INLINE_JS-->"]) {
  if (!html.includes(marker)) throw new Error(`index.html: missing ${marker}`);
}
// Function replacers: minified code may contain "$&" and similar replacement patterns.
html = html
  .replace("<!--INLINE_CSS-->", () => `<style>${style}</style>`)
  .replace("<!--INLINE_JS-->", () => `<script>${script}</script>`);

const gz = gzipSync(Buffer.from(html, "utf8"), { level: constants.Z_BEST_COMPRESSION });
// gzip header: MTIME (bytes 4-7) is already 0; the OS byte (9) depends on the build host
// (19 on macOS, 3 on Linux). Pin it to 3 (Unix) so Mac and Linux builds match. Not covered by the CRC.
gz[9] = 3;

await mkdir(dirname(outFile), { recursive: true });
await writeFile(outFile, gz);

const sha = createHash("sha256").update(gz).digest("hex");
console.log(`${outFile}: ${gz.length} bytes gzip (${Buffer.byteLength(html)} raw), sha256 ${sha}`);
