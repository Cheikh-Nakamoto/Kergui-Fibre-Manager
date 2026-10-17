# Mobile app — architecture

Part of [Phase 4](README.md). Decision record: [RFC-003](../rfcs/RFC-003-mobile-app.md).

## 1. System view

```
┌──────────── Phone ─────────────┐
│ Kergui app (Expo / RN, TS)     │
│  secure-store: device token    │
└──────────────┬─────────────────┘
               │ HTTPS  app.<domain>   Authorization: Bearer <token>
       ┌───────┴────────┐
  on the LAN        outside the LAN
       │                 │
 kergui DNS :53     public DNS → Cloudflare edge
 app.<domain> →          │  tunnel ingress: app.<domain>, path ^/api/v1/
 192.168.1.x             ▼
       │            cloudflared ─► 127.0.0.1:8080
       ▼                                  │
 kergui :443 (SNI proxy)                  │
  SNI = app.<domain> → local TLS ─────────┤
  SNI = payment site → relay (unchanged)  │
                                          ▼
                 httpapi: auth middleware → use cases → SQLite
                                          │
                                ZTE box 192.168.1.1 (RouterPort)

 Push:  use case event → port.Notifier → Expo Push API → FCM → phone
```

### One hostname, split-horizon DNS

The app knows a single base URL, `https://app.<domain>` (e.g.
`app.cheikhmodiouf.org`, next to the existing `pay.cheikhmodiouf.org`).

- **On the LAN**, kergui is already the network's DNS
  (`internal/adapter/captive/dns.go`). It answers `app.<domain>` with its own LAN
  address, so traffic stays local and works even when the Internet link is down.
- **Outside**, public DNS resolves `app.<domain>` to Cloudflare, and the tunnel
  forwards to `127.0.0.1:8080`.
- **TLS on the LAN**: `:443` on the LAN address is already taken by the walled
  garden `captive.SNIProxy` (`internal/adapter/captive/sni.go`). The proxy gains
  one rule: when the SNI is `app.<domain>`, hand the connection to an in-process
  TLS listener serving the API with a **Let's Encrypt certificate obtained by
  DNS-01** through the Cloudflare API. Every other name keeps today's behaviour.
- Result: a valid public certificate on both paths, `usesCleartextTraffic=false`,
  no certificate pinning, no LAN discovery needed, no "switch to remote" button.
- Phones with Android **Private DNS** enabled ignore the LAN resolver and simply go
  through the tunnel: slower, still correct.
- mDNS (`_kergui._tcp`) is a possible later addition, not required.

### Why not plain HTTP on the LAN

Members — including paying externals — share the Wi-Fi. A bearer token sent in
clear would be readable by any of them. Android also blocks clear text by default
since API 28, and a `network_security_config` cannot whitelist IP ranges.

## 2. App structure

Expo Router (file-based), one route group per role. Screens mirror the dashboard
tabs of `internal/webui/assets/index.html`.

```
mobile/
  app/
    _layout.tsx               providers (Query, theme, i18n), session gate
    (auth)/
      welcome.tsx             explain + "Scan a pairing code"
      pair.tsx                camera QR scanner, redeem, store token
    (admin)/
      _layout.tsx             bottom tabs
      devices/index.tsx       tiles + list (search, filter, sort)       ← "Appareils"
      devices/[mac].tsx       details, rename, block/unblock, member
      wifi.tsx                SSIDs: on/off, name, password            ← "Réseaux Wi-Fi"
      filter.tsx              MAC filter toggle + list                  ← "Filtrage MAC"
      members/index.tsx       members + invoices                        ← "Membres & factures"
      members/[id].tsx
      billing.tsx             usage history, prices, payments           ← "Historique & paiements"
      settings/index.tsx      server, router credentials test, paired phones
      settings/logs.tsx       journal (LAN only)                        ← "Journal"
    (member)/
      _layout.tsx
      home.tsx                amount due, status (ok / reminded / cut), Pay
      usage.tsx               this month's usage per device, chart
      invoices.tsx            past invoices and payments
  src/
    api/          generated schema.d.ts + client.ts (openapi-fetch, auth, base URL)
    features/     one folder per domain: devices, wifi, filter, members, billing, me, pairing
                  each = query/mutation hooks + view models + screens' components
    session/      zustand store: role, server id, member id; token in secure-store
    ui/           design system on react-native-paper (Material 3), charts
    i18n/         fr.json (default), en.json
    lib/          formatting (bytes "1,2 Go", XOF), MAC helpers, errors
```

