// HTTP API client, docs/design.md §5.

export type WifiMode = "sta" | "ap" | "connecting" | "off" | "unknown";
export type AmpConnState = "disconnected" | "detected" | "connected" | "busy" | "error";
export type AliasState = "established" | "registering" | "collision" | "failed" | "off";

export interface AmpStatus {
  state: AmpConnState;
  card: string | null;
  name: string | null;
  usb_id: string | null;
  device: string | null;
  identity: string | null;
  error: string | null;
}

export interface UpdateInfo {
  state: "idle" | "staged" | "applying";
  current_version: string;
  previous_version: string | null;
  pending: boolean;
  last_result: string | null;
}

export interface WifiStatus {
  mode: WifiMode;
  ssid: string | null;
  ip: string | null;
  signal: number | null;
  ap_ssid: string;
  ap_ip: string;
  last_error: string | null;
}

export interface Status {
  version: string;
  hostname: string;
  uptime_s: number;
  addresses: string[];
  wifi: WifiStatus;
  mdns: { hostname: string; alias: string; alias_state: AliasState };
  amp: AmpStatus;
  controller: { active: boolean };
  update: UpdateInfo;
  auth: { configured: boolean };
}

export interface Network {
  ssid: string;
  signal: number;
  security: string;
  in_use: boolean;
  saved: boolean;
}

export interface Saved {
  uuid: string;
  name: string;
  ssid: string;
  priority: number;
  active: boolean;
}

export interface SessionInfo {
  authenticated: boolean;
  configured: boolean;
}

/** status 0 = network failure (code "network"). */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly detail?: string,
    readonly retryAfter?: number,
  ) {
    super(`API error ${status} ${code}`);
    this.name = "ApiError";
  }
}

/** Builds an ApiError from an HTTP status and the (possibly non-JSON) response body. */
export function errorFromResponse(status: number, bodyText: string): ApiError {
  let body: { error?: unknown; detail?: unknown; retry_after?: unknown } = {};
  try {
    const v: unknown = JSON.parse(bodyText);
    if (v && typeof v === "object") body = v as typeof body;
  } catch {
    // Not JSON (e.g. a proxy error page): fall back to the HTTP status.
  }
  const code = typeof body.error === "string" ? body.error : `http_${status}`;
  const detail = typeof body.detail === "string" ? body.detail : undefined;
  const retry = typeof body.retry_after === "number" ? body.retry_after : undefined;
  return new ApiError(status, code, detail, retry);
}

export type FetchFn = (input: string, init?: RequestInit) => Promise<Response>;

export interface ApiOptions {
  fetch?: FetchFn;
  /** Called on every 401 before the promise rejects. */
  onUnauthorized?: () => void;
}

export class Api {
  onUnauthorized: (() => void) | undefined;
  private readonly fetchFn: FetchFn;

  constructor(opts: ApiOptions = {}) {
    this.fetchFn = opts.fetch ?? ((input, init) => fetch(input, init));
    this.onUnauthorized = opts.onUnauthorized;
  }

  session(): Promise<SessionInfo> {
    return this.call("GET", "/api/session");
  }

  login(password: string): Promise<{ ok: true }> {
    // A wrong password is not an expired session: do not trigger the 401 hook.
    return this.call("POST", "/api/login", { password }, false);
  }

  logout(): Promise<{ ok: true }> {
    return this.call("POST", "/api/logout");
  }

  status(): Promise<Status> {
    return this.call("GET", "/api/status");
  }

  wifiNetworks(rescan: boolean): Promise<{ networks: Network[]; saved: Saved[] }> {
    return this.call("GET", rescan ? "/api/wifi/networks?rescan=1" : "/api/wifi/networks");
  }

  wifiConnect(ssid: string, password: string): Promise<{ ok: true }> {
    return this.call("POST", "/api/wifi/connect", { ssid, password });
  }

  wifiForget(uuid: string): Promise<{ ok: true }> {
    return this.call("POST", "/api/wifi/forget", { uuid });
  }

  wifiHotspot(minutes: number): Promise<{ ok: true }> {
    return this.call("POST", "/api/wifi/hotspot", { minutes });
  }

  updateInfo(): Promise<UpdateInfo> {
    return this.call("GET", "/api/update");
  }

  /** Uploads an update package with progress (fetch has no upload progress). */
  uploadUpdate(file: Blob, onProgress: (loaded: number, total: number) => void): Promise<{ ok: true; version: string }> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/update");
      xhr.setRequestHeader("Content-Type", "application/gzip");
      xhr.upload.onprogress = (e) => onProgress(e.loaded, e.lengthComputable ? e.total : file.size);
      xhr.onerror = () => reject(new ApiError(0, "network"));
      xhr.onload = () => {
        if (xhr.status === 202 || xhr.status === 200) {
          try {
            resolve(JSON.parse(xhr.responseText));
          } catch {
            reject(new ApiError(xhr.status, "bad_response"));
          }
          return;
        }
        if (xhr.status === 401) this.onUnauthorized?.();
        reject(errorFromResponse(xhr.status, xhr.responseText));
      };
      xhr.send(file);
    });
  }

  private async call<T>(method: string, path: string, body?: unknown, notifyUnauthorized = true): Promise<T> {
    const init: RequestInit = { method, credentials: "same-origin", headers: {} };
    if (body !== undefined) {
      init.headers = { "Content-Type": "application/json" };
      init.body = JSON.stringify(body);
    }
    let res: Response;
    try {
      res = await this.fetchFn(path, init);
    } catch {
      throw new ApiError(0, "network");
    }
    const text = await res.text().catch(() => "");
    if (!res.ok) {
      if (res.status === 401 && notifyUnauthorized) this.onUnauthorized?.();
      throw errorFromResponse(res.status, text);
    }
    try {
      return JSON.parse(text) as T;
    } catch {
      throw new ApiError(res.status, "bad_response");
    }
  }
}
