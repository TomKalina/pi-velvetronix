// Bundles the MCP server (with web/src/protocol and its npm deps) into dist/server.mjs.

import { build } from "esbuild";

await build({
  entryPoints: [new URL("src/server.ts", import.meta.url).pathname],
  outfile: new URL("dist/server.mjs", import.meta.url).pathname,
  bundle: true,
  platform: "node",
  format: "esm",
  target: ["node22"],
  legalComments: "none",
  logLevel: "warning",
  banner: { js: "import { createRequire } from 'node:module'; const require = createRequire(import.meta.url);" },
});
