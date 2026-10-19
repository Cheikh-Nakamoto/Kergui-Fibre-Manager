# RFC-005 — Semi-distributed hub (v2)

- Status: **Accepted** — H0, H1 and H2 implemented (`cmd/kergui-hub`, `internal/hub`,
  `internal/trust`, `internal/agent`)
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
| `POST /relay/v1/heartbeat` | `{wifi:{ssid,bssid}, router_identity, lan_ip, version, released}` → `{lease: active\|standby, epoch, ttl, retry_after, bundle_version, credential_version}` |
| `GET /relay/v1/trust` | the owner-signed trust bundle (H1) |
| `GET /relay/v1/commands?wait=25s` | lease holder long-polls signed commands |
| `POST /relay/v1/commands/{id}/result` | signed `{status, body}` |
| `POST /relay/v1/snapshots` | signed snapshots: `devices` and `observations` every 60 s, `ssids` every 5 min (H2) |
| `GET /relay/v1/credentials` | the sealed box password |

The app's side, under `/api/v1/tenants/{tenantId}/` with a kernel JWT:
`agents/pairing`, `agents`, `agents/{id}/credentials`, `network`, `trust-bundle`,
`router/devices`, `router/devices/{mac}/block|unblock`, `router/commands/{id}`, and from H2
`router/inventory`, `router/alerts/{id}`, `whatsapp`, `whatsapp/optin`, `hub-key`.

The messaging provider's inbound calls land on `POST /whatsapp/v1/inbound/{secret}`, outside both
surfaces: they carry neither a user's JWT nor an agent token.

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

## Kernel findings (pinned at `e3fb50d` on `main`; the only tag is still `v0.1.0`)

