# Mobile app — Google Play release checklist

Part of [Phase 4](README.md). Policies change every year: re-check each item
against the official pages linked in [RFC-003](../rfcs/RFC-003-mobile-app.md#sources)
on the day of submission. State as of October 2026.

## 1. Developer account

| | Personal account | Organization account |
|---|---|---|
| Fee | 25 USD, once | 25 USD, once |
| Identity | government ID verification | D-U-N-S number + organization documents |
| Before production | **closed test, ≥ 12 opted-in testers, 14 consecutive days** | no testing requirement |
| Public developer name | the person's legal name | the organization |

- With a personal account, the 14-day closed test is on the critical path: recruit
  15–20 testers (family, members, friends) so the count never drops under 12 — if
  it does, the 14 days start again.
- Developer verification (identity) is also what keeps APKs installable outside
  Play on certified devices from 2027; the same verified account covers both.

## 2. App identity

- Package: `org.cheikhmodiouf.kergui` (permanent — cannot change after the first
  upload).
- Name: **"Kergui — Gestion box fibre"**. Never "Orange", "Livebox", "Fiberbox" or an
  Orange logo in the name, icon, screenshots or description: the impersonation
  policy rejects it. Write "compatible with ZTE F6600P / F660 / F680 boxes" in the
  description, with a sentence stating the app is independent and not affiliated
  with Orange or ZTE.
- Icon 512×512 and adaptive icon from `internal/webui/assets/logo.png`; feature
  graphic 1024×500; at least 4 phone screenshots (admin devices list, Wi-Fi,
  member home, payment) taken on the mock router — no real MAC addresses or names.

## 3. Signing and build

- **Play App Signing** (mandatory for new apps): Google holds the app signing key;
  EAS holds the **upload key** (`eas credentials`). Export a backup of the upload
  key to a password manager.
- Format: Android App Bundle (`.aab`), built by `eas build --profile production`.
- `versionCode` auto-incremented by EAS; never reuse one.

## 4. Technical requirements

| Requirement | Value / action |
|---|---|
| Target SDK | **API 36** (mandatory for new apps and updates since 31 Aug 2026). Plan **API 37** for the 2027 deadline |
| Local network (API 37) | when targeting 37: declare `ACCESS_LOCAL_NETWORK`, request it at runtime before the first LAN call, with an explanation screen ("Kergui talks to your box server on your Wi-Fi"); if refused, fall back to the tunnel path. Do **not** request it while targeting 36 |
| Min SDK | Expo SDK 57 default (Android 7+) |
| 16 KB page size | handled by current React Native / Expo native libraries; verify with the Play pre-launch report |
| Edge-to-edge | enforced on recent Android; use safe-area insets everywhere |
| Notifications | `POST_NOTIFICATIONS` runtime permission asked after pairing, not at first launch |
| Foreground services / background location | none — keep it that way (each needs a declaration) |

## 5. Policy declarations (Play Console → App content)

### Privacy policy

Required. Host it at `https://pay.cheikhmodiouf.org/confidentialite` (add the path
to the tunnel) or on GitHub Pages. It must say: data stays on the user's own
kergui server; what the app sends (device name, push token); Expo/Google push
processing; how to unpair and delete.

### Data safety form

The app transmits data to the user's own server and push tokens to Expo/Google.
Declare conservatively:

| Data type | Collected | Shared | Purpose | Optional |
|---|---|---|---|---|
| Name (member name, phone name) | yes | no | App functionality | no |
| Phone number (member) | yes | no | App functionality | yes |
| Device or other IDs (Wi-Fi MAC addresses, push token) | yes | yes (push token → Expo / FCM) | App functionality | push: yes |
| App activity / usage (internet usage in bytes) | yes | no | App functionality | no |
| Financial info — purchase history (invoices, payment status) | yes | no | App functionality | no |
| Crash logs (only if Sentry is enabled) | yes | yes (Sentry) | Analytics | — |

- Data encrypted in transit: **yes** (TLS everywhere).
- Users can request deletion: **yes** (unpair in the app; the admin deletes the
  member on the server).

### Other declarations

- **Content rating** questionnaire: utility, no user-generated content → expected
  "Everyone / 3+".
- **Target audience**: 18+ (adults managing a household network and payments); not
  designed for children.
- **Ads**: none.
- **Financial features**: the app shows bills and opens a payment page; it is not
  a wallet nor a lender.
- **Payments**: members pay for internet access consumed on the LAN, a
  real-world service, so **Google Play Billing is not required**; payment happens on
  NabooPay in a browser tab. State it in the review notes.
- **Account deletion**: no account is created in the app (pairing only); explain it
  in the "Data deletion" answer.

### App access for reviewers

Every screen is behind pairing, so the reviewer needs a way in:

- run a **demo kergui** on the mock router (`cmd/mockrouter`), reachable through
  the tunnel at e.g. `https://demo.cheikhmodiouf.org`;
- give, in "App access", a long-lived admin pairing link for it and a member one,
  plus step-by-step instructions;
- the demo must hold fake members, devices and an unpaid invoice.

## 6. Release tracks

```
internal testing  ──►  closed testing  ──►  production (staged rollout)
 (you, minutes)        (≥12 testers × 14 days     10 % → 50 % → 100 %
                        for a personal account)    over a few days
```

1. **Internal**: every CI tag lands here (`eas submit --track internal`).
2. **Closed**: testers join through a Google Group or e-mail list and the opt-in
   link; collect feedback in the Play Console and in GitHub issues.
3. **Production**: apply for access (personal account), answer the questionnaire on
   the closed test, then publish with a staged rollout; watch Android vitals
   (crash rate < 1.09 %, ANR < 0.47 % — Google's bad-behaviour thresholds).

## 7. Updates after launch

| Change | Channel |
|---|---|
| JS/TS only (screens, texts, fixes) | **EAS Update** to the `production` channel, scoped by `runtimeVersion` (policy `fingerprint`) — no store review |
| Native module, permission, SDK, Expo SDK upgrade | new store build through the tracks |
| Server-only change | none, as long as `/api/v1` stays compatible |

EAS Update must not change the app's purpose or add features Google would review
differently (Play policy on dynamically loaded code); keep it to fixes and UI.

## 8. Alternative distribution

- **GitHub Releases APK** (`eas build --profile preview` produces a signed APK) for
  people without Play — subject to developer verification on certified devices
  from 2027; ADB installs stay possible.
- **F-Droid**: not compatible as is (EAS cloud builds, push via Google services).

## 9. iOS (out of scope, noted)

Same codebase. Needs an Apple Developer account (99 USD / year),
`NSLocalNetworkUsageDescription` (iOS asks for local network access), APNs through
Expo, and App Store review of the external payment (real-world service exemption
applies there too).

## Submission checklist

- [ ] Account verified; closed test running ≥ 14 days with ≥ 12 testers (personal account)
- [ ] Package, name, icon, screenshots without Orange branding or real data
- [ ] Target SDK 36; pre-launch report clean
- [ ] Privacy policy URL live
- [ ] Data safety, content rating, target audience, ads, financial features answered
- [ ] Reviewer access (demo server + pairing links) tested from a phone on 4G
- [ ] Release notes in French
- [ ] Staged rollout configured; vitals monitored
