# pi-velvetronix MCP server

**English** · [Česky](README.cs.md)

An MCP server (stdio) that controls the amp through the pi-velvetronix daemon the same way the web
UI does: it logs in with `/api/login`, connects as a controller to `/ws` and sends messages through
`web/src/protocol` (code shared with the web UI, no copy of the protocol).

Tools: `vox_status`, `vox_catalog`, `vox_set_sound` (live, saves nothing, does not change VOLUME),
`vox_list_songs` and `vox_play_song` (songs and Tone Room presets), `vox_read_vtxprog` and
`vox_play_vtxprog` (a .vtxprog file on disk), `vox_select_program`, `vox_save_program` (overwrites
the slot), `vox_disconnect`.

Up to 8 windows and the MCP server can control the amp at once. Open pages see the MCP server's
changes live. The server disconnects after 8 s without a call. When all slots are taken,
`?takeover=1` drops the oldest window.

```sh
cd mcp && npm ci && npm run build
claude mcp add -s user velvetronix -e VOX_URL=http://velvetronix.local -e VOX_PASSWORD=… \
  -- node "$PWD/dist/server.mjs"
```
