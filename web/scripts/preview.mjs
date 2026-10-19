// Dev only: builds the UI against the simulated amp (dev/preview.ts) into dist/preview/index.html.
// Open that file in a browser; nothing talks to the Pi or the amp. Not part of `npm run build`.

import { build, transform } from "esbuild";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

const web = (p) => fileURLToPath(new URL(`../${p}`, import.meta.url));
const outFile = web("dist/preview/index.html");

const js = await build({
  entryPoints: [web("dev/preview.ts")],
  bundle: true, minify: false, format: "iife", target: ["es2022"], platform: "browser",
  write: false, charset: "utf8", logLevel: "warning",
});
const css = await transform(await readFile(web("src/styles.css"), "utf8"), { loader: "css", charset: "utf8" });
const html = (await readFile(web("src/index.html"), "utf8"))
  .replace("<title>VOX Velvetronix</title>", "<title>VOX Velvetronix (náhled)</title>")
  .replace("<!--INLINE_CSS-->", () => `<style>${css.code.replaceAll("</style", "<\\/style")}</style>`)
  .replace("<!--INLINE_JS-->", () => `<script>${js.outputFiles[0].text.replaceAll("</script", "<\\/script")}</script>`);
await mkdir(dirname(outFile), { recursive: true });
await writeFile(outFile, html);
console.log(`preview: file://${outFile}`);
