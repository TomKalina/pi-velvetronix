// Shared application state passed to the views.

import type { Api, AmpStatus, Status, UpdateInfo } from "../api";
import type { AmpState, VoxClient } from "../protocol/client";
import type { ConnState, WsTransport } from "../ws";
import { controlGate, isUpdating, type Gate } from "./logic";
import type { AmpSync } from "./sync";

export interface AppData {
  status: Status | null;
  /** The last /api/status request failed: everything shown is stale. */
  statusError: boolean;
  /** Fed by both {"t":"amp"} and /api/status. */
  amp: AmpStatus | null;
  update: UpdateInfo | null;
  ws: ConnState;
  vox: AmpState;
  /** Set while the daemon restarts after an update; a 401 then does not log out. */
  restarting: boolean;
  /** Program name to put into the save form next time the control screen renders (from a song). */
  saveDraft: string | null;
}

export interface Ctx {
  api: Api;
  ws: WsTransport;
  client: VoxClient;
  sync: AmpSync;
  data: AppData;
  changed(): void;
  refreshStatus(): Promise<void>;
  logout(): void;
}

export function gateOf(ctx: Ctx): Gate {
  const d = ctx.data;
  return controlGate({
    ws: d.ws.kind,
    amp: d.amp?.state ?? null,
    updating: isUpdating(d.update),
    ready: ctx.sync.ready && d.vox.ready,
    reading: ctx.sync.reading,
    readError: ctx.sync.error,
  });
}
