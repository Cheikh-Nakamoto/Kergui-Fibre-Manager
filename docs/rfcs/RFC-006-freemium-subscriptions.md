# RFC-006 — Freemium app with Pro subscriptions

- Status: **Proposed** (decisions settled in a design interview, 2026-11-21)
- Amended: 2026-11-21 — **V1 is the Google Play app without payment**; passes are bought on the
  hub's web page only (see "No payment in the app"). The two build flavors are dropped.
- Supersedes: [PLAN-premium-paiement](../plans/PLAN-premium-paiement.md) where they disagree (licensing in
  the hub with accounts rather than a separate license server; tiers; auto-reject rule). The plan's
  F1 "Couper l'accès" and F5 consumption groundwork still apply.
- Supersedes: the "Stores and selling" section of [RFC-004](RFC-004-standalone-mobile-app.md)
  (S1 paid app sold once, S2 no subscription). The app becomes free to install with paid passes.
  Also replaces the APK activation key added in `6127445`.
- Builds on: [RFC-005](RFC-005-semi-distributed-hub.md) — the central hub hosts licensing; relays
  run the auto-reject rule.

## Context

The next step of the mobile app: anyone can install it and connect to their box, but the actions
that change the box are locked behind a monthly subscription. The app goes on Google Play, and
payment uses **Wave / Orange Money** on the hub's web page, outside the app (see "No payment in
the app").

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
| **Free** | 0 | Connect to the box, device list, local rename, journal, new-device alerts, **current-month consumption**, turning a network back **on**, every "give access back" action (unblock, restore Wi-Fi access) |
| **Basic** | 650 F / 30 days | Free + block, cut Wi-Fi access |
| **Max** | 2 000 F / 30 days | Basic + consumption history, monthly consumption filter per device, SSID management (turn off, rename), Wi-Fi password change, auto-reject of unaccepted devices after 15 min |

Max is a superset of Basic. Alerts and the current month's consumption stay free: they create the
need to block, so they drive conversion. Anything that **gives access back** is free forever.

### Passes, not recurring billing

Mobile money has no reliable recurring debit, so subscriptions are **prepaid passes**:

| Duration | Basic | Max |
|---|---|---|
| 1 month (30 days) | 650 F | 2 000 F |
| 3 months (-5 %) | 1 850 F | 5 700 F |
| 12 months (= 10 months) | 6 500 F | 20 000 F |

- **Trial**: 7 days of Max, once per phone number **and** once per box. It is asked for on the
  hub's web page, never from the app (see "Trial from the web page").
- **Upgrade Basic → Max** mid-pass: the remaining Basic days are converted pro rata
  (650 → 2 000) into Max days.
- **Reminder**: WhatsApp message 3 days before expiry, with a NabooPay link.
- **Refunds**: none by default; handled case by case through support.

### No payment in the app (decision of 2026-11-21)

Google Play requires Play Billing to sell digital features in a Play-distributed app and forbids
steering users to another payment method. The 2026 external-payment programmes cover the US, the
UK, the EEA and Japan, not Senegal. So **V1 is one free Google Play build with no payment surface
at all**:

- **One build.** No build flavor (`KERGUI_FLAVOR` is gone), no Play Billing (`expo-iap` and the
  `com.android.vending.BILLING` permission are removed; the permission is blocked in the
  manifest), no NabooPay code in the app.
- **Purchase on the web only.** Passes (and the trial) are bought on the hub's page
  `GET /licensing/v1/buy`, for a phone number. The app only signs in with that number (WhatsApp
  code) and reads the account's entitlement token.
- **No steering.** The app shows no price, offer, feature pitch, "subscribe" or trial button, and
  no text or link that points to a website to pay. The former Pro screen is an **Account** screen
  (route `/account`): signed-in number, current tier and end date, grace notice, box transfer,
  refresh, sign out.
- **Locked actions** show one neutral sentence, « Cette fonctionnalité n'est pas disponible pour
  votre compte. », and open no other screen.
- **Demo mode** keeps everything open, so store reviewers see every feature.
- **Guard.** `mobile/scripts/check-play-bundle.sh` builds the Android JS bundle and fails on any
  payment wording (`NabooPay|Wave|Orange Money|FCFA|XOF|prix|abonne|payer|acheter|achat|s'abonner|site
  web|https?://…buy`); CI runs it on every change.

**V2 (maybe):** in-app purchase may come back through Play Billing. The hub's Play code
(purchase verification, Real-time Developer Notifications) stays in place and is off as long as
`KERGUI_HUB_PLAY_PACKAGE_NAME` is unset.

The earlier plan of **two build flavors** (`play` with Play Billing, `naboopay` as a direct APK
with NabooPay checkout in the app) is superseded by this decision.

Launch scope: **Android only**. iOS comes later under similar App Store rules, and with
background watch limits.

### Trial from the web page (decision of 2026-11-21)

- The purchase page `GET /licensing/v1/buy` has a « 7 jours d'essai Max » form that takes a phone
  number alone, with no sign-in (as for a purchase, anyone can ask for a number). It posts to
  `POST /licensing/v1/buy` (`offer=trial`), so the page's CSRF token and rate limit apply.
- No box is known at that point, so the trial is recorded **pending** on the number's account
  (created if need be), and a WhatsApp message confirms it. The page refuses a second request
  for the same number (`TRIAL_PHONE_USED`); unlike a purchase, it does say so, since it is the one
  thing the person can act on and it reveals nothing about a box.
