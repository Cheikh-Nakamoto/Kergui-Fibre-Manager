# RFC-003 — Mobile app (Phase 4)

- Status: **Superseded** by [RFC-005](RFC-005-semi-distributed-hub.md) (central hub + relays) for
  v2; the v1 app is stand-alone, see [RFC-004](RFC-004-standalone-mobile-app.md). Kept for its
  research notes (Play policies, pairing threats).
- Depends on: the backend prerequisites in [mobile/backend-prerequisites.md](../mobile/backend-prerequisites.md)
  (the admin API has **no authentication today**)
- Relates to: [RFC-001](../../RFC-001-router-adapter.md) (core architecture),
  [RFC-002](RFC-002-parental-control.md) (parental control, future mobile screen)
- Detailed docs: [docs/mobile/](../mobile/README.md)

## Context

Kergui runs on an always-on machine of the LAN (`make up`, Docker, host
networking). It exposes:

- a JSON API + embedded vanilla-JS dashboard on `127.0.0.1:8080`
  (`internal/adapter/controller/httpapi/server.go`, `internal/webui`);
- a captive portal (DNS `:53`, portal `:80`, SNI walled garden `:443` on the LAN
  address — `internal/adapter/captive`);
- NabooPay return pages and webhook, published through the Cloudflare tunnel
  `pay.cheikhmodiouf.org` restricted to `^/paiement/`
  (`deploy/cloudflared/config.yml`).

Two kinds of people use it:

- the **admin** (owner of the box) manages devices, Wi-Fi networks, the MAC
  filter, members, prices, invoices and payments — today only from a browser on
  the kergui machine (the API is bound to loopback);
- **members** (relatives and paying externals, `domain.Member`) only see the
  captive portal when they owe money, and pay through a NabooPay link.

Phase 4 adds an **Android app, published on Google Play**, used by both roles,
reachable **on the home Wi-Fi and from outside** (4G, another network).

### Requirements

| # | Requirement |
|---|---|
| R1 | One app, two roles: `admin` (everything the dashboard does) and `member` (own usage, own invoices, pay). |
| R2 | Works on the LAN and remotely, with the same URL and no manual switch. |
| R3 | No secret ever crosses the network in clear text; members on the same Wi-Fi cannot call admin endpoints. |
| R4 | Publishable on Google Play under 2026–2027 policies (target SDK, Data safety, payments, brand). |
| R5 | Push notifications (new device, payment received, invoice, reminder). |
| R6 | French first; i18n-ready. |
| R7 | Keep the Go core's principles: Clean Architecture, credentials never returned to clients, writes require confirmation, router endpoints stay `UNVERIFIED` until proven. |
| R8 | iOS possible later without a rewrite (not in scope). |

## Options considered

| Criterion | **Expo / React Native** | Capacitor (WebView) | TWA / PWA | Flutter |
|---|---|---|---|---|
| UI | Native views (Material 3) | HTML in a WebView | Chrome tab | Own renderer (Impeller), native-looking |
| Reuse of the existing dashboard | None (it is imperative DOM code, same-origin, no auth) | Low: the dashboard must be rewritten anyway (base URL, auth, routing) | Medium, but requires exposing the full dashboard publicly | None |
| Impact on the Go side | None beyond the API | A rewritten React dashboard would bring a JS build into a binary whose principle is "no build step" | Cookie auth, public dashboard, CSRF | None beyond the API |
| Secure token storage, biometrics, QR camera, push | First-party Expo modules | Plugins | Web APIs only (no Keystore) | Plugins |
| Local-network permission (Android 17) | Config plugin / manifest | Manifest | Not controllable | Manifest |
| Google Play risk | Low | "Webview spam / minimum functionality" scrutiny if it feels like a wrapped site | Accepted, but the app *is* the website | Low |
| Typed API client from OpenAPI | `openapi-typescript` (shared with any future web TS) | Same | Same | Separate Dart generator |
| Language / skills | TypeScript + React (already used by the maintainer) | Same | Same | Dart (new) |
| Build & store tooling | EAS Build / Submit / Update | Gradle by hand or Appflow | Bubblewrap | Gradle / Codemagic |
| iOS later | Same codebase | Same codebase | Limited | Same codebase |

## Decision

Build a **native cross-platform app with Expo (React Native) and TypeScript**,
in a new top-level `mobile/` directory of this repository.

- The **OpenAPI 3.1 spec** (`docs/openapi.yaml`) becomes the contract between the
  Go server and the app; TypeScript types are generated from it.
