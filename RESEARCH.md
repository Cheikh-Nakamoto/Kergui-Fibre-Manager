# RESEARCH — Orange Sénégal router protocols

> **Status legend:** `VERIFIED` = confirmed on a real device via a capture in this
> repo. `UNVERIFIED` = documented hypothesis compiled from public sources, **not**
> yet confirmed. As of this milestone **everything is `UNVERIFIED`** — the code
> was developed in a cloud environment with no access to a real router. The
> `kergui discover`/`inspect` tools plus the capture guide
> ([`docs/reverse-engineering/README.md`](docs/reverse-engineering/README.md)) are
> how these get confirmed.

> **Honesty note.** Some primary sources (`assistance.orange.sn`, `www.orange.sn`,
> `fcc.report`) were not directly reachable from the build environment (egress
> policy). Their substance is summarised here from search results and corroborating
> ZTE material and must be confirmed locally.

This project's rule (brief §Règle fondamentale): *never assume how the router
works — observe, capture, understand, document, then build the adapter.*

---

## Supported routers (target set)

| Brand (Orange SN) | Model | Adapter id | Read path | Endpoints |
|---|---|---|---|---|
| Fiberbox | ZTE ZXHN **F660** | `zte_f660` | implemented | `UNVERIFIED` |
| (fibre) | ZTE ZXHN **F680** | `zte_f680` | skeleton | `UNVERIFIED` |
| Funbox | ZTE **Funbox** | `zte_funbox` | skeleton | `UNVERIFIED` |

> "Keurgui Box" / "Flybox" is a **4G CPE** (a different device class and web UI),
> not a fibre box; it is out of scope for the fibre-focused adapters above.

---

## ZTE ZXHN F660 (reference adapter)

### Discovery
- Web UI at `http://192.168.1.1` (brief + Orange SN assistance).
- Login page `<title>` and body typically carry `ZXHN`/`F660` and a hidden
  `Frm_Logintoken` field — used by `kergui discover` to fingerprint the model.
- `Server` header is often a small embedded HTTPd (e.g. `mini_httpd`). `UNVERIFIED`.

### Authentication — `UNVERIFIED`
- Method: form login establishing a **session cookie** (commonly `SID`).
- Flow (hypothesis):
  1. `GET /` → login page carrying `Frm_Logintoken`.
  2. `POST /` with fields `Username`, `Password`, `Frm_Logintoken`,
     `action=login` (+ locale fields such as `_lang`).
  3. Success sets the session cookie; subsequent pages require it.
- **Password handling varies by firmware**: older F660 firmware accepts the
  plaintext password; newer ZXHN firmware hashes it (SHA256, sometimes combined
  with the login token). The adapter's `PasswordMode` selects this; default
  `plain`. This is the single most likely thing to differ on real hardware.

### Device discovery — `UNVERIFIED`
- Devices are read from two places and merged by **MAC**:
  - WLAN *associated devices* (`Réseau → WLAN → Appareils associés`) → MAC (+ SSID).
  - LAN host / DHCP client table → hostname, IP, MAC.
- ZTE ZXHN pages commonly embed this data as **JavaScript arrays** inside the
  page (e.g. `var _lan_host_0 = new Array("name","ip","mac","active")`), fetched
  via `getpage.gch?...&nextpage=<page>.gch`. The adapter parses that shape.

### Access control — `UNVERIFIED`
- `Réseau → WLAN → Liste de contrôle d'accès`, with modes **Bloc** / **Autorisé**
  (i.e. Black List / White List MAC filtering, as Orange SN documents).
- Parsed into `AccessRule{MAC, Mode}` — kept **separate** from the device
  inventory (brief §18).

### Block / Unblock — `UNVERIFIED`, **not implemented in Milestone 1**
- Expected to be a `POST` (e.g. `setpage.gch`) adding/removing a MAC on the ACL.
- Documented only; the adapter returns `ErrNotImplemented` and the CLI never
  executes a write. This is deferred to Milestone 2 behind explicit confirmation.

### Limitations
- Exact `.gch` page ids, field names, and the password hashing **will** vary by
  firmware and must be confirmed per device.
- No official public API; the web UI is the only integration surface.

### Firmware differences
- Plaintext vs. SHA256(+token) password is the key axis. The token-driven login
  path adapts; `PasswordMode` is the switch.

### Implementation strategy
- A single profile-driven ZTE gateway (`internal/adapter/router/zte`) with a
  per-model `Profile` holding every endpoint/field, so corrections after a real
  capture are a one-file change. Parsers are validated against synthetic fixtures
  and the in-process mock; real captures replace the fixtures to validate against
  reality.

---

## ZTE ZXHN F680 (skeleton)
Same ZXHN family; the public `ZTE-F680-API` client documents a 3-step login with
**SHA256 password + one-time token** and cookie sessions. The profile encodes
`PasswordSHA256Token`; data-page parsers await a capture (`ReadReady=false`).

## ZTE Funbox (skeleton)
Orange's ZTE "Funbox"; Orange SN documents the same Black List / White List MAC
filtering. Profile stubbed; parsers await a capture.

---

## Sources
- Orange Sénégal assistance — MAC filtering / connected devices (Fiberbox & Funbox):
  - https://assistance.orange.sn/questions/2440720-fibre-optique-orange-senegal-filtrage-mac-gerer-equipements-connectent-wifi
  - https://assistance.orange.sn/questions/2440712-modem-fun-box-orange-senegal-filtrage-mac-gerer-equipements-connectent-wifi
  - https://assistance.orange.sn/questions/2343543-wifi-verifier-gerer-equipements-connectes-wifi
- Orange SN tutorial — verifying connected devices:
  https://www.orange.sn/assistance/tutoriels/comment-verifier-les-equipements-connectes-au-wifi-adsl
- ZTE ZXA10 F660 user manual (Associated Device / Access Control List):
  https://fcc.report/FCC-ID/Q78-ZXA10F660/1557814.pdf
- Legitimate open-source ZTE web-API clients (login/session/data-page patterns):
  - https://github.com/denis0001-dev/ZTE-F680-API
  - https://github.com/juacas/zte_tracker

> Exploit/backdoor material (e.g. routersploit modules) is deliberately **excluded**:
> this project only performs legitimate administration of a router you own and
> never bypasses authentication.