### Layering rule (mirrors the Go Dependency Rule)

`app/` (screens) → `src/features` → `src/api` → network. Screens never call
`fetch`; `src/api` is the only module that knows URLs and headers; `src/ui` knows
nothing about the API. An ESLint `no-restricted-imports` rule enforces it, the
same way `tests/architecture_test.go` enforces the Go layers.

## 3. State and data

| Concern | Tool | Notes |
|---|---|---|
| Server data | TanStack Query v5 | per-resource keys (`['devices']`, `['wifi','ssids']`…), retries, background refetch on focus |
| Offline read | Query persister (MMKV / AsyncStorage) | last known list shown with an "as of" time when the server is unreachable |
| Session | zustand + `expo-secure-store` | token only in secure-store (Android Keystore-backed) |
| Optimistic writes | none for router writes | block/unblock/Wi-Fi wait for the server, which rereads the box (`read-after-write`) |
| Polling | `refetchInterval` on the devices screen only while visible | replaces the dashboard's manual refresh |

Bytes and money follow the dashboard's formatting (`fmtBytes` in
`internal/webui/assets/app.js`: `1,25 Go`, `850 Mo`; amounts in F CFA).

## 4. Key flows

### Pairing

```
Admin (dashboard on the kergui machine or `kergui pair`) ──► QR {url, code, exp, server_id, role}
App scans ──► POST /api/v1/pair/redeem {code, device_name, platform}
Server   ──► {token, role, member_id?, server_name}   (code burnt, token stored hashed)
App      ──► secure-store, route to (admin) or (member)
```

Details and rules: [security.md](security.md#pairing).

### Block a device (admin)

1. Device screen → "Bloquer" → confirmation dialog naming the device.
2. `POST /api/v1/devices/{mac}/block`.
3. `200` → invalidate `['devices']`; `501` → hide the action for this model and
   show "not supported on this box yet"; `502` → "box unreachable".

### Pay an invoice (member)

1. Home shows the amount due from `GET /api/v1/me`.
2. "Payer" → `POST /api/v1/me/payments` (reuses the billing use case; returns the
   existing pending payment if there is one) → `checkout_url`.
3. `expo-web-browser` opens the NabooPay checkout in a Custom Tab.
4. NabooPay redirects to `https://pay.<domain>/paiement/merci`; the webhook
   (`POST /paiement/webhook`, already signed and verified) marks it paid.
5. The server pushes "Paiement reçu"; the app refetches `['me']`. On return from
   the Custom Tab the app also refetches, in case push is disabled.

The app never sees card or mobile-money data.

### Push notifications

| Event (existing source) | To | Message |
|---|---|---|
| New device first seen (`DeviceView.NewlySeen`) | admin | "Nouvel appareil : <vendor / hostname>" |
| Webhook marks a payment paid (`httpapi/webhook.go`) | admin + payer | "Paiement reçu : 2 500 F" |
| Payment reminder due (`reminder_repo.go`) | member | "Votre facture d'octobre : 3 000 F" |
| Enforcement cuts a member (`usecase/enforce.go`) | admin + member | "Accès suspendu — facture impayée" |
| Router unreachable for N minutes | admin | "La box ne répond plus" |

Tapping a notification deep-links to the relevant screen
(`kergui://devices/<mac>`, `kergui://me`).

## 5. Error mapping

Status codes come from [docs/api.md](../api.md#status-codes).

| Code | App behaviour |
|---|---|
| `401` | token revoked or unknown → clear session, back to pairing |
| `403` | role not allowed (member hitting admin route) → should not happen; report |
| `404` | resource gone → refetch the list |
| `422` | router model not identified → admin banner pointing to Settings |
| `501` | feature not wired for this box → hide the action, explain once |
| `502` | box unreachable → banner, keep cached data |
| network error | server unreachable → offline banner with last refresh time |

## 6. Internationalisation and accessibility

- French is the default and the only complete locale at launch; strings live in
  `src/i18n/fr.json`, no hard-coded text in components.
- Dynamic type, TalkBack labels on every icon button, 48 dp touch targets,
  light/dark themes from the system setting.