**Fixed upstream.** [#62](https://github.com/agile-factory/kernel/pull/62) and
[#63](https://github.com/agile-factory/kernel/pull/63) are merged, so the hub carries no correction to
kernel-owned code any more: `internal/hub/app/userrepo.go` and `kernelfix/` are gone.

| Finding | Effect | Upstream fix |
|---|---|---|
| `PostgresUserRepository.FindByEmail/FindByID` scanned the `email` column into `domain.Email`, a struct without `sql.Scanner` | Every login answered `INVALID_CREDENTIALS` | #62 scans it into a string |
| `tenants.name` was `UNIQUE` platform-wide | A second household called "Maison Diallo" got a 500 | #63 adds `tenant/0003_drop_tenant_name_unique`; the slug stays unique |
| `response.Error` added the source file and line to every error | Internal paths leaked to clients | #62 shows `debug` only when `APP_ENV` is `development` or `test` |

**Still worked around in the hub.** These are deliberate differences, not defects to fix upstream.

| Finding | Effect | Hub choice |
|---|---|---|
| `ratelimit.IPKeyFn` trusts the leftmost `X-Forwarded-For`; #62 adds `TrustedProxyIPKeyFn`, which takes a proxy list | The default is bypassed with a forged header | The hub keeps its own `clientIP`: `RemoteAddr`, or `CF-Connecting-IP` only when it sits behind Cloudflare |
| `response.Error` keys the debug field on `APP_ENV` | A misconfigured deployment would leak again | `KERGUI_HUB_ENV` defaults to production and sets `APP_ENV`; one setting, checked at start-up |
| `config.Load` reads `.env` from the working directory and falls back to a known JWT secret | — | The hub has its own strict config (`internal/hub/config`) |
| The example `main.go` mounts tenant/membership routes without `middleware.Auth`, and handlers read the caller from the context | These routes always answer 401 | The hub routes everything but `/api/v1/auth/*` through `middleware.Auth` and does not expose `/admin/v1/*` |

**Behaviour to know:**

- Login requires a verified e-mail (status `ACTIVE`); signup alone returns usable tokens.
- The raw verification token sits in the `auth.signup_owner` outbox payload.
- JSON fields are camelCase (`accessToken`, `tenantId`), and `tenantSlug` is required at signup.
- `ApplyGlobalMiddlewares` caps request bodies at 1 MiB (`middleware.MaxBytes`) and sets no write
  timeout, so the relay long-poll passes through it.
- Reusing a refresh token now revokes every session of that user and logs a warning.
- The docs site (v1.2.0, snake_case, `Config{DB}`, `Mount`) does not match the code.

## H1 as built

H1 is implemented: `internal/trust`, `internal/agent`, the hub modules `internal/hub/agents` and
`internal/hub/relay`, the F6600P mock, and `kergui hub enroll` / `kergui serve --hub`. The
end-to-end test (`internal/hub/e2e`) runs the whole path in one process — hub on Postgres, relay,
in-memory `httpapi`, mock F6600P behind TLS.

Seven places where the implementation departs from, or sharpens, what is written above. Each one
is a decision, not an accident.

| # | Departure | Why |
|---|---|---|
| 1 | `GET /relay/v1/trust` and `PUT …/trust-bundle` added | The protocol table never said how a signed bundle reaches a relay. The heartbeat now carries `bundle_version`, so the relay fetches one only when it changes. |
| 2 | A standby is told `retry_after` = min(heartbeat, time left on the lease) | With a 60 s lease and a 20 s beat, a dead holder could cost 80 s of silence. A clean shutdown sends `released: true` and frees the lease at once. |
| 3 | The lease carries an `epoch` | It is a fencing token: commands and results carry it, and the hub refuses a result from a relay that has since lost the lease. |
| 4 | Priority preemption is a hand-over, not a seizure | The lower-priority holder is told to stand down first; the better candidate takes the lease on its next beat. The box allows one session, so a gap is correct and an overlap never is. |
| 5 | The recovery proof stretches the box password with argon2id first | As written, `HMAC(box password, nonce ‖ key)` would let whoever saw the proof try passwords offline, and box passwords are weak. The exchange is also fixed to the LAN in H3, never through the hub. |
| 6 | A device snapshot (`POST /relay/v1/snapshots`, devices only) is pulled forward from H2 | `router/devices` has to serve a last known state with its `as_of`. The diff, `Recognise` and the alerts stay in H2. |
| 7 | A command's id is the `cmd_id` the owner signed | A separate hub-side row id meant the relay signed one id and the hub checked another. One identity also makes re-submitting the same signed command idempotent. |

Two things worth knowing for H2 and H3:

- **The box identity in H1 is the TLS pin.** `RouterInfo` exposes no serial yet; the pin is
  required and the Wi-Fi signature is checked only when the relay reports one (a PC on Ethernet,
  or Android with the location permission refused, reports none and the standby says so).
- **Discovery preferred the first matching adapter**, and `f660` is a prefix of `f6600p`, so a page
  naming the model in plain text was handed to the F660 adapter. It now prefers the longest
  matching marker.

## H2 as built

H2 is implemented: the relay reports everything one poll says, `internal/hub/inventory` keeps the
household's reference inventory and runs the diff, `internal/hub/alerts` sends the message,
`internal/hub/hubkey` derives the narrow key and `internal/hub/replyblock` turns a reply into one
signed command. The acceptance path runs end to end in `internal/hub/e2e`: a device joins the mock
F6600P, the relay signs what it saw, the hub finds it new, an alert goes out on the console
provider, the household answers "1", and the device is blocked on the mock.

Seven places where the implementation departs from, or sharpens, what is written above.

| # | Departure | Why |
|---|---|---|
| 1 | `Recognise`'s `Proposed` outcome raises no alert | A phone that comes back under a rotated MAC has corroborating evidence only, and the function never binds it alone. The hub records it as proposed for that device and stays silent; the owner confirms it in H3. Alerting on a device the household already has is how people learn to ignore alerts. |
| 2 | The first snapshot of a household is a baseline | Without it, pairing a relay would announce every phone in the house as a stranger. |
| 3 | Two read-only routes added to `httpapi`: `GET /api/observations` and `GET /api/traffic` | `router/devices` was not enough: the diff needs DHCP leases and radio capabilities, which only `port.HostObserver` exposes. Neither route is on the relay's allow-list — the hub cannot ask for them; the relay reads them for its own snapshots. An adapter without the capability answers 501 and the relay reports a `degraded` snapshot derived from the device list rather than nothing at all. |
| 4 | `trust.Relay` gained `MACs`, covered by `Bundle.IsProtected` | Only the relay holding the box protected itself (`Verifier.Protect`); a standby relay was blockable by the narrow key, and cutting the household's next relay is cutting its remote access. The relay publishes its own MAC in its signed snapshots and the owner's app copies it into the bundle. |
| 5 | The narrow key may only reach a device the relay has just seen | The hub composes the alert the household answers, so without this it could name an address that was never there. The relay re-reads the device list on that path (`ErrNotPresent`), and a list it cannot read is a refusal, not a shrug. |
| 6 | The rate limit exists twice | The relay's counter is in memory and starts again at every hand-over; the hub keeps its own in `hub_reply_actions`, with the same values (`trust.DefaultHubBlockLimit`). The relay's remains the one that decides. |
| 7 | The Evolution Go webhook is authenticated by a secret in its path | Evolution Go offers a `WEBHOOK_URL` and signs nothing, so there is no signature to check. The path secret is compared in constant time, the instance name in the payload is checked, and the route is rate-limited; the console adapter signs its inbound calls (HMAC-SHA256), which is what the tests exercise. Meta's signed webhooks arrive with Meta in H5. |

What the licence of Evolution Go requires, and what the hub does about it: the server answers 503
until its instance is registered in its Manager, and keeps its activation alive with its own
heartbeats, so a 503 is reported as `ErrNotActivated` (not a hiccup) and `Status` feeds the hub's
health. Its "usage notification" clause asks for a visible notice, reachable by administrators from
the settings or the documentation: the provider port carries an `Attribution()`, the WhatsApp route
returns it for the app's settings screen, the hub logs it once at start-up, and the opt-in
confirmation message carries it. Its routes stay **UNVERIFIED** until proven against a live
instance, and the paths are settings rather than constants.

Four defects found in H1 while building H2, all fixed here:

| Finding | Effect |
|---|---|
| `domain.MAC` and `domain.IP` marshalled to JSON but could not read themselves back | The relay never found its own address in a device list, so "never cut the relay" never fired. |
| `SharedGateway` exposed every optional router capability except `HostObserver` | The per-station view was unreachable behind the serialised session. |
| `audit_action` is a Postgres enum owned by the kernel's audit module | None of the hub's own actions were in it, so every hub audit row was refused and retried ten times — and the retries starved the dispatcher that carries the alerts. Each module now adds its own values. |
| `audit_logs.actor_id` references `users`, and `target_id` is a `uuid` | The pairing and result paths passed an agent id, and the block path passed a MAC. Both now travel in the metadata. |

Two things worth knowing for H3:

- **The hub test packages must run with `-p 1`.** They share one database, and the kernel's outbox
  dispatcher holds a database-wide advisory lock: two packages at once had one hub's dispatcher
  deliver the other hub's events, to its own notifier.
- **The hub key is derived, not stored** (HKDF-SHA256 over `KERGUI_HUB_KEY_SEED` and the household
  id). Every household has its own, the owner delegates or revokes it in the bundle, and a hub with
  no seed says so instead of promising a block it cannot do.

## Milestones

| | Content |
|---|---|
| **H0** ✅ | This RFC; `cmd/kergui-hub` on the pinned kernel; Postgres compose; strict config; Postgres integration tests (signup → verify → login → tenant → refresh rotation, isolation, rate limit); architecture rule; `hub-ci.yml`. Apple entitlement request (owner action). |
| **H1** ✅ | Mock F6600P; `internal/trust`; agents (QR, tokens, keys); relay (lease, queue, long-poll); `internal/agent`; `kergui serve --hub`; remote devices / block / unblock; audit. Device snapshots pulled forward from H2. |
| **H2** ✅ | Snapshots, hub diff, `Recognise`, grouped alerts; outbox `router.device_first_seen`; Evolution Go + console adapters; `wa.me` opt-in; "1 = block"; push port. |
| **H3** | App: account, household, members, QR pairing with the 6-digit code, delegations, recovery, sealed credentials, Android relay service, iPhone foreground relay, Local Push spike, non-technical French onboarding, demo on mock F6600P. GitHub Actions only. |
| **H4** | Usage charts (`TrafficDelta`), cut / restore Internet, SSID on/off with guards (F6600P SSID writes validated on hardware first). |
| **H5** | Meta Cloud API; image built on GitHub Actions, VPS deploy; store privacy labels (data now collected), CDP declaration; Play foreground-service declaration. |

## Consequences

- The privacy promise of RFC-004 ("data not collected") holds only without a hub account. Store
  listings and the privacy policy change in H5.
- `go.mod` gains the kernel, pgx and golang-migrate. They are linked only into `kergui-hub`.
- Hub tests need Postgres: `make hub-test` (docker) locally, a service container in CI. It covers
  the hub modules and the end-to-end relay path; `internal/trust` and the F6600P mock need no
  database.
