# Mobile app

An app for **iOS (App Store) and Android (Google Play)** that manages the ZTE
box **directly from the phone on the home Wi-Fi — no server**. Decision record:
[RFC-004](../rfcs/RFC-004-standalone-mobile-app.md).

**Licensing (decision of 2026-11-21, [RFC-006](../rfcs/RFC-006-freemium-subscriptions.md#no-payment-in-the-app-decision-of-2026-11-21)).**
V1 is **one free Google Play build with no payment inside**: no price, offer,
"subscribe" button or link to pay. Passes are bought on the hub's web page
(`/licensing/v1/buy`) only; the app signs in with the WhatsApp number and reads
the account's entitlement. Locked actions show « Cette fonctionnalité n'est pas
disponible pour votre compte. »; the Account screen (`/account`) shows the tier,
its end date, box transfer and sign out. Demo mode opens everything.
`mobile/scripts/check-play-bundle.sh` (run by CI) fails on any payment wording
in the bundle. In-app purchase (Play Billing) may come back in V2.

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
| [RFC-004](../rfcs/RFC-004-standalone-mobile-app.md) | Why the Go core goes into the app, scope, stores | **v1** |
| [RFC-006](../rfcs/RFC-006-freemium-subscriptions.md) | Free app, tiers, accounts, passes bought on the web page only | **v1** |
| [development.md](development.md) | Stack, `mobile/` layout, local workflow, the bridge, checks | **v1** |
| [connexion-et-journal.md](connexion-et-journal.md) | Ce que l'audit du chemin réseau a trouvé, et le journal technique de l'app | **v1** |
| [release-signing.md](release-signing.md) | Signing script, GitHub secrets, CI and release workflows | **v1** |
| [release-playstore.md](release-playstore.md) | Store checklist (Play, with App Store notes), pre-submission compliance, console steps | **v1** |
| [relay-android.md](relay-android.md) | A phone left at home as the relay: the flow, the Wi-Fi check, one box session, battery / Doze, manual checks | **H3** |
| [play-data-safety.md](play-data-safety.md) | Play App content answers: Data safety, no in-app purchases, rating, account deletion | **v1** |
| [play-listing.md](play-listing.md) | Play listing copy in French, graphics, demo screenshots | **v1** |
| [milestones.md](milestones.md) | M0 → M3, state, risks | v1 + v2 |
| [RFC-003](../rfcs/RFC-003-mobile-app.md) | The server-backed design, now the optional v2 "hub" | v2 |
| [architecture.md](architecture.md), [security.md](security.md), [backend-prerequisites.md](backend-prerequisites.md) | Hub networking, pairing and roles, server changes | v2 |

## Principles carried over from the core

- The app never speaks the box protocol itself: the Go adapters do.
- Router endpoints stay `UNVERIFIED` until proven: an action the core answers
  with `501` is hidden or explained.
- Every write is reread from the box afterwards. A write that cannot be undone,
  or that touches this phone, is confirmed first, like `--yes` on the CLI:
  taking over the household, deleting the account, cutting the whole house,
  turning a network off, forgetting the box. The confirmation is the app's own
  dialog (`ui/ConfirmHost.tsx`, a Paper `Dialog`, not the system alert, whose
  « destructive » style Android ignores): its title names the action, one line
  says what follows, the action button is red when it cuts or erases, and
  « Annuler » is a text button. Giving something back (Internet for the house,
  a network turned on) is not asked. Frequent, reversible actions (blocking or
  authorising a device) are still confirmed until RFC-007's undo replaces them
  (#108). Deleting a notification, or all of them with « Tout effacer », is
  not asked either: a snackbar offers « Annuler » for a few seconds and puts
  them back (#125).
- No error shows the core's Go text, an HTTP code or the box's own message. An
  error the app cannot name says « Ça n’a pas marché. Réessayez. Si ça
  continue, signalez-le. » with a « Signaler un problème » button; the raw text
  goes to the journal (`core.note`) only.
- The router password is never returned to the UI, never logged, and is stored
  encrypted with a key kept in the Keychain / Keystore.
