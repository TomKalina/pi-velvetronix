# Tunnel for the MCP endpoint

Makes `/mcp/<token>` reachable from the internet, for MCP clients that do not run on the local
network (claude.ai connectors, the Claude apps). Nothing else of the daemon is exposed: the web UI,
the login and `/api/*` stay local.

```
claude.ai ──HTTPS── Cloudflare (vox.kaln.cz) ──tunnel── cloudflared ──HTTP── velvetronix.local/mcp/<token>
```

`cloudflared` runs on another always-on machine on the same network (here the Raspberry Pi 5),
not on the Pi Zero 2 W: the update package holds only the daemon (design §6), and the Zero has
512 MB RAM.

## Setup (done 2026-11-24)

1. A remotely managed tunnel `velvetronix` (Cloudflare API, `config_src: cloudflare`) with this
   ingress:

   ```json
   [{"hostname": "vox.kaln.cz", "path": "^/mcp/[A-Za-z0-9_-]{20,}$",
     "service": "http://192.168.0.150:80",
     "originRequest": {"httpHostHeader": "velvetronix.local", "connectTimeout": 10}},
    {"service": "http_status:404"}]
   ```

   The service uses the Pi Zero's IP: `cloudflared` does not resolve `.local` names. If the IP
   changes, update the ingress (or give the Pi Zero a fixed DHCP lease).
2. DNS: `vox.kaln.cz` CNAME `<tunnel id>.cfargotunnel.com`, proxied.
3. `cloudflared` (linux-arm64 release, sha256 checked against the release notes) in
   `~/.local/bin`, the tunnel token in `~/.config/velvetronix-tunnel/env` (`TUNNEL_TOKEN=...`,
   mode 0600), and the user unit [velvetronix-tunnel.service](velvetronix-tunnel.service):

   ```sh
   cp velvetronix-tunnel.service ~/.config/systemd/user/
   systemctl --user daemon-reload && systemctl --user enable --now velvetronix-tunnel
   loginctl enable-linger "$USER"   # runs without a login session
   ```

## Use

The connector URL is `https://vox.kaln.cz/mcp/<token>` with the token from
`POST /api/mcp/token` (README, MCP endpoint). In claude.ai: Settings → Connectors → Add custom
connector, no OAuth. The URL is the secret: whoever has it controls the amp. A new token or a
changed web password revokes it.
