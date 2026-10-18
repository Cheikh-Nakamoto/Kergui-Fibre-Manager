# Mobile app — store release checklist (Google Play, App Store)

Part of [the mobile app](README.md). v1 is a **paid app with no server**
([RFC-004](../rfcs/RFC-004-standalone-mobile-app.md)). Policies change every
year: re-check each item against the official pages linked in
[RFC-003](../rfcs/RFC-003-mobile-app.md#sources) on the day of submission. State
as of October 2026.

## 0. Selling a paid app (check first)

- **Google**: a paid app needs a **payments (merchant) profile** linked to the
  developer account, in a country where Google supports merchant registration.
  Confirm in Play Console → Settings → Payments profile that it can be opened
  from Senegal before anything else; if not, use an organization account in a
  supported country. Prices per country (XOF, EUR…) are set in Play Console →
  Monetize → App pricing.
- **Apple**: App Store Connect → Business: sign the **Paid Apps Agreement**, add
  the bank account and the tax forms; pick a price tier and the territories.
- Join Apple's **Small Business Program** and accept Google's 15 % service fee
  tier (both: revenue under 1 M USD / year).
- A paid app cannot offer a free trial without in-app purchase: the in-app
  **demo mode** plays that role, and screenshots / video show it.

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

- Package / bundle id: `org.kergui.app` on both stores (permanent — cannot
  change after the first upload; `mobile/app.json`).
- Name: **"Kergui — Gestion box fibre"**. Never "Orange", "Livebox", "Fiberbox" or an
  Orange logo in the name, icon, screenshots or description: the impersonation
  policy rejects it. Write "compatible with ZTE F6600P / F660 / F680 boxes" in the
  description, with a sentence stating the app is independent and not affiliated
  with Orange or ZTE.
- Icon 512×512 and adaptive icon from `internal/webui/assets/logo.png`; feature
  graphic 1024×500; at least 4 phone screenshots (devices list, device detail,
  Wi-Fi, settings) taken in **demo mode** — no real MAC addresses or names.

## 3. Signing and build

- **Play App Signing** (mandatory for new apps): Google holds the app signing
  key; the **upload key** is created by `scripts/mobile/setup-signing.sh android`
  and lives in the GitHub `release` environment.
- Format: Android App Bundle (`.aab`), built and uploaded to the internal track
  by `mobile-release.yml`; `versionCode` = `github.run_number`.
- iOS: `.ipa` built by fastlane on the macOS runner and uploaded to TestFlight.
- Everything is in [release-signing.md](release-signing.md).

## 4. Technical requirements

| Requirement | Value / action |
|---|---|
| Target SDK | **API 36** (mandatory for new apps and updates since 31 Aug 2026). Plan **API 37** for the 2027 deadline |
| Local network (API 37) | `ACCESS_LOCAL_NETWORK` is declared; when targeting 37, request it at runtime before the first call to the box, with an explanation ("Kergui talks to your box on your Wi-Fi"). Do **not** request it while targeting 36 |
| Min SDK | Expo SDK 57 default (Android 7+) |
| 16 KB page size | handled by current React Native / Expo native libraries; verify with the Play pre-launch report |
| Edge-to-edge | enforced on recent Android; use safe-area insets everywhere |
| Foreground services / background location | none — keep it that way (each needs a declaration) |

## 5. Policy declarations (Play Console → App content)

### Privacy policy

Required by both stores, even when nothing is collected. Host it on GitHub
Pages. It must say: the app talks only to the user's own box on the local
network; device names, MAC addresses and the box password stay on the phone
(password encrypted, key in the Keychain / Keystore); no analytics, no ads, no
account, no server; uninstalling deletes everything.

### Data safety form

Nothing is sent to the developer or a third party: the only network peer is the
user's own box on the LAN. Declare **"No data collected"** and **"No data
shared"**. Re-read Google's definition of "collection" on the day of submission;
if it ever counts the box as a recipient, declare "Device or other IDs" (MAC
addresses) for App functionality, not shared.

### Other declarations

- **Content rating** questionnaire: utility, no user-generated content → expected
  "Everyone / 3+".
- **Target audience**: 18+ (adults managing a household network); not designed
  for children.
- **Ads**: none.
- **Financial features**: none.
- **Payments**: none inside the app; it is sold as a paid app through the store.
- **Account deletion**: no account exists; everything is on the phone and goes
  away with the app ("Data deletion" answer).

### App access for reviewers

Reviewers have no ZTE box. In "App access" (Play) and "Notes for review"
(App Store), write: *"Tap « Essayer la démo » on the first screen: the app runs a
simulated box locally, no login needed. With a real box, the phone must be on
its Wi-Fi."* Attach a short video of the app on a real box.

## 6. Release tracks

```
internal testing  ──►  closed testing  ──►  production (staged rollout)
 (you, minutes)        (≥12 testers × 14 days     10 % → 50 % → 100 %
                        for a personal account)    over a few days
```

1. **Internal**: every `mobile-v*` tag lands here (`mobile-release.yml`).
2. **Closed**: testers join through a Google Group or e-mail list and the opt-in
   link; collect feedback in the Play Console and in GitHub issues.
3. **Production**: apply for access (personal account), answer the questionnaire on
   the closed test, then publish with a staged rollout; watch Android vitals
   (crash rate < 1.09 %, ANR < 0.47 % — Google's bad-behaviour thresholds).

## 7. Updates after launch

Every change ships as a new store build through the tracks (no over-the-air JS
updates in v1). A Go core change and a JS change follow the same path.

## 8. Alternative distribution

None planned for a paid app. The code being open source, anyone can build it;
GitHub Releases are not used for APKs.

## 9. App Store specifics

- Apple Developer Program: 99 USD / year; App Store Connect app with bundle id
  `org.kergui.app`.
- `NSLocalNetworkUsageDescription` is set (`mobile/app.json`): iOS asks once
  before the first call to the box; if refused, the app explains how to allow it
  in Settings.
- `ITSAppUsesNonExemptEncryption = false`: the app only uses the standard TLS
  and the OS encryption (export compliance exemption).
- App Privacy: **Data Not Collected**.
- TestFlight: internal testers right away; external testers after a short beta
  review.
- Guideline 4.2 (minimum functionality) is met by native screens and the demo
  mode; never call the app "Orange" (guideline 5.2, intellectual property).

## Submission checklist

- [ ] Merchant profile (Google) and Paid Apps Agreement (Apple) active; price set
- [ ] Account verified; closed test running ≥ 14 days with ≥ 12 testers (personal account)
- [ ] Package, name, icon, screenshots without Orange branding or real data
- [ ] Target SDK 36; pre-launch report clean
- [ ] Privacy policy URL live
- [ ] Data safety (nothing collected), App Privacy, content rating, target audience, ads answered
- [ ] Demo mode tested on a phone without the box (reviewer path)
- [ ] Release notes in French
- [ ] Staged rollout configured; vitals monitored
