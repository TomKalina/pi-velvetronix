# pi-velvetronix MCP server

[English](README.md) · **Česky**

MCP server (stdio), který ovládá kombo přes daemon pi-velvetronix stejně jako webové UI:
přihlásí se přes `/api/login`, připojí se jako ovladač na `/ws` a posílá zprávy přes
`web/src/protocol` (sdílený kód s webem, žádná kopie protokolu).

Nástroje: `vox_status`, `vox_catalog`, `vox_set_sound` (živě, nic neukládá, nemění VOLUME),
`vox_list_songs` a `vox_play_song` (písničky i presety Tone Room), `vox_read_vtxprog`
a `vox_play_vtxprog` (soubor .vtxprog z disku), `vox_select_program`, `vox_save_program`
(přepíše slot), `vox_disconnect`.

Kombo může ovládat až 8 oken a MCP server najednou. Otevřené stránky vidí změny z MCP živě.
Po 8 s bez volání se server odpojí. Když je plno, `?takeover=1` odpojí nejstarší okno.

```sh
cd mcp && npm ci && npm run build
claude mcp add -s user velvetronix -e VOX_URL=http://velvetronix.local -e VOX_PASSWORD=… \
  -- node "$PWD/dist/server.mjs"
```
