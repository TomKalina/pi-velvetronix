# Záznam testů

Skutečně provedené testy. Datum 2026-10-19, Raspberry Pi Zero 2 W Rev 1.0, Raspberry Pi OS Lite
64-bit (Debian 13 trixie, kernel 6.18.50), domácí Wi-Fi 2,4 GHz, Mac (macOS 27) jako klient.

## Automatické testy (Mac)

| Sada | Příkaz | Výsledek |
|---|---|---|
| Go (vše, s race detektorem) | `go test -race ./...` | 11 balíčků OK |
| Web (protokol VOX, UI logika, presety, knoby) | `cd web && npm test` | 365 testů OK (protokol: všechny referenční vektory z docs/protocol.md §11, neplatné vstupy §11.7) |
| Instalační a root skripty v Debian kontejneru | `scripts/test-deploy.sh` | 185 + 32 kontrol OK (symlinky podstrčené daemonem, výpadek během aktualizace, idempotence instalace, polkit pravidlo) |
| Determinismus | `scripts/build.sh`, `scripts/release.sh` 2× | binárka i balíky bajtově shodné |

## Na zařízení

| Test | Postup | Výsledek |
|---|---|---|
| Identifikace desky | `/proc/device-tree/model`, `uname -a` | Raspberry Pi Zero 2 W Rev 1.0, aarch64 |
| První instalace | `scripts/install.sh` | služba aktivní, READY po vlastní kontrole, `systemd-analyze verify` čisté, `security` 3.5 OK |
| Opakovaná instalace | `scripts/install.sh` znovu | idempotentní, zachová Wi-Fi a tajemství |
| Přihlášení | `POST /api/login` správné/špatné heslo, bez cookie | 200 / 401 `bad_password` / 401 `unauthorized` |
| Cross-origin POST | POST s cizím `Origin` | 403 |
| Polkit | scan Wi-Fi přes API | striktní varianta (`system_unit`) na polkitd 126 **nefunguje** (NM: not authorized), proto pravidlo podle uživatele; pak scan 8 sítí OK |
| mDNS | `curl http://vox.local/healthz` | 200, alias `vox.local` i v režimu hotspotu |
| Aktualizace přes web | 0.1.0 → 0.1.1 (`scripts/dev-update.sh`) | nainstalováno, potvrzeno při prvním startu |
| Rollback: pád | balík, jehož binárka hned skončí `exit 1` | 3 nepotvrzené starty, návrat na 0.1.1 za ~15 s |
| Rollback: zaseknutí | balík, který nikdy nepošle READY | 3× timeout 60 s, návrat na 0.1.1 za 207 s |
| Odmítnutí špatných souborů | `.img.xz`, raw `.img`, zip, holá binárka, x86-64, cizí název, cizí arch, stejná verze, špatný sha256, `../` v taru, instalační balík | všech 11 odmítnuto se správným kódem, stav zůstal `idle` |
| Restart Pi | `systemctl reboot` | web zpět za ~35 s, služba startuje ~1 s |
| MIDI přes ALSA bez komba | `snd-virmidi` + `aconnect` smyčka, `cmd/fakeamp` na druhé straně | rawmidi I/O, identita `42 34 01`, stav `detected → connected` |
| UI end-to-end (headless Chrome) se simulátorem | přihlášení, načtení 8 programů, gain, výběr B1, uložení do A3 s potvrzením a kontrolou | vše OK, „Uloženo do A3 a ověřeno“; druhý prohlížeč odmítnut |
| Změny z ovladačů komba | simulátor posílá `41 04 00 vv 00` každé 3 s | posuvník v UI se sám posouvá |
| **Skutečné kombo VT100X** | uživatel připojil kombo přes OTG adaptér | USB `0944:0132` „VOX Amplification Ltd. / Valvetronix X“, full-speed, USB-MIDI 1.0 (`snd-usb-audio`), daemon ho sám našel, identita `F0 7E 00 06 02 42 34 01 00 00 01 00 01 00 F7`; uživatel potvrdil, že načtení programů i ovládání fungují |
| Odpojení komba | kabel/kombo odpojeno | kernel `USB disconnect`, daemon `no such device` → stav „odpojeno“ |
| Aktualizace přes web s novým rozložením (root-only `/var/lib/pi-velvetronix-update`) | 0.2.0 → 0.3.0 (`scripts/dev-update.sh`) | nainstalováno, boot-guard pokus 1, potvrzeno přes `state/confirmed`; po restartu kombo znovu nalezeno a identifikováno |
| Hotspot na požádání | `POST /api/wifi/hotspot {"minutes":2}` | hotspot nahoře za 2 s, po 2 min návrat do domácí sítě |
| Klient na hotspotu | Mac se připojil k `VOX-Velvetronix` | s výchozím PMF se **nepřipojil**; s `pmf=1` (vypnuto) DHCP 10.42.0.60, web, přihlášení, `mode: ap` |
| Výpadek domácí sítě | `nmcli connection down` profilu domácí sítě | ztráta 11:46:16 → hotspot 11:47:51 (95 s) → po 5 min bez klientů návrat do domácí sítě 11:52:57; uložený profil beze změny |

## Neprovedeno / omezení

- Ukládání do slotu na **skutečném** kombu a přežití vypnutí komba: ověřeno jen se simulátorem
  (uživatel potvrdil ovládání, ne výslovně uložení a cyklus napájení).
- Chování komba u neověřených zpráv z protocol.md „Still unverified“ (např. `11`, `40`, presety).
- Brum/šum při připojeném Pi, zpětné napájení VBUS (měření multimetrem).
- Tepelné chování v krabičce u komba.
- Hotspot s telefony (iOS/Android), jen Mac.
- Grafický režim a záložka Písničky (0.3.0) ověřené jen proti simulátoru v prohlížeči
  (`npm run preview`), ne se skutečným kombem. Presety jsou přibližné.