- The app has no trial button and no trial wording. When it signs in on a box it calls
  `POST /licensing/v1/bind {boxId}` (authenticated, idempotent, on every token refresh) and then
  `POST /licensing/v1/token`. The bind ties the pending trial to that box serial.
- **The 7 days start at the binding, not at the request**: otherwise a trial asked for on the web
  and only used weeks later would have expired without serving.
- **Once per box** is checked at the binding: if the box already had a trial (any account's, or a
  Play free trial), the binding still succeeds, the answer says `"trial": "refused",
  "trialCode": "TRIAL_BOX_USED"`, the account stays as it was on that box (Free), and the trial
  keeps waiting for a box that never had one. `hub_licensing_trials` keeps `UNIQUE (phone)` and
  `UNIQUE (box_id)`; a pending row has no `box_id` and no `started_at`.
- The authenticated `POST /licensing/v1/trial` the app used to call is removed.

### Pass from the web page (decision of 2026-11-21)

- The purchase form takes a phone number, a tier and a duration; the box serial is **optional**.
  With a serial, the pass is credited to that box at the webhook, as before.
- Without one, the paid order is recorded on the number's account with no box
  (`hub_licensing_payments.box_id` NULL) and **waits**, like the web trial. The bind that the app
  makes on every token refresh applies the waiting passes to its box, in the order they were paid,
  after the trial; their days start at the binding. The bind answer says how many (`"passes"`).
  A payment is bound once; a box that refuses the trial (`TRIAL_BOX_USED`) still gets the passes.
- Each credit is confirmed over WhatsApp to the number: until when it runs on its box, or that it
  waits for the app. A message that cannot go out does not undo the credit. Play renewals are not
  confirmed (Play tells the buyer).

### Accounts

- An account is a **phone number**, verified by a one-time code sent over **WhatsApp** (the
  existing Evolution adapter), entered in the app.
- An account holds **one subscription per box**. Several phones (the household) can sign in to the
  same account and drive that box.
- **Box transfer** (broken or replaced box): the owner re-binds the pass to another box from the
  app, at most **2 transfers per 30 days**; beyond that, through support.

### Entitlement token

A new `licensing` module in `internal/hub`, on the central hub instance that the project runs.

- After payment (NabooPay webhook, or a verified Play purchase) the hub credits the pass to the account of the phone number
  given on the purchase page.
  The page (`GET /licensing/v1/buy`) needs no sign-in, on purpose: anyone can pay for a number, so a
  relative can buy a household's pass. The pass waits on that number's account until it signs in.
- The hub issues a **signed entitlement token** (Ed25519): `{account, box_id, tier, expires_at,
  issued_at, key_id}`, `key_id` naming the signing key (first 8 bytes of its SHA-256, hex). The
  signing key is kept out of the repository; the public key ships in the app. The format and the
  checks live in `internal/entitlement` (standard library only), shared by the hub and the mobile
  core.
- The app verifies the token **offline**, and so does the embedded Go core: the app hands it the
  token and the trusted public keys (by key id), and the core refuses a paid action itself with a
  `403 {"code": "not_entitled"}`, whatever the screens do. The CLI and `kergui serve` pass no gate. It is valid until the pass expiry, at most 30 days per
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

- Devices known when the feature is enabled count as **accepted**. On first activation, the app
  offers to accept every present device in one tap.
- The phone running the app is **always excluded** (its own MAC or IP), so the user never cuts
  themself off.
- A new device is accepted when it is **approved from its alert notification** or **renamed** by the
  user. If neither happens within **15 min** (fixed), it is refused at the **Wi-Fi access control**
  (`port.WiFiAccessControl`), not the firewall MAC filter.
- Every automatic refusal is written to the journal ("auto : non accepté") and raises a
  notification that opens the device sheet. Restoring its access is free.
- **Who enforces it**: the RFC-005 relay agent when it has sent a heartbeat to the hub in the last
  2 minutes; otherwise the phone's `WatchService` takes over. This avoids two admin sessions
  knocking each other off the box. The agent receives the entitlement token too and checks the
  tier before acting.
- Limit: without an agent, enforcement only runs while a phone is on the box's Wi-Fi.

## Milestones

1. **Licensing**: hub `licensing` module (accounts, WhatsApp OTP, passes, NabooPay webhook, Play
   purchase verification kept for V2, token signing), web purchase page, app gating, Account
   screen with no payment surface, removal of the activation key.
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
    model exposes its serial, Pro cannot be bound on it. The app says the box model is not
    yet supported by the account, and the free tier works normally.
- Direct-APK distribution is affected by Android developer verification in 2027 (see RFC-003).

## Consequences

- The app becomes free to install; revenue moves to passes sold on the hub's web page.
- The hub becomes a production dependency for Pro (not for Free): it must be hosted, monitored and
  backed up, and the signing key must be protected and rotatable.
- New personal data: account phone numbers, stored by the hub.
- The app must never contain payment wording, a price or a link to pay; a CI check on the
  bundle guards it (`mobile/scripts/check-play-bundle.sh`).
- Revenue goes through NabooPay on the web page only (NabooPay's fee, no store fee). Users must
  learn about the page outside the app (WhatsApp, word of mouth), which may slow conversion.
