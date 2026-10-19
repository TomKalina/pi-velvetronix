import { describe, expect, it, vi } from "vitest";
import { Api, ApiError, errorFromResponse, type FetchFn } from "./api";

function fakeFetch(status: number, body: string, calls: { path: string; init?: RequestInit }[] = []): FetchFn {
  return async (path, init) => {
    calls.push({ path, init });
    return new Response(body, { status });
  };
}

describe("errorFromResponse", () => {
  it("reads code, detail and retry_after", () => {
    const e = errorFromResponse(429, '{"error":"rate_limited","retry_after":42}');
    expect(e).toMatchObject({ status: 429, code: "rate_limited", retryAfter: 42 });
    expect(errorFromResponse(409, '{"error":"active_connection","detail":"x"}')).toMatchObject({ code: "active_connection", detail: "x" });
  });

  it("falls back to the HTTP status for non-JSON bodies", () => {
    expect(errorFromResponse(502, "<html>Bad gateway</html>").code).toBe("http_502");
    expect(errorFromResponse(500, '"str"').code).toBe("http_500");
  });
});

describe("Api", () => {
  it("sends JSON bodies and parses JSON responses", async () => {
    const calls: { path: string; init?: RequestInit }[] = [];
    const api = new Api({ fetch: fakeFetch(202, '{"ok":true}', calls) });
    await expect(api.wifiConnect("HomeNet", "secret123")).resolves.toEqual({ ok: true });
    expect(calls[0].path).toBe("/api/wifi/connect");
    expect(calls[0].init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ ssid: "HomeNet", password: "secret123" });
    expect(calls[0].init?.headers).toEqual({ "Content-Type": "application/json" });
  });

  it("adds ?rescan=1 only when asked", async () => {
    const calls: { path: string }[] = [];
    const api = new Api({ fetch: fakeFetch(200, '{"networks":[],"saved":[]}', calls) });
    await api.wifiNetworks(false);
    await api.wifiNetworks(true);
    expect(calls.map((c) => c.path)).toEqual(["/api/wifi/networks", "/api/wifi/networks?rescan=1"]);
  });

  it("calls onUnauthorized on 401 for protected endpoints", async () => {
    const hook = vi.fn();
    const api = new Api({ fetch: fakeFetch(401, '{"error":"unauthorized"}'), onUnauthorized: hook });
    await expect(api.status()).rejects.toMatchObject({ status: 401, code: "unauthorized" });
    expect(hook).toHaveBeenCalledTimes(1);
  });

  it("does not call onUnauthorized for a wrong login password", async () => {
    const hook = vi.fn();
    const api = new Api({ fetch: fakeFetch(401, '{"error":"bad_password"}'), onUnauthorized: hook });
    await expect(api.login("x")).rejects.toMatchObject({ code: "bad_password" });
    expect(hook).not.toHaveBeenCalled();
  });

  it("maps fetch failures to code network", async () => {
    const api = new Api({ fetch: () => Promise.reject(new TypeError("Failed to fetch")) });
    const e = await api.session().catch((x: unknown) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect(e).toMatchObject({ status: 0, code: "network" });
  });
});
