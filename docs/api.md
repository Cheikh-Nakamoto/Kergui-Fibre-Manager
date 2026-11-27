# REST API reference

Started by `kergui serve` (default `127.0.0.1:8080`). The server also serves the
web dashboard at `/`. It is configured with a single target router (`--router`,
`--adapter`); credentials come from the encrypted vault (set via `login` or
`POST /api/login`) and are **never returned** to clients.

Base path: `/api`. Bodies and responses are JSON.

## Endpoints

| Method & path | Body | Success | Notes |
|---|---|---|---|
| `GET /api/health` | — | `{status,version,router,adapter}` | Liveness + configured target |
| `GET /api/discover` | — | `RouterInfo` | Non-destructive fingerprint |
| `GET /api/devices` | — | `DeviceView[]` | Uses stored credentials. Reads the devices; afterwards, once a day, purges the stale rules kergui wrote (RFC-007 D6), and at most every 5 min switches the box's MAC filter back on, in blocklist mode (`Discard`), when it is off while a rule kergui wrote is on the box (#115, journal « Kergui a rallumé les blocages de la box »). Neither ever fails the list |
| `GET /api/inspect` | — | `InspectReport` | Protocol diagnostic |
| `POST /api/login` | `{username,password,test?}` | `{status,stored,adapter,router_id}` | `test:true` only checks; never echoes the password. `409 password_change_required` when a new box demands a new admin password: nothing is stored |
| `POST /api/box/first-password` | `{username,password,newPassword}` | `{status,stored,adapter,router_id}` | After `409 password_change_required`: sets `newPassword` on the box (same username), signs in with it and stores it (never the label password). `400 weak_password` before the box is asked (≥ 8 chars, a lower-case letter, an upper-case letter, a digit, printable ASCII); `422 password_rejected` with the box's message; `400` when the box does not ask. See [routers/zte-f6600p.md](routers/zte-f6600p.md) |
| `PATCH /api/devices/{mac}` | `{custom_name}` | `{mac,custom_name}` | Local rename, stamped with the time of the edit (`LabelAt` in `GET /api/devices`) |
| `GET /api/labels` | — | `{labels:[{mac,name,notes,at}]}` | The household's names and notes, with when each was set (#95). Never touches the box |
| `POST /api/labels` | `{labels:[{mac,name?,notes?,at}]}` | `{changed:[mac]}` | Labels made elsewhere (the owner's phone, the relay): each one replaces the stored label only if its `at` is later (last write wins); a missing `name`/`notes` is left as is; an unknown device is added offline. `400` for an unknown field, a bad MAC, a missing `at`, more than 2000 labels or a text over 500 characters. On the relay's allow-list (owner or control delegation) |
| `POST /api/devices/{mac}/block` | — | `{mac,status:"blocked"}` | Write; gated by `WriteReady` (UNVERIFIED on hardware) |
| `POST /api/devices/{mac}/unblock` | — | `{mac,status:"unblocked"}` | Write; gated by `WriteReady` (UNVERIFIED on hardware) |
| `GET /api/wifi/ssids` | — | `WiFiSSID[]` | Every Wi-Fi network of the router, enabled or not |
| `PATCH /api/wifi/ssids/{id}` | `{enabled?,name?,password?,hidden?}` | `WiFiSSID` | Switch a network on/off, rename it, set its WPA password; `400` for a locked network or the last enabled one |

## Status codes

| Code | When |
|---|---|
| `200` | success |
| `400` | invalid MAC or JSON body |
| `401` | authentication failed / session expired (run `login` first) |
| `409` | the box demands a new admin password (`code: password_change_required`) |
| `404` | device not found in the inventory |
| `422` | router model not identified (pass `--adapter`) / unsupported firmware / the box refused the new password (`code: password_rejected`) |
| `501` | operation not implemented for this model yet (e.g. a write path not wired/verified) |
| `502` | router unreachable |
| `500` | unexpected error |

Error bodies: `{"error":"<message>"}`, plus a stable `"code"` for the errors the
app answers with a screen of its own: `not_entitled` (403), `auth_failed` (401),
`password_change_required` (409), `weak_password` (400), `password_rejected` (422).

## Object shapes

`RouterInfo`: `BaseURL, Vendor, Model, Firmware, HardwareVer, HTTPServer, Title,
AdapterID, Confidence, Reachable, DiscoveredAt`.

`DeviceView` (a device plus inventory metadata): `ID, MAC, IP, Hostname,
CustomName, Vendor, SSID, FirstSeen, LastSeen, Connected, Blocked, RouterID,
Notes, NewlySeen`. `MAC` is canonical lowercase (`aa:bb:cc:dd:ee:ff`); `IP` is a
string (empty if none).

`WiFiSSID`: `id` (e.g. `DEV.WIFI.AP3`), `alias` (`SSID3`), `band` (`2.4GHz`/`5GHz`),
`name`, `enabled`, `hidden`, `security` (e.g. `WPA2/WPA3-SAE`), `locked` (why the
router refuses changes, e.g. the 5 GHz twin under band steering; absent otherwise).
The password is write-only: it is never returned.

`InspectReport`: `Info (RouterInfo), Meta (adapter + endpoint table with per-row
Verified flags), AuthOK, DeviceCount, AccessRuleCount, Warnings[]`.

## Examples

```sh
curl -s http://127.0.0.1:8080/api/health
curl -s http://127.0.0.1:8080/api/devices | jq '.[] | {name:.CustomName, mac:.MAC, blocked:.Blocked}'

# turn on a second Wi-Fi network with its own name and password
curl -s -X PATCH http://127.0.0.1:8080/api/wifi/ssids/DEV.WIFI.AP3 \
  -d '{"enabled":true,"name":"Kergui-Clients","password":"********"}'

# store credentials (stays server-side, encrypted)
curl -s -X POST http://127.0.0.1:8080/api/login \
  -d '{"username":"admin","password":"***"}'

# rename locally
curl -s -X PATCH http://127.0.0.1:8080/api/devices/aa:bb:cc:dd:ee:ff \
  -d '{"custom_name":"Téléphone Maman"}'

# block (200 once logged in; 501 if the model's write path is not wired)
curl -s -X POST http://127.0.0.1:8080/api/devices/aa:bb:cc:dd:ee:ff/block
```
