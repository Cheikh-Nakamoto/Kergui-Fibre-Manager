# Phase 4 — milestones

Part of [Phase 4](README.md). Estimates assume one developer, part time on the app
and the server together; they are ranges, not commitments. Each milestone ends
with something usable and its exit criteria checked.

| Milestone | Scope | Exit criteria | Estimate |
|---|---|---|---|
| **4.0 Backend prerequisites** | [backend-prerequisites.md](backend-prerequisites.md): pairing, tokens, roles, `/api/v1`, OpenAPI spec + contract test, LAN TLS through the SNI proxy, tunnel ingress for `app.<domain>`, CLI `pair`/`clients`, dashboard "Téléphones appairés" | §9 of that document: `401` / `403` / `200` matrix verified through the tunnel and on the LAN; dashboard unchanged; `make check` green | 2–3 weeks |
| **4.1 Admin MVP (LAN)** | `mobile/` scaffold, design system, pairing screen, devices (list, details, rename, block/unblock), Wi-Fi networks, MAC filter, settings (server, revoke this phone) | Admin manages the real box from their phone on the home Wi-Fi; internal-track build installed; Jest + Maestro flows green against the mock router | 3–4 weeks |
| **4.2 Remote + push** | Same URL from 4G (tunnel), offline cache + banners, `expopush` adapter, notifications for new device / payment / router down, deep links | Admin receives "Nouvel appareil" on 4G within a minute; app usable read-only when the server is down | 2 weeks |
| **4.3 Member space** | Invite flow, member home (amount due, status), usage chart, invoices, pay via NabooPay Custom Tab, reminders and cut notices by push; admin members/billing screens | A real member pays an invoice from the app and the admin sees it marked paid without refreshing | 2–3 weeks |
| **4.4 Google Play release** | [release-playstore.md](release-playstore.md): listing, privacy policy, Data safety, demo server for reviewers, closed test, staged production rollout | App public on Google Play; vitals under thresholds after one week | 3 weeks (14 days are the closed test) |

Total: roughly **12–15 weeks**. 4.4's closed test can start as soon as 4.3 is on
the closed track — features land as updates during the 14 days.

## Dependencies

```
4.0 ──► 4.1 ──► 4.2 ──► 4.3 ──► 4.4
          └── closed test can start here with admin-only testers if needed
```

- 4.0 blocks everything: no app build may talk to an unauthenticated API.
- 4.3 needs NabooPay in production mode and `KERGUI_PUBLIC_URL` already set (both exist).
- 4.4 needs the Play developer account created early (identity verification can
  take days) — open it during 4.1.

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Router endpoints still `UNVERIFIED` on some models | actions fail on real boxes | the app shows only what the server allows (`501` hides the action); validate on the F6600P first |
| Fewer than 12 testers stay opted in for 14 days | production delayed | recruit 15–20; or use an organization account |
| Let's Encrypt / Cloudflare DNS-01 misconfiguration | LAN path fails | the app silently uses the tunnel path; health check in Réglages shows the cause |
| Phones with Private DNS bypass kergui's DNS | always remote path | acceptable; documented in the app's help |
| Android 17 local-network permission refused | no LAN path | fall back to the tunnel; explain once |
| Play policy re-reading payments as digital goods | rejection | review notes explain the real-world service; NabooPay stays in the browser |
| Expo SDK upgrade churn (yearly target SDK bump) | maintenance | one upgrade PR per SDK, Maestro flows as safety net |
| Single maintainer | bus factor | this documentation, the OpenAPI contract and CI |

## Out of scope for Phase 4

- iOS build (kept possible, see [release-playstore.md](release-playstore.md#9-ios-out-of-scope-noted)).
- Parental control screens — they follow [RFC-002](../rfcs/RFC-002-parental-control.md)
  once its backend exists.
- Managing several kergui servers from one app (the session holds one server).
- Wolof translation (structure ready, strings later).
