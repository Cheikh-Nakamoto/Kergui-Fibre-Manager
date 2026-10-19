# RFC-005 — Semi-distributed hub (v2)

- Status: **Accepted** — H0 implemented (`cmd/kergui-hub`, `internal/hub`)
- Supersedes: [RFC-003](RFC-003-mobile-app.md) for the v2 hub. RFC-003's server-on-the-LAN design is
  replaced by a central hub plus relays.
- Builds on: [RFC-004](RFC-004-standalone-mobile-app.md). The v1 stand-alone app keeps working unchanged
  without a hub account.
- Kernel: [github.com/agile-factory/kernel](https://github.com/agile-factory/kernel)
  (docs: https://kernel-docs.cheikhmodiouf.org/)

## Context

The v1 app only reaches the router from the home Wi-Fi. v2 adds remote use, centred on the
household's owner:

- see the connected devices;
- a **WhatsApp alert** when a new device joins;
- block it;
- cut the Internet;
- turn another SSID on;
- see the consumption.

All of this works from anywhere, as long as **one phone of the household stays on the home
Wi-Fi** with the app connected to the hub. The owner can keep router access to themself; the
other phones then only act as **relays** when the owner is away.

Constraints:

| # | Constraint |
|---|---|
| C1 | **Nothing can reach the box from outside.** The F6600P is on DS-Lite (no public IPv4, no VPN server), so relays dial out. |
| C2 | **The box allows one admin session.** A new login evicts the previous one. |
| C3 | **The audience** (Senegal) is mostly non-technical and often has no PC at home. Everything is set up from the app; the default relay is a phone. |
| C4 | **The app is complete on Android and iOS.** Only *background* relaying differs (see Relays). |
| C5 | **Keep the core's rules:** credentials never returned to a UI nor logged, writes confirmed and read back, endpoints `UNVERIFIED` until proven. |
| C6 | **The ZTE F6600P is the reference box**, and the whole app already runs on it. The hub and the relay stay adapter-agnostic: they speak the `httpapi` JSON API. |
| C7 | **The hub is included in the app price.** No in-app purchase; the kernel's billing module is out of scope. |

## Decision

```
 Owner on 4G (app)              WhatsApp (alert + reply "1" = block)
        │ HTTPS + kernel JWT              ▲│
        ▼                                 │▼
 ┌──────────── Hub (VPS, Postgres) ──────────────────────┐
 │ kernel: auth · tenant · membership · audit · events  │
 │ hub: agents · relay · inventory · alerts · routerapi │
 └────────────▲──────────────────────────────────────────┘
              │ outbound HTTPS long-poll (the relay calls the hub)
   ┌──────────┴───────────┐   one active relay per household (lease)
   │ Active relay on LAN  │   Android: foreground service + mobilecore
   │                      │   iPhone: app open; background via Local Push Connectivity
   │                      │   optional: dedicated phone, or PC (kergui serve --hub)
   └──────────┬───────────┘
              │ HTTPS on the LAN, pinned certificate (v1)
         ZTE F6600P
```

A relay runs commands through the **same in-memory `httpapi` handler** as
`mobilecore.Core.Request`. A remote command is therefore `{method, path, body}` → `{status, body}`;
no new RPC surface.

### Hub = kernel + hub modules, Postgres only

`cmd/kergui-hub` composes the kernel modules (`internal/hub/app`) and, from H1, the hub modules.

- **Kernel modules used:**

  | Module | Role in the hub |
  |---|---|
  | `auth` | owner signup also creates the tenant; login; rotating refresh |
  | `tenant` | a household = a tenant, with JSONB settings |
  | `membership` | e-mail invites; roles `ROLE_OWNER/ADMIN/VIEWER` |
  | `audit` | who blocked what |
  | `events` | transactional outbox, at least once |
  | `notification` | e-mail |
  | `ratelimit`, `health`, middlewares | HTTP plumbing |

- **Not in the kernel, built in the hub:**
  - device tokens (`platform/apikey` is an empty README);
  - WhatsApp (the kernel only has e-mail, and an SMS port for B2B2C);
  - relaying;
  - device inventory;
  - `router:*` permissions (the kernel's role map is unexported).
- **The kernel needs Postgres (pgx).** It is linked only into `cmd/kergui-hub` and `internal/hub/...`;
  `tests/architecture_test.go` fails if the CLI, the LAN server, `mobilecore` or any other internal
  package pulls it in.

### Relays

- **Android:** relays continuously from a foreground service. `dataSync` is capped at 6 h / 24 h from
  Android 15, so the service uses `connectedDevice`.
- **iPhone:** relays while the app is open. iOS suspends apps a few seconds after they leave the
  screen and has no foreground-service equivalent.
- **iPhone in the background:** `NEAppPushProvider` (Local Push Connectivity), an extension iOS starts
  only on the configured SSIDs. Its entitlement must be granted by Apple; a spike is planned for H3.
- **Rejected for iPhone background relaying:**
  - a VPN extension: Apple reserves VPN apps for organisation accounts (guideline 5.4), and the VPN icon
    would show permanently;
  - silent pushes and `BGAppRefreshTask`: too throttled for alerts.
- **Optional:** a dedicated phone left plugged in at home, or `kergui serve --hub` on a PC. The PC
  relay is also the integration test bench.

### Design rules

1. **One router session per household.**
   - The hub grants a 60 s lease, renewed by a heartbeat every 20 s, to one eligible relay.
   - Priority: dedicated relay, then the owner's phone, then the others.
   - Standby relays never log in to the box.
   - With a hub account, the app always goes through the hub, even on the LAN.
2. **The relay must be on the right network.**
   - The Wi-Fi signature (SSID + BSSIDs) registered by the owner must match the relay's current
     connection.
   - Second check: the box identity, i.e. the TLS pin or, failing that, `RouterInfo` serial/MAC.
   - The relay binds its process to that Wi-Fi (`bindProcessToNetwork`).
   - On Android, reading SSID/BSSID requires the location permission. If it is refused, only the box
     identity is checked, and the app says so.
3. **The hub transports, the owner decides** (see Trust model).
4. **Relay-side allow-list.**
   - Only `devices`, `block`/`unblock`, `wifi-block`, `ssids`, `filter` and `usage` are reachable.
   - Never `POST /api/login` or `/api/discover` (SSRF on the LAN).
   - Commands expire within 60 s and their ids are never replayed.
5. **Never cut the relay.**
   - The relay finds its own MAC by matching its LAN IP in the device list.
   - "Cut Internet" and SSID changes always exclude relays and protected devices.
   - The SSID the active relay is on cannot be turned off.
6. **The hub owns the reference inventory.** The "new device" diff runs on the hub, after
   `domain.Recognise` has had a chance to recognise a phone with a rotated MAC. Alerts are grouped
   (2 min window).

### Trust model: owner ↔ relay keys

- **The owner is the root of trust.**
  - Keys are generated in Go: `crypto/ed25519` for signing, `golang.org/x/crypto/nacl/box` (X25519)
    for encryption.
  - They are stored in the existing AES-GCM vault and never cross into JS.
  - Who holds which keys:

    | Party | Keys |
    |---|---|
    | Owner | Ed25519 + X25519 |
    | Each relay | Ed25519 + X25519 |
    | Each member allowed to control | Ed25519 |

- **Pairing does not trust the hub:**
  1. The owner's QR carries the pairing code and the owner's key fingerprint.
  2. The relay pins that key and sends its own key through the hub.
  3. Both screens show the same 6-digit code, derived from both keys.
  4. The owner confirms and signs the relay in.

  A hub that swapped a key would make the two codes differ.
- **Trust bundle.** Signed by the owner, monotonic version. It lists:
  - the owner key;
  - the relays;
  - delegations (member key, `view`/`control`, 30-day expiry renewed by the owner's app);
  - revocations.

  With `owner_only`, no `control` delegation exists, so relays refuse members' commands even if the hub
  lets them through.
- **Signed commands.**
  - The signed payload is `{tenant_id, cmd_id, method, path, sha256(body), issued_at, expires_at, key_id}`.
  - The relay checks, in order: signature, delegation, expiry, replay, then the allow-list.
  - Results and snapshots are signed by the relay, so the hub cannot fake "blocked ✅" or a device list.
- **The box password.**
  - The owner types it once.
  - The owner's app seals it with `nacl/box` (owner → relay X25519) inside Go.
  - The relay checks that it came from the owner. The hub only ever stores ciphertext.
- **WhatsApp "1 = block".** The hub key holds a narrow delegation: `block` only, rate-limited, never on
  a relay or a protected device. It can be switched off.
- **Lost owner phone.**
  - Either a co-owner device signed in advance,
  - or, at home on the Wi-Fi, a challenge-response `HMAC(box password, nonce ‖ new key)` that the relay
    checks. Whoever knows the box password already controls the box.

### Relay protocol (stdlib HTTP)

| Endpoint | Purpose |
|---|---|
| `POST /relay/v1/pair/redeem` | QR code → `kga_…` token (stored as sha256), public keys |
| `POST /relay/v1/heartbeat` | `{wifi:{ssid,bssid}, router_identity, lan_ip, version}` → `{lease: active\|standby, ttl}` |
| `GET /relay/v1/commands?wait=25s` | lease holder long-polls signed commands |
| `POST /relay/v1/commands/{id}/result` | signed `{status, body}` |
| `POST /relay/v1/snapshots` | signed devices, traffic counters, SSIDs (every 60 s; SSIDs every 5 min) |
| `GET /relay/v1/credentials` | the sealed box password |

Long-poll instead of WebSocket: it survives network changes on phones, passes Cloudflare, and needs
only the standard library.

### Alerts

- **The scan is delegated to the active relay.** Alerts reach people whose app is closed.
- **Two channels:** WhatsApp, and app push behind a `Notifier` port.
- **WhatsApp opt-in, the user writes first.**
  - A QR code or `wa.me` link pre-fills `KERGUI <code>` to the Kergui number.
  - The inbound message verifies the number and opens the conversation.
- **Providers, behind one port:**
  - **Evolution Go** for the pilot: self-hosted, Go/whatsmeow, unofficial. The risk is a ban of a
    dedicated number. Its licence requires activation with a heartbeat and a visible "Evolution Go"
    notice.
  - **Meta Cloud API** before opening to paying customers: utility template with quick-reply buttons,
    signed webhooks.
- **Rejected: linking each pilot's personal WhatsApp.**
  - It moves the ban risk onto personal numbers.
  - A linked device decrypts *all* of that user's chats.
  - Messages to oneself do not notify.

## Kernel findings (pinned at `a4c4515`, the only tag is `v0.1.0`)

**Worked around in the hub.** Fixed upstream in
[agile-factory/kernel#63](https://github.com/agile-factory/kernel/pull/63); once it is merged, bump the
pin and delete `userrepo.go` and `kernelfix/`.

| Finding | Effect | Hub workaround |
|---|---|---|
| `PostgresUserRepository.FindByEmail/FindByID` scan the `email` column into `domain.Email`, a struct without `sql.Scanner` | Every login answers `INVALID_CREDENTIALS` | `internal/hub/app/userrepo.go` redoes both reads |
| `tenants.name` is `UNIQUE` platform-wide | A second household called "Maison Diallo" gets a 500 | `internal/hub/app/kernelfix/0001_*.sql` drops the constraint; the slug stays unique |
| `response.Error` adds the source file and line to errors unless `APP_ENV` is production/staging | Internal paths leak to clients | `KERGUI_HUB_ENV` defaults to production and sets `APP_ENV` |
| `ratelimit.IPKeyFn` trusts the leftmost `X-Forwarded-For` | The limit is bypassed with a forged header | The hub keys on `RemoteAddr`, or on `CF-Connecting-IP` behind Cloudflare |
| `config.Load` reads `.env` from the working directory and falls back to a known JWT secret | — | The hub has its own strict config (`internal/hub/config`) |
| The example `main.go` mounts tenant/membership routes without `middleware.Auth`, and handlers read the caller from the context | These routes always answer 401 | The hub routes everything but `/api/v1/auth/*` through `middleware.Auth` and does not expose `/admin/v1/*` |

**Behaviour to know:**

- Login requires a verified e-mail (status `ACTIVE`); signup alone returns usable tokens.
- The raw verification token sits in the `auth.signup_owner` outbox payload.
- JSON fields are camelCase (`accessToken`, `tenantId`), and `tenantSlug` is required at signup.
- The docs site (v1.2.0, snake_case, `Config{DB}`, `Mount`) does not match the code.

## Milestones

| | Content |
|---|---|
| **H0** ✅ | This RFC; `cmd/kergui-hub` on the pinned kernel; Postgres compose; strict config; Postgres integration tests (signup → verify → login → tenant → refresh rotation, isolation, rate limit); architecture rule; `hub-ci.yml`. Apple entitlement request (owner action). |
| **H1** | Mock F6600P; `internal/trust`; agents (QR, tokens, keys); relay (lease, queue, long-poll); `internal/agent`; `kergui serve --hub`; remote devices / block / unblock; audit. |
| **H2** | Snapshots, hub diff, `Recognise`, grouped alerts; outbox `router.device_first_seen`; Evolution Go + console adapters; `wa.me` opt-in; "1 = block"; push port. |
| **H3** | App: account, household, members, QR pairing with the 6-digit code, delegations, recovery, sealed credentials, Android relay service, iPhone foreground relay, Local Push spike, non-technical French onboarding, demo on mock F6600P. GitHub Actions only. |
| **H4** | Usage charts (`TrafficDelta`), cut / restore Internet, SSID on/off with guards (F6600P SSID writes validated on hardware first). |
| **H5** | Meta Cloud API; image built on GitHub Actions, VPS deploy; store privacy labels (data now collected), CDP declaration; Play foreground-service declaration. |

## Consequences

- The privacy promise of RFC-004 ("data not collected") holds only without a hub account. Store
  listings and the privacy policy change in H5.
- `go.mod` gains the kernel, pgx and golang-migrate. They are linked only into `kergui-hub`.
- Hub tests need Postgres: `make hub-test` (docker) locally, a service container in CI.
