# ZTE ZXHN F660 (Orange Sénégal "Fiberbox")

- Adapter id: `zte_f660`
- Status: **reference adapter** — read path implemented, endpoints `UNVERIFIED`
- Source of truth: [`../../RESEARCH.md`](../../RESEARCH.md)

## Interface
- URL: `http://192.168.1.1`
- Default credentials: commonly `admin` / `admin` (some firmware `admin` / `Web@0063`).

## Authentication (UNVERIFIED)
- `GET /` → login page carrying a hidden `Frm_Logintoken`.
- `POST /` with `Username`, `Password`, `Frm_Logintoken`, `action=login`, `_lang`.
- Session established via the `SID` cookie.
- Password mode: `plain` by default; newer firmware may need `sha256` /
  `sha256_token` — switch `PasswordMode` in `profile.go`.

## Endpoints (UNVERIFIED — candidates in `profile.go`)
| Operation | Method | Path |
|---|---|---|
| login | POST | `/` |
| router info | GET | `/getpage.gch?pid=1002&nextpage=status_device_info_t.gch` |
| devices | GET | `/getpage.gch?pid=1002&nextpage=net_lan_status_t.gch` |
| access rules | GET | `/getpage.gch?pid=1002&nextpage=net_wlan_acl_t.gch` |
| block / unblock | POST | `/setpage.gch` (documented only; not implemented in M1) |

## Data format (UNVERIFIED)
Tabular data embedded as JavaScript arrays:
- `_lan_host_N = Array(hostname, ip, mac, active)`
- `_wlan_assoc_N = Array(mac, ssid)`
- `_acl_mode` + `_acl_N = Array(mac, action)`

Devices are merged by **MAC** across the LAN host and WLAN associated tables.

## Limitations / firmware differences
Exact `.gch` ids, field names and the password hashing vary by firmware. Confirm
with the capture loop in [`../reverse-engineering/README.md`](../reverse-engineering/README.md),
then set `Verified: true` in `profile.go`.

## Captures
Place sanitised captures in `internal/fixtures/zte_f660/` (no real credentials,
cookies or MACs).
