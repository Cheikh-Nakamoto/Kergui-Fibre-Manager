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
  | `auth` | rotating refresh and the token signer; the app signs in with a WhatsApp-verified number instead of its e-mail signup (see "Hub identity as built") |
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
   - The relay bounds every box operation (login, command, snapshot, auto-reject, cut / restore,
     SSID change) to its own lease deadline: the time it sent the granting heartbeat plus the
     heartbeat's `ttl` (`ttlMs`, else `ttlSeconds`). None starts once it has passed, and one still
     running is cancelled when it comes (503 `{code: "lease_expired"}` for a command). The hub's
     epoch fencing protects results and snapshots, not the box session: without this, a relay that
     stalled past its lease (Doze, a slow box, bad Wi-Fi) would log in over the new holder. A gap
     between two holders is correct; an overlap never is (#55).
   - The long poll judges the lease at the start of the wait: a lease that lapses or moves during
     the wait ends it with "nothing to do" (204), not "not holder", so the relay heartbeats next
     instead of standing down for a full beat (#55).
   - With a hub account, the app goes through the hub — except the owner's phone at home, which
     talks to the box directly and whose session wins (#94, « proprio prioritaire », below). The
     relay yields to it without going offline.
2. **The relay must be on the right network.**
   - The Wi-Fi signature (SSID + BSSIDs) registered by the owner must match the relay's current
     connection.
   - Second check: the box identity, i.e. the TLS pin or, failing that, `RouterInfo` serial/MAC.
   - The relay binds its process to that Wi-Fi (`bindProcessToNetwork`).
   - On Android, reading SSID/BSSID requires the location permission. If it is refused, only the box
     identity is checked, and the app says so.
3. **The hub transports, the owner decides** (see Trust model).
4. **Relay-side allow-list.**
   - Only `devices`, `block`/`unblock`, `wifi-block`/`wifi-unblock`, `ssids` (read, on / off, rename), `filter`, `usage` and `internet/cut`/`restore` are reachable (#50).
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
| `POST /relay/v1/heartbeat` | `{wifi:{ssid,bssid}, router_identity, lan_ip, version, released, auto_reject, paused}` → `{lease: active\|standby, epoch, ttl, retry_after, bundle_version, credential_version, policy_version}` |
| `GET /relay/v1/trust` | the owner-signed trust bundle (H1) |
| `GET /relay/v1/commands?wait=25s` | lease holder long-polls signed commands |
| `POST /relay/v1/commands/{id}/result` | signed `{status, body}` |
| `POST /relay/v1/snapshots` | signed snapshots: `devices` and `observations` every 60 s, `ssids` every 5 min (H2) |
| `GET /relay/v1/credentials` | the sealed box password |
| `GET /relay/v1/policy` | the owner-signed relay policy: auto-reject and its entitlement token (RFC-006, #36) |

The app's side, under `/api/v1/tenants/{tenantId}/` with a kernel JWT:
`agents/pairing`, `agents`, `agents/{id}/credentials`, `network`, `trust-bundle`,
`router/devices`, `router/devices/{mac}/block|unblock`, `router/commands/{id}`, `router/relay` (is the
relay on the box, #46), `members/me/key` (a member phone's key, #47), `relay-policy` and
`router/auto-reject` (who enforces auto-reject, RFC-006), and from H2
`router/inventory`, `router/alerts/{id}`, `whatsapp`, `whatsapp/optin`, `hub-key`, and from H3
`push-tokens` (#49).

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
  - *Superseded 2026-11-23 (issue #59): the hub embeds whatsmeow itself, see
    [docs/hub-whatsapp.md](../hub-whatsapp.md). Evolution Go needs a paid licence activation.*
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
| 7 | The Evolution Go webhook is authenticated by a secret in its path | Evolution Go offers a `WEBHOOK_URL` and signs nothing, so there is no signature to check. The path secret is compared in constant time, the instance name and token in the payload are checked, and the route is rate-limited; the console adapter signs its inbound calls (HMAC-SHA256), which is what the tests exercise. Meta's signed webhooks arrive with Meta in H5. |

What the licence of Evolution Go requires, and what the hub does about it: the server answers 503
until its instance is registered in its Manager, and keeps its activation alive with its own
heartbeats, so a 503 is reported as `ErrNotActivated` (not a hiccup) and `Status` feeds the hub's
health. Its "usage notification" clause asks for a visible notice, reachable by administrators from
the settings or the documentation: the provider port carries an `Attribution()`, the WhatsApp route
returns it for the app's settings screen, the hub logs it once at start-up, and the opt-in
confirmation message carries it. Its routes (`POST /send/text`, `GET /instance/status`, both
authenticated by the instance's own token, not `GLOBAL_API_KEY`) and its `MESSAGE` webhook shape are
verified against Evolution Go's source, and the routes and the `503 LICENSE_REQUIRED` answer against
a live, not yet activated instance (2026-11-22); sending and receiving stay to be proven on an
activated one. *Since issue #59 the Evolution adapter is gone: [docs/hub-whatsapp.md](../hub-whatsapp.md).* The paths remain
adapter fields rather than constants.

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

## Hub identity as built (#41)

The kernel's auth is e-mail and password: `signup-owner` creates the tenant, the user (pending
until the e-mail is verified) and the owner membership; `login` needs a verified e-mail. The
audience signs in with a phone number and a WhatsApp code (RFC-006, Accounts), and two sign-ins
for one app would be one too many. The number verified by the licensing module's OTP is
therefore the single hub identity, and the kernel stays unforked: `internal/hub/identity`
composes its public repositories (`users`, `tenants`, `tenant_settings`, `memberships`,
`refresh_tokens`) and its token signer (`auth.Module.JWTService()`), the way its own
`SignupOwner` does, minus the e-mail and the password.

| # | Departure | Why |
|---|---|---|
| 1 | `POST /licensing/v1/otp/verify` also returns `hub: {userId, tenantId, accessToken, refreshToken}` | One sign-in, two sessions. The household is signed in before the `kgs_` session is created, so a failure leaves neither. |
| 2 | The first verification of a number creates an **ACTIVE** user, the household tenant (`foyer-<account id>`, default settings) and its **owner** membership; later ones (any phone of the household) reuse them | The WhatsApp code proves the number as an invite token proves an address, so there is no pending state. `hub_identity_households` maps the licensing account to the user and the tenant, under an advisory lock so two phones at once cannot create two households. It is keyed on the account id, not the number. |
| 3 | The kernel user's e-mail is `acc-<account id>@phone.kergui.invalid` and its password column holds a value no bcrypt hash compares to | `users.email` is required and unique. `.invalid` (RFC 2606) never receives mail, and `POST /api/v1/auth/login` can never open a phone household. |
| 4 | The refresh token is the kernel's, scoped to the household, and rotates at the kernel's `POST /api/v1/auth/refresh` | No new token kind. Its TTL is the auth module's (`KERGUI_HUB_REFRESH_TTL`), so a pair issued at sign-in lives as long as a rotated one. |
| 5 | `POST /licensing/v1/session/hub` (with the `kgs_` session) issues a new pair | The `kgs_` session lasts a year, the kernel refresh token 30 days, and a replayed refresh token makes the kernel revoke them all. The licensing session is the way back in without a new code; signed out of it, the household is out of reach too. |
| 6 | The kernel's e-mail signup and login stay mounted | The hub's integration and end-to-end tests use them, and so can an operator with `curl`. The app does not. |

The app keeps the pair in SecureStore beside the `kgs_` session (`mobile/src/licensing/hubTokens.ts`),
refreshes the access token a minute before its `exp`, one refresh at a time, saves the rotated
refresh token before using it, falls back to `session/hub` when the kernel refuses, and signs out
of both. The acceptance path runs in `internal/hub/e2e` (`TestE2E_WhatsAppNumberIsTheHubAccount`):
OTP → household → `GET /api/v1/tenants/{id}/agents`, refresh rotation and reuse, a second phone,
and another number refused with 403.

### Household members (#42)

The owner invites a number with a role; the invite goes out on WhatsApp and is accepted when that
number signs in. The membership, the roles (owner / admin / viewer) and the revocation are the
kernel's; the hub adds what a phone household needs around them.

| # | Departure | Why |
|---|---|---|
| 7 | The hub keeps its own invites (`hub_identity_invites`, keyed on the E.164 number) instead of the kernel's `invite_tokens` | The kernel's invite is keyed on an e-mail and its `member.invited` event sends one, which a phone user never receives. One open invite per number and household (inviting again replaces it), 7 days like the kernel's, at most 20 a day per household so the route cannot be used to message strangers. A message that fails to send withdraws its invite. |
| 8 | Routes `GET/POST/DELETE/PATCH /api/v1/tenants/{id}/members[...]` (`internal/hub/identity/handler.go`) beside the kernel's own `/memberships` | Every member lists (numbers masked for a viewer); only the **owner** invites, changes a role, removes and sees the pending invites (`OWNER_ONLY` otherwise). The owner's own membership cannot be changed or removed (`OWNER_UNTOUCHED`). |
| 9 | Accepting is part of sign-in: `identity.SignIn` turns the number's open invites into ACTIVE memberships, in the role invited, and the session goes to the household **most recently joined** | A member signs in to the household they were invited into; their own (created on their first sign-in, #41) stays theirs, and the app comes back to it when it is removed (`POST /licensing/v1/session/hub` again). A member removed earlier gets their membership row back, not a second one. |
| 10 | The role change updates `memberships.role` in place, audited as the hub's own `MEMBER_ROLE_CHANGED` | The kernel has no role-change use case, and a new row would leave a revoked one beside it that `FindByUserAndTenant` may read. Invite, acceptance and removal reuse `MEMBER_INVITED` / `_ACCEPTED` / `_REVOKED`. |
| 11 | Removal is the kernel's `RevokeUC`, then, in the same transaction, the member's refresh tokens **scoped to that household** are revoked and their WhatsApp alerts for it stopped | The kernel revokes one refresh token or all of a user's, and the member keeps their own household. Their access token stops at the membership check at once (`tenantaccess.Guard`, 403). A removed member who replays the old refresh token trips the kernel's reuse detection, which revokes all their sessions; the app falls back to the licensing session. |
| 12 | Roles on the household routes: a viewer reads (devices, inventory, members) and is refused every write with 403, router commands included (`relay.Policy.CheckCommand` before the queue, the relay checking again) | Unchanged rule from the trust model, now reachable by a second number. An admin is refused the members routes. |

The app's Household screen (`mobile/src/app/household.tsx`, from Réglages > Foyer) lists the members
with their role, and for the owner the invite form (number, viewer or admin), the pending invites
and, per member, "Rendre administrateur / lecteur" and "Retirer" (confirmed). A 403 on the
household signs the app in again from the licensing session, once, to the household that is its
own now (`mobile/src/licensing/household.ts`). The demo shows a made-up household and no form. The
acceptance path runs in `internal/hub/e2e` (`TestE2E_InviteAcceptMemberRolesAndRemoval`): invite
on WhatsApp → the number signs in into the household as a viewer → it lists the devices the relay
pushed → its signed block is refused 403 and never reaches the box → role change → removal (403,
refresh token dead, its next session in its own household) → re-invite and rejoin → a cancelled
invite is not accepted.

### Relay pairing (#43)

The owner's keys are generated in the phone's Go core (`mobilecore/owner.go`) on first use and kept
in its AES-GCM vault (secret `owner-identity`) beside the box password; JS gets the public halves,
the QR payload, the six digits and signed bundles, nothing else. The app's Réglages > "Relais à la
maison" (`mobile/src/app/relay.tsx`, `mobile/src/licensing/relays.ts`) runs the pairing: a first
bundle signed by the owner alone if the household has none, `agents/pairing`, the QR, a look at
`agents` every 3 s for the relay that redeemed it, its six digits from the keys the hub lists, then
"same code" signs it into the next bundle (`PUT trust-bundle`) and "different code" revokes it.

| # | Departure | Why |
|---|---|---|
| 13 | The core keeps the last bundle version it signed and always signs above both it and the hub's | A hub that replayed or dropped a bundle would otherwise have the owner sign a version a relay already holds, and the relay would refuse it as stale. |
| 14 | A stored bundle the owner's key did not sign is not built on: the next one starts from the owner alone | The hub cannot slip a relay or a delegation into what the owner signs. A phone reinstalled with new keys starts over the same way (recovery is #48). |
| 15 | A phone relay pairs through the core as well (`RelayEnroll`, `RelayCheckTrust`), in the vault under the PC relay's id `hub-agent`; running it is #45 | One pairing protocol for both relays: `agent.Enroll`, and `agent.CheckTrust` to learn the owner signed it in. |
| 16 | The demo has no hub: the core plays the relay on the same QR (`DemoRelayRedeem`, which pins the owner's key from it) and the bundle it signs stays on the phone | The store reviewer and the Maestro flow see the whole onboarding, and nothing is sent. |

The acceptance path runs in `internal/hub/e2e` (`TestE2E_QRPairingWithThePCRelayAndAPhoneRelay`,
`TestE2E_ASwappedKeyMakesTheCodesDifferAndPairingFails`): the owner is a real `mobilecore.Core`,
the PC relay the real `kergui hub enroll`, the phone relay another core; a proxy that swaps the
relay's key in the owner's view makes the codes differ, and a hub serving its own owner key is
refused by the fingerprint in the QR.

### Sealed box password (#44)

The password the owner typed at the box login stays in the core's vault. `SealCredentials`
(`mobilecore/credentials.go`) seals it with `nacl/box` (owner X25519 → relay X25519) for the relays
of the bundle the owner signed, and JS gets the ciphertext, which it `PUT`s on
`agents/{id}/credentials`. "Same code" seals it for the new relay right after the bundle upload
(`relays.confirm`); every login to the box from the app seals it again for all relays
(`relays.afterBoxLogin`, in the background, noted in the journal on failure). The relay
(`agent.pullCredentials`) fetches `GET /relay/v1/credentials` when `credential_version` moves,
opens it with the owner's X25519 key from the signed bundle and stores it in its own vault.

| # | Departure | Why |
|---|---|---|
| 17 | The relays to seal for, and their keys, are read from the bundle **the owner's key signed**, never from `GET agents` | The hub cannot add itself as a recipient or swap a relay's key. A phone whose key did not sign the bundle (an admin member) seals nothing. |
| 18 | The version is the owner's, kept in the vault with a SHA-256 of the last login sealed: it moves only when the password does | Sealing again at every login does not make every relay fetch it; the hub answers 409 to a version it already has, which the app takes as "unchanged". |
| 19 | The relay keeps the **sealed** version, refuses one not above it, and does not fetch again a version whose blob it refused | A hub replaying an older blob of the owner's under a higher version cannot roll the password back, and a forged blob costs one fetch, not one per heartbeat. |
| 20 | Pairing without a password in the core (owner not logged in to the box on this phone) still adds the relay; the screen says the password follows at the next login | The relay is trusted; it simply cannot log in until then. |

The acceptance path is `TestE2E_SealedBoxPasswordFromThePhoneToTheRelay` (`internal/hub/e2e`): the
owner is a real `mobilecore.Core` logged in to the mock F6600P; seal → hub → relay fetch → login;
the box password changes (`F6600P.SetPassword`) and reaches the relay through `credential_version`
(v2); a blob sealed by another key is refused and the relay logs back in with the owner's password;
neither password appears in any row of the hub's database, in the hub's log or in the relay's.

### Phone relay (#45)

A phone left at home runs the relay loop in its Go core (`mobilecore/relayrun.go`:
`RelayStart`, `RelayStop`, `RelayStatus`, `RelayFindBox`) on `internal/agent`, the same
code as `kergui serve --hub`; the Android foreground service `RelayService` (type
`connectedDevice`) only keeps it alive, follows the Wi-Fi and shows its state in an ongoing
notification with « Arrêter ». The app's welcome screen offers « Être un relais » (scan the
owner's QR, or type the code shown under it; six digits; the owner confirms; permissions;
start), and Réglages « Ce téléphone sert de relais ». Details, battery and Doze:
[relay-android.md](../mobile/relay-android.md).

| # | Departure | Why |
|---|---|---|
| 21 | The relay checks the network **the owner signed** (`trust.Network` in the bundle) itself, on every round (`agent.CheckNetwork`), and stands down on its own | The hub's check reads the hub's own record of the network, which the hub could change. A relay on another Wi-Fi, another access point or in front of another box never calls the box, whatever the hub answers. |
| 22 | A relay standing down sends `released: true` on every heartbeat, and the hub treats a released heartbeat as **not a candidate** (`Eligibility`), holder or not | Before, `released` only freed a lease its sender held; a relay standing down could still be granted one, or keep a lower-priority relay waiting by its priority. |
| 23 | Every socket of the core is bound to the home Wi-Fi while it relays (`RouterOptions.DialControl` → `RelayNetwork.BindSocket` → `Network.bindSocket`), on top of `bindProcessToNetwork` | Go's sockets do not go through bionic's `connect`, so `bindProcessToNetwork` alone does not apply to them. A refused bind is a refused connection: nothing reaches the box over mobile data. |
| 24 | The relay phone never goes through the connection screen: `RelayFindBox` tries the Wi-Fi's gateway and the usual addresses and keeps the one whose certificate is the pin the owner signed | The password arrives sealed (#44); asking a household to type the box's address on a second phone is a step for nothing. |
| 25 | The relay's commands run without the core's paid-action gate | The owner's app gates what it signs and the relay's allow-list bounds what runs, as on the PC relay; a dedicated phone holds no pass of its own. |
| 26 | One I/O lock in the core for every call that reaches the box (the app's `Request`, the watch service, the relay) | The box keeps one admin session; on the relay phone the watch and the relay share the core's single session instead of logging each other out. The watch service skips its auto-reject round while the relay enforces it. |

The acceptance path is `internal/hub/e2e/phone_relay_e2e_test.go`: a real `mobilecore.Core`
pairs with the QR, finds the box by its pin, takes the lease, pushes the devices and runs a
block the owner signed, then stands down (`RelayStop` frees the lease at once); on a
neighbour's Wi-Fi it never logs in; without the location permission it relays on the box
identity and says so. Not done here: the owner's app does not yet put the household's Wi-Fi
names in the bundle (`ssids`), so until it does the signature is the box certificate alone;
QR scanning on iPhone and the iPhone relay are separate issues.

### Remote mode in the app (#46)

A phone whose owner key signed a relay into the household's bundle goes through the hub for the
device list and for block / unblock, at home as well as away (design rule 1). JS does the HTTP;
the Go core (`mobilecore/remote.go`) decides what to believe: `RemoteRelays` reads the relays
from the bundle the owner's key signed, `OpenDeviceSnapshot` checks the relay's signature over
the list `router/devices` serves, `SignDeviceCommand` signs the block or unblock with the owner's
key after the paid-action gate, and `VerifyCommandResult` checks the relay's signature over the
answer before the app shows ✅ (`mobile/src/features/remote.ts`, `mobile/src/core/remote.ts`).

| # | Departure | Why |
|---|---|---|
| 27 | `GET router/relay` added: `{state: active\|standby\|offline\|none, agentId, lastSeenAt, ageSeconds, reason}`, from the lease and the household's heartbeats (`domain.DecideRelayStatus`) | The app has to say "relay offline since 14:02" instead of waiting a minute for a command to expire. Active means the lease holder is heard within the presence window: a lease that has not run out says nothing about a phone whose battery just died. Advisory only — lists and results are believed on the relay's signature. |
| 28 | An offline or never-heard relay fails the block in the app before anything is signed or sent; standby still sends | Queuing a command for nobody only makes the household wait 60 s for "expired". A standby relay may take the lease within a heartbeat. |
| 29 | Remote mode is the owner's phone only for now; other members stay in local mode | A member's key did not sign the bundle, so their core cannot vouch for a relay's key; they get their own delegated key in #47 (below). |
| 30 | The paid-action gate for a remote command uses the box serial the core read this run, or else the one the app kept from its last reading | A remote phone does not log in to the box to read the serial again; the token is still verified in Go (keys, expiry, tier, box). |
| 31 | In remote mode the app never logs in to the box for the Devices tab: no Wi-Fi deny list (cutting Wi-Fi access through the relay is #50), no usage sample, no auto-reject round. A hub that cannot be reached keeps a phone that was remote in remote mode and says so | Falling back to a box session would knock the relay off the box without telling anyone. |

The acceptance path is `TestE2E_RemoteModeBlockFromTheOwnersCore` (`internal/hub/e2e`): the owner
is a real `mobilecore.Core`, the relay the PC one, the box the mock F6600P. Remote mode on, the
signed list with its `as_of`, a block refused without a pass and signed with one, run on the mock,
its result verified, a result the hub signed refused, an unblock followed through
`router/commands/{id}`, then the relay stopped: `router/relay` says offline with its date, the
next block stays queued, and nobody logs in to the box. The demo has no hub: once its simulated
relay is paired, the Devices tab shows the same banner, marked « Démo ». Not done here: the other
tabs (Conso, Wi-Fi) and the Android watch service still read the box directly; they move to the
relay with #50.

### Member delegations (#47)

A member's phone gets its own key, created in its Go core (`mobilecore/member.go`, vault secret
`member-identity`, never the owner key even on a phone that owns another household); the app
publishes the public halves on `PUT members/me/key` when it opens Réglages > Foyer. The owner's
Foyer screen lists each member with that key and the six digits `PairingCode(owner, member)`,
which the member's screen shows too, and grants « Voir » (`view`) or « Contrôler » (`control`):
the owner's core signs the delegation into the next bundle (`SignTrustBundle` change `delegate`,
`revoke`, `ownerOnly`). The rules live in `internal/trust` (`Bundle.Delegate`, `Revoke`,
`RenewDelegations`, `MemberDelegation`); the relay's `Verifier` enforced them already.

| # | Departure | Why |
|---|---|---|
| 32 | A delegation lasts 30 days (`trust.DelegationTTL`) and the owner's app renews every one that ends within 7 days (`DelegationRenewWithin`), or has ended, each time remote mode is detected and with every bundle it signs (`RenewDelegations`) | Expiry is what ends a member's access when the owner's phone is gone: nobody else can renew. While the owner's app runs, a member never sees it lapse. |
| 33 | Revoking drops the delegation **and** lists the key in `revoked`; the relay names a revoked key before it looks for its delegation; granting the key again lifts the revocation | A relay that still holds an older bundle refuses the key by name, and the owner can change their mind. Removing a member from the household revokes their delegation too, and the hub forgets their key. |
| 34 | `owner_only` keeps the delegations and suspends them: no member command passes (member's core, hub, relay), and the members still read the relay's signed list | Turning it off gives everyone back what they had. The list is not control. |
| 35 | The member's core pins, per household, the owner key of the first bundle that delegates it, and believes only that key afterwards (`other_owner` otherwise) | The member cannot know the owner's key in advance; the six digits on both screens are what catch a hub that slipped its own owner in first. An owner who reinstalls is #48: a handover checked against the bundle it held moves the pin. |
| 36 | « Contrôler » is offered to an admin only; the hub keeps refusing a viewer's writes (#42) and now also refuses an expired delegation (`ErrDelegationExpired`, 403) | The hub's role and the owner's delegation are two separate locks; a viewer with « Contrôler » would only meet a 403. |
| 37 | A member's commands go through the paid-action gate on their phone, like the owner's | The relay runs whatever a delegated key signs; the gate is where the pass is checked. |

Remote mode (#46, departure 29) opens to a member once the bundle delegates their key: the
member's core reads the relays from it, opens the relay's signed list and results
(`OpenDeviceSnapshot`, `VerifyCommandResult`), and signs block / unblock with its own key
(`SignMemberCommand`, refusals `owner_only` / `not_delegated`). The relay's refusals — expired,
revoked, owner-only, a view key writing — are tested where the clock is the test's
(`internal/agent/delegation_test.go`); the acceptance path is `TestE2E_MemberDelegationFromKeyToBlock`
(`internal/hub/e2e`): two real cores, the member's key published, the same six digits, a control
delegation, the member's block run by the PC relay on the mock F6600P and verified by the member's
core, then owner-only and revocation refused at the core and at the hub. The demo shows the
screen on its example household, in memory, nothing signed.

### Lost owner phone (#48)

Two ways to hand the household to a new owner key, both ending the same way: a **handover**
(`trust.Handover`, domain `kergui/handover/v1`) from the old owner key to the new one, signed by a
key the bundle in force already trusts, carried inside the next bundle (`Bundle.Succession`), which
the new key signs with the old one in `revoked` (`trust.Succeed`). Every relay and member checks it
against the bundle **it** holds (`trust.CheckSuccession`), never against the new one, then pins the
new key (`Verifier.AcceptBundle`; the relay keeps it in its state, the member's core in its vault).

- **Co-owner.** The owner signs a member's key in as a co-owner (`Bundle.CoOwners`,
  `SignTrustBundle` change `coOwner` / `removeCoOwner`; « Réglages > Téléphone perdu »). That
  member's phone takes the household over (`TakeOverHousehold`): its member key endorses its own
  owner key.
- **At home.** The owner's new phone asks the relay's recovery listener (`:8791`, its address on
  the home network only, `internal/agent/recovery.go`) for a nonce, sends
  `RecoveryProof(tenant, box password, nonce, new key)` and gets the handover the relay signs
  (`RecoverAtHome`).

| # | Departure | Why |
|---|---|---|
| 38 | The proof is HMAC keyed with argon2id(box password) (departure 5), bound to the household, the nonce and the new key; the nonce is single-use, 2 minutes, and claims are limited to 5 per 10 minutes | Whoever sees a proof can only test one password per argon2id run; a captured proof cannot be replayed nor redirected to another key. |
| 39 | Recovery has its own listener on the relay's LAN address, refuses any non-LAN caller, and is on no command allow-list; the phone's core refuses a relay address that is not private, link-local or loopback | The hub can neither ask for a challenge, nor see a proof, nor make a handover: it only carries the resulting bundle. |
| 40 | A co-owner must be an administrator of the household on the hub | Only an owner or admin may `PUT trust-bundle`; the hub is not asked to judge a succession. |
| 41 | The relay policy the old owner signed stops binding after a succession; the members keep their delegations; a relay or member that missed the bundle holding the handover follows any later one (the handover is kept), but not a second succession on top of it | Fail safe: what the old key signed alone is dropped, a broken chain leaves `other_owner`. |

Tests: `internal/trust/succession_test.go`, `internal/agent/recovery_test.go` (LAN path, co-owner
path, never through the hub, old key refused), `mobilecore/recovery_test.go` (both paths through
the cores, a member following the handover, an Internet address refused before any request); jest
`recovery.test.tsx`, `recoveryFeature.test.ts`; Maestro `recovery-steps.yaml` (demo, in memory).

### App push and WhatsApp from the app (#49)

A new device now reaches the household on both channels with the app closed. The alerts module
pushes every closed inventory window to **every installation of the household that registered a
token**, whether or not its member linked WhatsApp (the app is the channel of whoever did not),
and records each push in `hub_alert_deliveries` (`channel = 'push'`). The push data carries
`{kind: new_device, tenantId, alertId, mac}`; the app opens the device sheet of `mac`, also when
the push launched it (`mobile/src/alerts/taps.ts`).

- **Tokens.** `PUT /api/v1/tenants/{tenantId}/push-tokens` `{token, platform}` and
  `DELETE …/push-tokens/{token}` (any member, their own phone), table `hub_push_tokens` keyed on
  the household and the token. The app registers its Expo push token when the alerts are on and
  it is signed in, and removes it when they are turned off; a removed member loses theirs with
  their WhatsApp opt-in (`Repo.StopMember`).
- **Adapter.** `KERGUI_HUB_PUSH_PROVIDER=console` (default) logs, like the console WhatsApp
  provider; `expo` sends through Expo's push service (`internal/hub/alerts/adapters/expo`), which
  carries it over FCM and APNs. A `DeviceNotRegistered` ticket drops the token. Expo needs, once,
  the Firebase (FCM v1) service account uploaded to the EAS project, and the build the EAS
  project id (`KERGUI_EAS_PROJECT_ID`) and `google-services.json`
  (`KERGUI_GOOGLE_SERVICES_FILE`, `mobile/config/push.js`); without the project id the app
  registers nothing. `KERGUI_HUB_EXPO_ACCESS_TOKEN` only when the project enforces push security.
- **Réglages > Alertes WhatsApp.** The opt-in opens WhatsApp on the hub's `wa.me` link
  (`KERGUI <code>`), the provider's notice (`GET whatsapp`, Evolution Go's) is shown under it, and
  the owner's « Répondre « 1 » pour bloquer » signs the hub key in (`SignTrustBundle` change
  `hubKey`, `trust.Bundle.DelegateHubKey`: `hub_block`, no expiry, one hub key) or revokes it by
  key id. `RemoteRelays` reports `hubKey` to the owner's app.

Tests: `internal/hub/alerts/usecase/alert_test.go` (fake pusher), `adapters/expo/expo_test.go`,
`internal/hub/app/push_integration_test.go`, `internal/hub/e2e/push_e2e_test.go` (new device on
the mock F6600P → WhatsApp and push on the console providers), `internal/trust/hubkey_delegation_test.go`,
`mobilecore/hubkey_test.go`; jest `alertTaps`, `pushToken`, `whatsappFeature`, `whatsappScreen`;
Maestro `whatsapp-steps.yaml` (demo, in memory).

### Consumption, SSIDs and cut / restore through the relay (#50)

In remote mode the Conso and Wi-Fi tabs and the device sheet's « Couper l'accès » go through the
relay, as the Devices tab did with #46: the core signs any request of the allow-list
(`SignRemoteRequest`, owner key or a member's delegated key, after the paid-action gate), the hub
carries it on `POST router/commands` (`{payload, sig, body}`, checked against the hub's own list
`carried`, then the same `Policy.CheckCommand` and queue as block / unblock), and the app believes
the answer once `VerifyCommandResult` checks the relay's signature. The screens are the local
ones; they show « Via le relais · mesuré / lu il y a … » from the result's `executedAt` (as_of)
and the same staleness warning past 3 min. The Devices tab gains « Internet de la maison »:
couper (Basic) / rétablir (free).

The relay's allow-list is now exactly: `GET devices`, `wifi/ssids`, `wifi/blocked`, `filter`,
`usage`; `POST filter/toggle`, `devices/+/block`, `unblock`, `wifi-block`, `wifi-unblock`;
`PATCH wifi/ssids/+`; `POST internet/cut`, `internet/restore` (`TestAllowList_IsExactlyIssue50`).

| # | Departure | Why |
|---|---|---|
| 42 | `internet/cut` and `internet/restore` are the relay's own routes, not the API's: it blocks every device not already blocked, one by one through the block route, except itself (LAN IP), the bundle's relays (`Relay.MACs`) and the protected devices, and answers `{cut, spared, failed}`; restore unblocks the MACs it is given, else those of its last cut. A relay that cannot tell which device it is (no LAN IP match, no MACs in the bundle) refuses the cut | Rule 5 is checked where the box is, on the relay's own reading, whatever the owner's app sent. A device someone blocked on purpose is not "restored". The app keeps the cut list too, so a relay restart does not lose it. |
| 43 | A remote SSID change is on / off or a rename only (a password or `hidden` is refused, by the core and by the relay); turning off **or renaming** the network the relay is on — by its reported Wi-Fi, or by its own device's SSID in the box's list — or one a protected device is on is refused (403) | A rename drops the relay off the network as surely as turning it off, and the owner's signed Wi-Fi signature would no longer match. The device list covers a phone whose location permission was refused. |
| 44 | A write whose endpoint the box model documents as not `Verified` is refused by the relay with 501 `{code: "unverified"}`, read from the adapter's `AdapterMeta` (`Config.Endpoints`, through Inspect, cached); the Wi-Fi deny list (`wifi_blocked`) counts as such a read. Endpoints that cannot be read fail closed | On the F6600P, SSID writes are Verified (validated live 2026-11) and Wi-Fi access control is not: through the relay « Couper l'accès » shows, disabled, with « pas encore validé sur ce modèle de box ». Locally it stays as before (#31). |
| 45 | `GET usage` through the relay takes a sample first; past months, the 6-month history and the MAC filter switch are not on the allow-list: in remote mode the app says they are read at home | The figures are "as of now", not as of the last sample. The rest is Max reading of the relay's database, out of #50's scope. |
| 46 | A household VIEWER may now send reads (`GET`) through the hub; writes stay refused at the hub | Consumption and SSIDs are reads; the relay's view delegation already allowed them. |

Tests: `internal/agent/house_test.go` (guards, unverified, sampling), `mobilecore/remotereq_test.go`
(tier gating per RFC-006; a real core relay on the mock F6600P: the cut spares the relay and a
protected device, the relay's and the protected device's SSIDs cannot be turned off, the 5 GHz is
renamed once nobody guarded is on it, `wifi-block` 501), `internal/hub/relay/handler/carry_test.go`,
and jest `remoteHouse.test.tsx`. Maestro: `mobile/.maestro/house-remote-steps.yaml`.

### « Proprio prioritaire »: the owner at home and the relay (#94)

Seen on 2026-11-25 with a phone relay: the owner's phone, at home, logged in to the box beside the
relay (the app's own reads — the box serial for the pass, the watch service — still reach the box
in remote mode), evicted the relay's session (`login_preempt`), and from then on each side evicted the other on
its next call: every relay read became a login plus a preempt, and a relay round can run until
its lease cancels it (the core's I/O lock is not bounded by the lease), past the hub's 45 s
presence window — the hub reported the relay « hors ligne depuis 15:19 ». (The relay phone's
journal was not captured; why it never came back at all is not proven.) Meanwhile the app showed
« Via le relais » although the box was right there.

The owner decided: the owner's direct session comes first.

- **The app** (`mobile/src/features/atHome.ts`, `useRemoteMode`) is at home when the box answers,
  at the address the phone signed in to, with the certificate it pinned (the box identity, as the
  relay checks it without the Wi-Fi name; the owner's phone does not ask for the location
  permission). At home the owner's phone talks to the box directly; away it goes through the
  relay. It asks again every 20 s, on every return to the app, and at once when a local read finds
  the box unreachable; the lists stay on screen while it switches. A member's phone always goes
  through the relay.
- **The adapter** asks before evicting another session (`port.RouterOptions.Preempt`, given the
  box's name for it, `192.168.1.17(user)`); a refusal leaves the login parked and fails with
  `domain.ErrSessionHeld` (503 `session_held`). The owner's app has no policy: it always evicts.
- **The relay** (`internal/agent/yield.go`) evicts its own stale session (the label carries its own
  LAN IP) and yields to any other. Having yielded it is **paused**: it keeps its heartbeat (with
  `paused: true`), its lease and its long poll — the hub answers `GET router/relay` with
  `state: paused`, never offline, and no other relay takes the box from the owner — but it reads
  nothing from the box, does not claim auto-reject (the owner's phone runs the rule), and answers
  a member's command with 503 `{code: "owner_at_home"}`. A command the **owner** signed means the
  owner is away (the app goes through the relay only then): it takes the box back at once,
  whatever holds it. Without one, the relay looks again after `YieldBackoff` (2 min): one login,
  and it pauses again if the owner is still on the box, or carries on if the session has gone. A
  relay that does not know its LAN IP never yields (it could not tell its own session from the
  owner's).
- **The phone relay's I/O lock** (`mobilecore`, `ioLock`) is now waited on only as long as the
  relay's lease runs: a relay stuck behind the app's or the watch service's call no longer runs its
  round past the lease and misses its heartbeat.
- **The app away**, finding the relay paused, signs a read of the device list (at most once a
  minute): that is the owner's command that takes the box back, so the list catches up within a
  round instead of waiting for the box to time the owner's old session out.

| # | Departure | Why |
|---|---|---|
| 47 | The owner's phone at home talks to the box directly; "at home" is the pinned certificate answering at the signed-in address, not the Wi-Fi name | Going through the hub from the box's own Wi-Fi is slower, depends on the relay, and did not stop the phone reaching the box anyway. The Wi-Fi name would need the location permission on the owner's phone. |
| 48 | The paused relay keeps the lease and is reported `paused` | Releasing it would hand the box to another relay, which would fight the owner the same way; and the household must never read "offline" for a relay that is fine. |
| 49 | The signal that the owner has left is a command the owner signed, not a new hub route | It is authenticated by the owner key, which the relay already trusts; the hub can delay it but not forge it. The backoff covers an owner who leaves without sending anything. |
| 50 | The heartbeat's `paused` is omitted when false | The hub refuses unknown fields: a relay that is not paused stays understood by a hub that predates it. **Deploy the hub (migration `0004_paused`) before the app.** |

Tests: `internal/agent/yield_test.go` (the F6600P mock in its parking mode: the relay pauses
instead of going offline, never logs in beside the owner while paused, a member's command waits, an
owner command takes the box back, the backoff takes it back once the owner's session is gone),
`internal/adapter/router/ztef6600p/yield_test.go`, `internal/hub/relay/domain/status_test.go`, and
jest `ownerAtHome.test.tsx` (at home → local, away → relay, the switch both ways without an error
on screen, a member stays on the relay, a paused relay).

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
