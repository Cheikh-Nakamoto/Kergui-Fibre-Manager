# RFC-006 — Freemium app with Pro subscriptions

- Status: **Proposed** (decisions settled in a design interview, 2026-11-21)
- Supersedes: the "Stores and selling" section of [RFC-004](RFC-004-standalone-mobile-app.md)
  (S1 paid app sold once, S2 no subscription). The app becomes free to install with paid passes.
  Also replaces the APK activation key added in `6127445`.
- Builds on: [RFC-005](RFC-005-semi-distributed-hub.md) — the central hub hosts licensing; relays
  run the auto-reject rule.

## Context

The next step of the mobile app: anyone can install it and connect to their box, but the actions
that change the box are locked behind a monthly subscription. The app goes on Google Play, and
payment happens **outside the app** so that Google Play's billing fee does not apply.

Three facts from the code shape the design:

- The Go core runs inside the app with no server (RFC-004), and the repository is source-available.
  A client-only lock can be removed by anyone who rebuilds the app, so the hub must be the
  authority.
- The hub (RFC-005) already has Postgres, WhatsApp delivery (Evolution) and owner keys; NabooPay
  (Wave / Orange Money) is already integrated in `internal/usecase/billing.go`.
- The core can already meter traffic (`metering.go`), manage SSIDs and the Wi-Fi password
  (`port.WiFiSSIDManager`), cut access (`enforce.go`, `wifi.go`) and watch for new devices every
  30 s (Android `WatchService`).

## Decision

### Tiers

| Tier | Price | Features |
|---|---|---|
| **Free** | 0 | Connect to the box, device list, local rename, journal, new-device alerts |
| **Basic** | 650 F / 30 days | Free + block, unblock, cut |
| **Max** | 2 000 F / 30 days | Basic + consumption, consumption history, monthly consumption filter per device, active SSID management, Wi-Fi password change, auto-reject of unapproved devices after 15 min |

Max is a superset of Basic. Alerts stay free: they create the need to block, so they drive
conversion.

### Passes, not recurring billing

Mobile money has no reliable recurring debit, so subscriptions are **prepaid passes**:

| Duration | Basic | Max |
|---|---|---|
| 1 month (30 days) | 650 F | 2 000 F |
| 3 months (-5 %) | 1 850 F | 5 700 F |
| 12 months (= 10 months) | 6 500 F | 20 000 F |

- **Trial**: 7 days of Max, once per phone number **and** once per box.
- **Upgrade Basic → Max** mid-pass: the remaining Basic days are converted pro rata
  (650 → 2 000) into Max days.
- **Reminder**: WhatsApp message 3 days before expiry, with a NabooPay link.
- **Refunds**: none by default; handled case by case through support.

### Google Play compliance: "existing account" model

Google Play requires Play Billing to unlock digital features in an app and forbids steering users
to another payment method (links, buttons, prices, "pay on our website"). The app therefore follows
the multi-platform "existing account" pattern:

- In the Play build, locked features show a neutral **"Disponible avec un compte Pro"** state and an
  **"Activer mon compte Pro"** sign-in. No price, no purchase link, no mention of where to pay.
- Selling happens outside the app: a purchase page served by the hub, WhatsApp, word of mouth.
- Signing in from the app is allowed; buying from the app is not.
- Fallback if conversion is too low: Play Billing on the Play build (15 % on subscriptions), with
  NabooPay kept for the direct APK.

Launch scope: **Android only** (Google Play + direct APK). iOS comes later under similar App Store
rules, and with background watch limits.

### Accounts

- An account is a **phone number**, verified by a one-time code sent over **WhatsApp** (the
  existing Evolution adapter), entered in the app.
- An account holds **one subscription per box**. Several phones (the household) can sign in to the
  same account and drive that box.
- **Box transfer** (broken or replaced box): the owner re-binds the pass to another box from the
  app, at most **2 transfers per 30 days**; beyond that, through support.

### Entitlement token

A new `licensing` module in `internal/hub`, on the central hub instance that the project runs.

- After payment (NabooPay webhook) the hub credits the pass to the account of the phone number
  given on the purchase page.
- The hub issues a **signed entitlement token** (Ed25519): `{account, box_id, tier, expires_at,
  issued_at}`. The signing key is kept out of the repository; the public key ships in the app.
- The app verifies the token **offline**. It is valid until the pass expiry, at most 30 days per
  token, with a **3-day grace** without network; the app refreshes it whenever it has network.
- Accepted risk: a fork of the source can remove the gate. The target is the ordinary user, not a
  determined attacker.
- The `kergui` CLI and the web dashboard (`kergui serve`) stay **free and ungated**: they are
  self-hosted technical tools. Only the consumer mobile app is gated.
- The activation key from `6127445` (SHA-256 in the APK) is **removed**: the free tier needs
  nothing, Pro goes through the account.

### Expiry

- Rules already written to the box **stay in place**.
- Paid screens freeze and auto-reject stops.
- **Unblocking stays free forever**, so a user is never locked out by their own rule.

### Auto-reject after 15 minutes (Max)

Enabled from Settings.

- Devices known when the feature is enabled count as **accepted**.
- A new device must be **approved from its alert notification within 15 min**; otherwise it is
  refused at the **Wi-Fi access control** (`port.WiFiAccessControl`), not the firewall MAC filter.
- **Who enforces it**: the RFC-005 relay agent when it has sent a heartbeat to the hub in the last
  2 minutes; otherwise the phone's `WatchService` takes over. This avoids two admin sessions
  knocking each other off the box. The agent receives the entitlement token too and checks the
  tier before acting.
- Limit: without an agent, enforcement only runs while a phone is on the box's Wi-Fi.

## Milestones

1. **Licensing**: hub `licensing` module (accounts, WhatsApp OTP, passes, NabooPay webhook, token
   signing), purchase page, app gating + "Activer mon compte Pro" screen, removal of the
   activation key.
2. **Auto-reject**: phone `WatchService` first, then the relay agent with leader election by
   heartbeat.
3. **Google Play listing**: privacy policy (phone number collected), Data safety form, closed test
   (≥ 12 testers for 14 days, see RFC-003), review of in-app wording against the steering rule.

## Open questions

- **Box identity.** `RouterID` is currently derived from the base URL (`192.168.1.1`), the same in
  every household, so a pass binds to the box's **serial number** instead.
  - **F6600P — verified** on a real box (capture of 2026-10, firmware `ZTEGF660006SN`): the
    `router_info` endpoint the adapter already calls (`devmgr_statusmgr_lua.lua`, marked
    `Verified`) returns `OBJ_DEVINFO_ID.SerialNumber` (format `ZTEG` + 8 hex, e.g.
    `ZTEGXXXXXXXX`). The same value also appears in `OBJ_SN_INFO_ID.Sn` with a lower-case hex part,
    so the identifier is normalised to upper case. The fixture `zte_f6600p/status.xml` already
    carries it (`ZTEG00000001`), and the adapter only needs to parse it.
  - **F660, F680, Funbox — pending**: added model by model as the boxes become available. Until a
    model exposes its serial, Pro cannot be bound on it. The app shows "modèle pas encore pris en
    charge pour Pro", and the free tier works normally.
- Direct-APK distribution is affected by Android developer verification in 2027 (see RFC-003).

## Consequences

- The app becomes free to install; revenue moves to passes sold outside the store.
- The hub becomes a production dependency for Pro (not for Free): it must be hosted, monitored and
  backed up, and the signing key must be protected and rotatable.
- New personal data: account phone numbers, stored by the hub.
- In-app wording must be reviewed for every release so that it never points to the payment page.
