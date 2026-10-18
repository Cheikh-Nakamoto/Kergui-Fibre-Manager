# Mobile app — security

> **v2 (hub).** This document describes the optional server-backed version.
> The v1 app is stand-alone and needs none of it: see
> [RFC-004](../rfcs/RFC-004-standalone-mobile-app.md).

Part of [Phase 4](README.md). Today the admin API has **no authentication**: it is
safe only because it listens on `127.0.0.1:8080` and the tunnel publishes nothing
but `^/paiement/`. The mobile app changes both facts, so this document is a hard
prerequisite, implemented in Milestone 4.0
([backend-prerequisites.md](backend-prerequisites.md)).

## Threat model

| # | Threat | Mitigation |
|---|---|---|
| T1 | A member (or a guest) on the same Wi-Fi calls admin endpoints | Every `/api/*` request needs a token with the `admin` scope; loopback trust is the only exception and never applies to tunnelled requests |
| T2 | Sniffing a token on the Wi-Fi | TLS everywhere (split-horizon + Let's Encrypt), clear text disabled in the app |
| T3 | Brute force or replay of a pairing code from the Internet | One-time codes, 5-minute expiry, 128-bit entropy, rate limit per IP, redeem endpoint the only unauthenticated route on the tunnel |
| T4 | Lost or stolen phone | Revocation from the dashboard / CLI; optional biometric unlock; token in Android Keystore-backed secure storage |
| T5 | Database leak (`data/kergui.db`) | Tokens stored as SHA-256 hashes; router credentials remain in the AES-256-GCM/argon2id vault |
| T6 | Internet exposure of sensitive admin routes | Tunnel ingress limited to `^/api/v1/` + `/.well-known/assetlinks.json`; `/api/v1/logs` and `/api/v1/config/env` refuse non-LAN requests |
| T7 | A member reads another member's data | Member scope only reaches `/api/v1/me/*`, which filters by the token's `member_id`; same rule as `/portail/api/me` today |
| T8 | Malicious deep link / QR | QR payload validated (host must match `KERGUI_APP_HOST`, scheme `https`), nothing executed from it |

## Pairing

### Issuing a code (admin side)

Allowed only from a trusted place:

- the dashboard on the kergui machine (loopback), panel "Téléphones appairés" →
  "Ajouter un téléphone" (admin) or, on a member's row, "Inviter dans l'app";
- the CLI: `kergui pair --role admin` or `kergui pair --role member --member <id>`
  (prints the QR in the terminal and the code as text);
- an already paired admin phone (`POST /api/v1/pair/codes`), to invite members.

The QR encodes a URL so that a phone camera also opens the app:

```
https://app.<domain>/pair#v=1&code=<base32, 26 chars>&sid=<server id>&r=admin
```

The fragment (`#…`) is never sent to any server. `sid` lets the app refuse a
code from another kergui instance than the one it is already paired with.

### Redeeming (app side)

`POST /api/v1/pair/redeem` (unauthenticated, rate-limited)

```json
{ "code": "…", "device_name": "Pixel 8 de Cheikh", "platform": "android" }
```

→ `200`

```json
{ "token": "kgt_…(43 chars)", "client_id": "…", "role": "member",
  "member_id": "…", "server_id": "…", "server_name": "Kergui — Maison" }
```

Rules:

- a code is **single use** and expires after **5 minutes**; redeeming burns it even
  on failure after validation;
- failure always answers the same `401 {"error":"invalid or expired code"}`;
- the token is 32 random bytes, shown once, stored server-side as
  `sha256(token)`;
- the token is long-lived (no expiry) but revocable, with `last_used_at`
  updated at most once per minute.

## Authorization

A middleware wraps `Server.Handler()`:

| Request | Rule |
|---|---|
| `/paiement/*`, `GET /` (dashboard), portal | unchanged |
| `POST /api/v1/pair/redeem` | public, rate-limited |
| any other `/api/*` from loopback **without** `Cf-Connecting-Ip` | allowed (the dashboard) — `--trust-loopback=true` by default, can be turned off |
| any other `/api/*` | `Authorization: Bearer kgt_…` required → `401` if missing/unknown/revoked |
| scope `admin` | every route |
| scope `member` | only `/api/v1/me`, `/api/v1/me/*` → otherwise `403` |
| LAN-only routes (`/api/v1/logs`, `/api/v1/config/env`, `/api/v1/pair/codes` for `role=admin`) | refused with `403` when the request came through the tunnel (`Cf-Connecting-Ip` present) |

Notes:

- `cloudflared` connects from `127.0.0.1`, so "loopback" alone is not a trust
  signal: the presence of `Cf-Connecting-Ip` marks a tunnelled request. Kergui
  must ignore that header on the LAN listener (strip it), since a LAN client
  could forge it — there it can only make a request *less* trusted, never more.
- Promoting a member phone to admin is not possible; issue a new admin code.

## Remote exposure (Cloudflare tunnel)

`deploy/cloudflared/config.yml` gains one rule **before** the catch-all:

```yaml
  - hostname: app.cheikhmodiouf.org
    path: ^/(api/v1/|\.well-known/assetlinks\.json$|pair$)
    service: http://127.0.0.1:8080
```

- Never publish `/` (the dashboard trusts loopback) or `/api/` without `v1`.
- Add a Cloudflare WAF rate-limiting rule on `/api/v1/pair/redeem`
  (e.g. 10 requests / minute / IP), in addition to the in-process limiter.
- Optional hardening for admin phones: Cloudflare Access service token
  (`CF-Access-Client-Id/Secret` headers) — not required, the bearer token already
  authenticates; keep it in reserve if the redeem endpoint is ever abused.

## App-side rules

- Token only in `expo-secure-store`; never in logs, crash reports, analytics or
  the Query cache persister.
- `android:usesCleartextTraffic="false"`, `allowBackup="false"` (the token must not
  follow a cloud backup to another phone).
- Optional "Verrouiller l'app" setting: `expo-local-authentication` on launch and
  after 5 minutes in background.
- Write actions (block, Wi-Fi password, close the month, lift a blacklist) always
  go through a confirmation dialog — the mobile equivalent of `--yes`.
- Wi-Fi passwords are write-only, as in the API: the app never displays one it did
  not just type.
- On `401`, wipe the token and the cached data, then go back to pairing.

## Revocation

- Dashboard: list of paired phones (name, role, member, last used) with "Révoquer".
- CLI: `kergui clients list`, `kergui clients revoke <id>`.
- Deleting a member revokes their phones; deactivating a member does not.
- Revocation is immediate (checked on every request; no cache).
