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
| `GET /api/devices` | — | `DeviceView[]` | Read-only; uses stored credentials |
| `GET /api/inspect` | — | `InspectReport` | Protocol diagnostic |
| `POST /api/login` | `{username,password,test?}` | `{status,stored,adapter,router_id}` | `test:true` only checks; never echoes the password |
| `PATCH /api/devices/{mac}` | `{custom_name}` | `{mac,custom_name}` | Local rename |
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
| `404` | device not found in the inventory |
| `422` | router model not identified (pass `--adapter`) / unsupported firmware |
| `501` | operation not implemented for this model yet (e.g. a write path not wired/verified) |
| `502` | router unreachable |
| `500` | unexpected error |

Error bodies: `{"error":"<message>"}`.

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
