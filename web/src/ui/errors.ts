// Mapping of server and protocol error codes to Czech messages (no DOM).

import { ApiError } from "../api";
import { VoxError } from "../protocol/client";

const NETWORK = "Zařízení neodpovídá. Zkontrolujte připojení k Wi-Fi.";
const EXPIRED = "Přihlášení vypršelo, přihlaste se znovu.";

export function loginErrorText(e: unknown): string {
  if (!(e instanceof ApiError)) return "Přihlášení selhalo.";
  switch (e.code) {
    case "bad_password": return "Špatné heslo.";
    case "rate_limited": return `Příliš mnoho pokusů – zkuste za ${e.retryAfter ?? 60} s.`;
    case "no_password": return "Heslo není nastavené. Nastavte ho na zařízení příkazem „pi-velvetronix set-password“.";
    case "network": return NETWORK;
    default: return `Přihlášení selhalo (${e.code}).`;
  }
}

const UPDATE_ERRORS: Record<string, string> = {
  too_large: "Soubor je příliš velký (nejvýš 40 MiB).",
  not_gzip: "Soubor není balíček .tar.gz. Nahrajte pi-velvetronix-<verze>-linux-arm64.tar.gz, ne obraz disku (.img, .xz) ani .zip.",
  bad_tar: "Archiv je poškozený nebo to není tar.",
  unexpected_entry: "Balíček obsahuje nečekaný soubor (smí mít jen manifest.json a pi-velvetronix).",
  missing_entry: "V balíčku chybí manifest.json nebo program pi-velvetronix.",
  bad_manifest: "Soubor manifest.json v balíčku je neplatný.",
  wrong_name: "Balíček není pro pi-velvetronix.",
  wrong_arch: "Balíček je pro jinou architekturu (potřeba linux/arm64).",
  bad_version: "Balíček má neplatné číslo verze.",
  same_version: "Tato verze už je nainstalovaná.",
  sha256_mismatch: "Kontrolní součet programu nesouhlasí, soubor je poškozený.",
  not_elf: "Program v balíčku není spustitelný soubor pro Linux.",
  not_aarch64: "Program v balíčku není pro Raspberry Pi (arm64).",
  busy: "Jiná aktualizace už probíhá.",
  io_error: "Chyba zápisu na zařízení.",
};

export function updateErrorText(e: unknown): string {
  if (!(e instanceof ApiError)) return "Nahrání selhalo.";
  if (e.code === "network") return "Spojení se zařízením se během nahrávání přerušilo.";
  if (e.code === "unauthorized") return EXPIRED;
  return UPDATE_ERRORS[e.code] ?? `Aktualizace selhala (${e.code}).`;
}

export function wifiErrorText(e: unknown): string {
  if (!(e instanceof ApiError)) return "Operace selhala.";
  switch (e.code) {
    case "active_connection": return "Síť, ke které je zařízení právě připojené, nelze zapomenout.";
    case "network": return NETWORK;
    case "unauthorized": return EXPIRED;
    default: return e.detail ? `Operace selhala: ${e.detail}` : `Operace selhala (${e.code}).`;
  }
}

/** last_error from Status.wifi, e.g. "connect_failed: HomeNet". */
export function wifiLastErrorText(err: string): string {
  const m = /^connect_failed:\s*(.*)$/.exec(err);
  if (m) return `Připojení k síti „${m[1]}“ se nepovedlo (špatné heslo nebo slabý signál).`;
  return err;
}

/** BridgeError (design §7) and VoxError codes (protocol/client.ts). */
const AMP_ERRORS: Record<string, string> = {
  bad_frame: "Zařízení odmítlo neplatnou zprávu.",
  forbidden: "Zařízení tento příkaz nepovoluje.",
  save_flag_required: "Zápis do paměti komba je povolen jen tlačítkem Uložit.",
  amp_disconnected: "Kombo není připojené.",
  queue_full: "Příliš mnoho příkazů najednou, zkuste to znovu.",
  updating: "Probíhá aktualizace, ovládání je pozastavené.",
  write_failed: "Zápis do USB selhal.",
  timeout: "Kombo neodpovídá.",
  nak: "Kombo příkaz odmítlo.",
  format_error: "Kombo hlásí chybu ve zprávě.",
  reset: "Spojení s ovladačem se přerušilo.",
  send_failed: "Spojení s ovladačem nefunguje.",
  busy: "Jiný zvuk se právě nastavuje, počkejte na dokončení.",
};

export function ampErrorText(codeOrError: unknown): string {
  let code: string;
  if (codeOrError instanceof VoxError) code = codeOrError.code;
  else if (typeof codeOrError === "string") code = codeOrError;
  else if (codeOrError instanceof RangeError) return `Neplatná hodnota: ${codeOrError.message}`;
  else return "Neznámá chyba.";
  return AMP_ERRORS[code] ?? `Chyba komunikace s kombem (${code}).`;
}

/** UpdateInfo.last_result, e.g. "installed 0.1.1", "rolled-back 0.1.2 -> releases/0.1.1", "failed sha256". */
export function lastResultText(r: string | null): string {
  if (!r) return "žádná";
  const [word, ...rest] = r.split(" ");
  const tail = rest.join(" ");
  switch (word) {
    case "installed": return `nainstalováno ${tail}`;
    case "rolled-back": return `nová verze nenaběhla, vráceno zpět (${tail})`;
    case "failed": return `instalace selhala (${tail})`;
    default: return r;
  }
}
