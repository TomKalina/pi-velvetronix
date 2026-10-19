import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import { VoxError } from "../protocol/client";
import { ampErrorText, lastResultText, loginErrorText, updateErrorText, wifiErrorText, wifiLastErrorText } from "./errors";

const UPDATE_CODES = [
  "too_large", "not_gzip", "bad_tar", "unexpected_entry", "missing_entry", "bad_manifest", "wrong_name", "wrong_arch",
  "bad_version", "same_version", "sha256_mismatch", "not_elf", "not_aarch64", "busy", "io_error",
];
const BRIDGE_CODES = ["bad_frame", "forbidden", "save_flag_required", "amp_disconnected", "queue_full", "updating", "write_failed"];

describe("loginErrorText", () => {
  it("maps the login errors", () => {
    expect(loginErrorText(new ApiError(401, "bad_password"))).toBe("Špatné heslo.");
    expect(loginErrorText(new ApiError(429, "rate_limited", undefined, 30))).toBe("Příliš mnoho pokusů – zkuste za 30 s.");
    expect(loginErrorText(new ApiError(503, "no_password"))).toMatch(/^Heslo není nastavené/);
    expect(loginErrorText(new ApiError(0, "network"))).toMatch(/neodpovídá/);
  });
});

describe("updateErrorText", () => {
  it("has a Czech message for every design §5 update code", () => {
    for (const code of UPDATE_CODES) {
      const t = updateErrorText(new ApiError(400, code));
      expect(t, code).not.toContain(code);
    }
    expect(updateErrorText(new ApiError(400, "not_gzip"))).toContain(".tar.gz");
  });

  it("keeps unknown codes visible", () => {
    expect(updateErrorText(new ApiError(400, "weird"))).toContain("weird");
  });
});

describe("ampErrorText", () => {
  it("covers every BridgeError and VoxError code", () => {
    for (const code of [...BRIDGE_CODES, "timeout", "nak", "format_error", "reset", "send_failed"]) {
      expect(ampErrorText(code), code).not.toContain(code);
      expect(ampErrorText(new VoxError(code))).toBe(ampErrorText(code));
    }
  });

  it("reports encoder range errors", () => {
    expect(ampErrorText(new RangeError("nr out of range 0..100: 120"))).toContain("nr out of range");
  });
});

describe("Wi-Fi and update results", () => {
  it("maps Wi-Fi errors", () => {
    expect(wifiErrorText(new ApiError(409, "active_connection"))).toMatch(/nelze zapomenout/);
    expect(wifiLastErrorText("connect_failed: HomeNet")).toContain("„HomeNet“");
    expect(wifiLastErrorText("other")).toBe("other");
  });

  it("describes last_result", () => {
    expect(lastResultText(null)).toBe("žádná");
    expect(lastResultText("installed 0.1.1")).toBe("nainstalováno 0.1.1");
    expect(lastResultText("rolled-back 0.1.2 -> releases/0.1.1")).toBe("nová verze nenaběhla, vráceno zpět (0.1.2 -> releases/0.1.1)");
    expect(lastResultText("failed sha256")).toBe("instalace selhala (sha256)");
  });
});
