// Wires API, WebSocket transport, VOX client and views. main.ts runs it against the device;
// dev/preview.ts runs it against a simulated amp.

import { ApiError, type Api } from "./api";
import { VoxClient } from "./protocol/client";
import type { AppData, Ctx } from "./ui/context";
import { h, toast } from "./ui/dom";
import { ampErrorText } from "./ui/errors";
import { createLogin } from "./ui/login";
import { isUpdating } from "./ui/logic";
import { createShell } from "./ui/shell";
import { AmpSync } from "./ui/sync";
import type { WsTransport } from "./ws";

const STATUS_POLL_MS = 5000;

export function runApp(root: HTMLElement, api: Api, ws: WsTransport): void {
  const client = new VoxClient(ws);
  const sync = new AmpSync(client, changed);

  const data: AppData = {
    status: null, statusError: false, amp: null, update: null, ws: ws.connState,
    vox: { mode: null, program: null, current: null, slots: new Array(8).fill(null), ready: false },
    restarting: false,
    saveDraft: null,
  };
  const ctx: Ctx = { api, ws, client, sync, data, changed, refreshStatus, logout };

  let shell: ReturnType<typeof createShell> | null = null;
  let poll: ReturnType<typeof setInterval> | null = null;
  let renderQueued = false;

  function changed(): void {
    // readAll() only once the bridge and the amp are both up; nothing is ever written here.
    sync.update(data.ws.kind === "open" && data.amp?.state === "connected" && !isUpdating(data.update));
    if (renderQueued || !shell) return;
    renderQueued = true;
    requestAnimationFrame(() => {
      renderQueued = false;
      shell?.render();
    });
  }

  ws.onState((s) => {
    data.ws = s;
    changed();
  });
  ws.onText((m) => {
    if (m.t === "amp") data.amp = m.amp;
    else if (m.t === "update") data.update = m.update;
    else return;
    changed();
  });
  ws.onError((e) => toast(ampErrorText(e.code)));
  client.onState((s) => {
    data.vox = s;
    changed();
  });

  async function refreshStatus(): Promise<void> {
    try {
      const st = await api.status();
      data.status = st;
      data.statusError = false;
      // While the socket is open its amp messages (sent on connect and on change) are newer than a
      // polled snapshot. {"t":"update"} comes only on change, so update state is always taken from here.
      if (data.ws.kind !== "open") data.amp = st.amp;
      data.update = st.update;
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return;
      data.statusError = true;
    }
    changed();
  }

  function startApp(): void {
    shell = createShell(ctx);
    root.replaceChildren(shell.el);
    ws.start();
    void refreshStatus();
    poll = setInterval(() => void refreshStatus(), STATUS_POLL_MS);
  }

  function stopApp(): void {
    if (poll !== null) clearInterval(poll);
    poll = null;
    ws.stop();
    shell?.destroy();
    shell = null;
    Object.assign(data, { status: null, statusError: false, amp: null, update: null });
  }

  function showLogin(configured: boolean, message?: string): void {
    root.replaceChildren(createLogin(api, { configured, message, onSuccess: startApp }));
  }

  function logout(): void {
    api.logout().catch(() => {}).finally(() => {
      stopApp();
      showLogin(true);
    });
  }

  api.onUnauthorized = () => {
    // The daemon restarts during an update; that is not a logout.
    if (data.restarting || !shell) return;
    stopApp();
    showLogin(true, "Přihlášení vypršelo, přihlaste se znovu.");
  };

  async function boot(): Promise<void> {
    try {
      const s = await api.session();
      if (s.authenticated) startApp();
      else showLogin(s.configured);
    } catch {
      root.replaceChildren(
        h(
          "main",
          { class: "login-wrap" },
          h("div", { class: "card login" }, h("p", { class: "msg msg-err" }, "Zařízení neodpovídá."), h("button", { type: "button", onclick: () => void boot() }, "Zkusit znovu")),
        ),
      );
    }
  }

  void boot();
}
