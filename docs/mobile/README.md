# Mobile app

An app for **iOS (App Store) and Android (Google Play)**, **sold once** (paid
app, nothing to buy inside), that manages the ZTE box **directly from the phone
on the home Wi-Fi — no server**. Decision record:
[RFC-004](../rfcs/RFC-004-standalone-mobile-app.md).

```
   Phone (Kergui app: Expo / React Native)
     │  KerguiCore.request("GET", "/api/devices")      in memory, no socket
     ▼
   Go core (gomobile): same use cases and JSON API as `kergui serve`,
   SQLite inventory, encrypted credentials, mock router for demo mode
     │  HTTP(S) on the LAN, self-signed certificate pinned on first use
     ▼
   ZTE box 192.168.1.1
```

## Documents

| Document | What it answers | Version |
|---|---|---|
| [RFC-004](../rfcs/RFC-004-standalone-mobile-app.md) | Why the Go core goes into the app, scope, stores and selling | **v1** |
| [development.md](development.md) | Stack, `mobile/` layout, local workflow, the bridge, checks | **v1** |
| [release-signing.md](release-signing.md) | Signing script, GitHub secrets, CI and release workflows | **v1** |
| [release-playstore.md](release-playstore.md) | Store checklist (Play, with App Store notes) | **v1** |
| [milestones.md](milestones.md) | M0 → M3, state, risks | v1 + v2 |
| [RFC-003](../rfcs/RFC-003-mobile-app.md) | The server-backed design, now the optional v2 "hub" | v2 |
| [architecture.md](architecture.md), [security.md](security.md), [backend-prerequisites.md](backend-prerequisites.md) | Hub networking, pairing and roles, server changes | v2 |

## Principles carried over from the core

- The app never speaks the box protocol itself: the Go adapters do.
- Router endpoints stay `UNVERIFIED` until proven: an action the core answers
  with `501` is hidden or explained.
- Every write is confirmed in a dialog, like `--yes` on the CLI, and reread from
  the box afterwards.
- The router password is never returned to the UI, never logged, and is stored
  encrypted with a key kept in the Keychain / Keystore.
