# Phase 4 — Mobile app

An Android app (Google Play) for Kergui Fibre Manager, used by the **admin** and
by **members**, on the home Wi-Fi and from anywhere. Status: **Proposed** — the
decision record is [RFC-003](../rfcs/RFC-003-mobile-app.md).

```
   Phone (Kergui app, Expo / React Native)
        │  https://app.<domain>/api/v1/…   Bearer <device token>
        │
        ├── at home ──► kergui LAN DNS answers 192.168.1.x ──► kergui :443 (Let's Encrypt cert)
        │
        └── outside ──► public DNS ──► Cloudflare tunnel ──► kergui 127.0.0.1:8080
                                                              │
                              Clean Architecture core (use cases, SQLite)
                                                              │
                                                     ZTE box 192.168.1.1
   Push: kergui ──► Expo Push ──► FCM ──► phone
```

## Documents

| Document | What it answers |
|---|---|
| [RFC-003](../rfcs/RFC-003-mobile-app.md) | Why Expo / React Native, the options rejected, research notes and sources |
| [architecture.md](architecture.md) | System view, networking (split-horizon DNS, TLS), app structure, state, key flows |
| [security.md](security.md) | Threat model, QR pairing, device tokens, roles, remote exposure rules |
| [backend-prerequisites.md](backend-prerequisites.md) | Every Go change the app needs, package by package (Milestone 4.0) |
| [development.md](development.md) | Frozen stack, `mobile/` layout, local workflow, tests, CI |
| [release-playstore.md](release-playstore.md) | Google Play checklist: account, signing, policies, Data safety, tracks |
| [milestones.md](milestones.md) | 4.0 → 4.4, exit criteria, estimates, risks |

## Roles

| Role | Who | Can |
|---|---|---|
| `admin` | Owner of the box | Everything the dashboard does: devices, block/unblock, Wi-Fi networks, MAC filter, members, prices, invoices, payments, logs (LAN only), paired phones |
| `member` | A `domain.Member` (relative or external) | See **only** their own devices, usage, invoices and payments; pay; receive reminders |

A phone gets a role by scanning a pairing QR code produced by the admin
([security.md](security.md#pairing)). There are no passwords in the app, and the
router password never leaves the server.

## Principles carried over from the core

- The app is a client of the use cases; it never talks to the router.
- Router endpoints stay `UNVERIFIED` until proven: the app hides an action the
  server answers with `501`.
- Writes require an explicit confirmation, like `--yes` on the CLI.
- Credentials are never returned to a client, never logged.
