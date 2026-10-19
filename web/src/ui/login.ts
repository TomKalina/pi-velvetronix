// Login screen.

import type { Api } from "../api";
import { h } from "./dom";
import { loginErrorText } from "./errors";

export function createLogin(api: Api, opts: { configured: boolean; message?: string; onSuccess(): void }): HTMLElement {
  const pw = h("input", { type: "password", id: "login-pw", autocomplete: "current-password", required: true, autofocus: true });
  const err = h("p", { class: "msg msg-err", role: "alert", hidden: !opts.message }, opts.message ?? "");
  const btn = h("button", { type: "submit", class: "primary" }, "Přihlásit");

  const form = h(
    "form",
    {
      class: "card login",
      onsubmit: async (e: Event) => {
        e.preventDefault();
        btn.disabled = true;
        err.hidden = true;
        try {
          await api.login(pw.value);
          pw.value = "";
          opts.onSuccess();
        } catch (ex) {
          err.textContent = loginErrorText(ex);
          err.hidden = false;
          pw.select();
        } finally {
          btn.disabled = false;
        }
      },
    },
    h("h1", null, "VOX Velvetronix"),
    h("p", { class: "muted" }, "Dálkové ovládání komba VOX Valvetronix X."),
    !opts.configured &&
      h("p", { class: "msg msg-warn" }, "Heslo není nastavené. Nastavte ho na zařízení příkazem „pi-velvetronix set-password“."),
    h("label", { htmlFor: "login-pw" }, "Heslo"),
    pw,
    err,
    btn,
  );
  return h("main", { class: "login-wrap" }, form);
}