- The server gains **device tokens with roles** issued by **QR pairing**, and a
  `/api/v1` prefix.
- One public hostname (`app.<domain>`, e.g. `app.cheikhmodiouf.org`) with
  **split-horizon DNS**: kergui's own LAN DNS answers its LAN address, public DNS
  points to the Cloudflare tunnel. A real Let's Encrypt certificate is served on
  both paths, so the app never uses clear text and never pins a certificate.
- Push goes through the **Expo Push service** behind a `port.Notifier`, so FCM
  direct can replace it without touching use cases.

Capacitor stays the documented **plan B** if time-to-market ever dominates; it
would require the same backend work.

## Consequences

- **Backend first**: nothing ships before Milestone 4.0 (auth, roles, TLS,
  tunnel ingress). This also closes today's gap: the admin API is unauthenticated.
- A JavaScript toolchain enters the repository, confined to `mobile/`; the Go
  binary, the dashboard and `make build` stay dependency-free.
- `docs/api.md` is superseded by `docs/openapi.yaml` as the source of truth
  (`api.md` keeps the human-readable overview).
- New recurring costs: Google Play developer fee (25 USD once), an Expo account
  (free tier is enough to start), the domain already exists.
- A Google Play **closed test with ≥ 12 testers for 14 consecutive days** is on the
  critical path if the developer account is personal.

## Research notes (October 2026)

- **Target SDK**: since 31 August 2026, new apps and updates on Google Play must
  target **Android 16 (API 36)**; extension possible to 1 November 2026. The
  yearly bump to API 37 is expected in 2027.
- **Local network protection (Android 17 / API 37)**: apps targeting API 37 have
  LAN access blocked by default and must declare and request
  **`ACCESS_LOCAL_NETWORK`**. Google advises not requesting it at runtime before
  targeting 37. DNS on port 53 is exempt. Our LAN path (split-horizon to
  `192.168.x.x`) falls under it.
- **New personal developer accounts** (created after 13 Nov 2023) must run a
  closed test with **12 opted-in testers for 14 continuous days** before applying
  for production. Organization accounts (D-U-N-S number) are exempt.
- **Developer verification for sideloading**: from 30 September 2026 in four
  countries (Brazil, Indonesia, Singapore, Thailand), global on certified devices
  in 2027. APKs distributed outside Play (e.g. GitHub Releases) will need a
  verified developer identity; ADB installs stay exempt.
- **WebView wrappers**: Google treats apps that are just webviews of a website as
  spam / minimum-functionality violations — one more reason against a thin wrapper.
- **Payments**: members pay for internet access consumed on the LAN, a real-world
  service, so Google Play Billing does not apply; the NabooPay checkout opens in a
  Custom Tab. Re-check the Payments policy at submission time.
- **Brand**: the listing must not use the Orange name or logo (impersonation
  policy); describe compatibility with "ZTE F6600P / F660 boxes" instead.
- **Expo**: SDK 56 (May 2026) ships React Native 0.85 and React 19.2 with Hermes V1
  by default; SDK 57 fixes a Hermes V1 memory regression and is the recommended
  baseline.
- **If plan B**: Capacitor Android/iOS had a critical remote-content issue
  (CVE-2026-103922), fixed in 8.3.5 / 8.4.3 / 8.5.1 — pin a fixed version.

## Implementation plan (phased)

See [mobile/milestones.md](../mobile/milestones.md): 4.0 backend prerequisites →
4.1 admin MVP on the LAN → 4.2 remote access + push → 4.3 member space and
payment → 4.4 Google Play release.

## Sources

- Google Play target SDK requirements — https://developer.android.com/google/play/requirements/target-sdk
- Local network permission — https://developer.android.com/privacy-and-security/local-network-permission
- Closed-testing requirement for new personal accounts — https://support.google.com/googleplay/android-developer/answer/14151465
- Android sideloading / developer verification timeline — https://www.androidauthority.com/android-sideloading-changes-timeline-3679204/
- Common policy issues (webview spam, minimum functionality) — https://android-developers.googleblog.com/2020/10/developer-tips-and-guides-common-policy.html
- Expo SDK 56 changelog (and SDK 57 note) — https://expo.dev/changelog/sdk-56
- React Native vs Expo vs Capacitor, 2026 — https://www.pkgpulse.com/guides/react-native-vs-expo-vs-capacitor-cross-platform-mobile-2026
- Capacitor CVE-2026-103922 — https://advisories.gitlab.com/npm/@capacitor/android/CVE-2026-103922/
