# Mobile app — store release checklist (Google Play, App Store)

Part of [the mobile app](README.md). V1 is **one free Google Play build with
no payment inside** ([RFC-006](../rfcs/RFC-006-freemium-subscriptions.md#no-payment-in-the-app-decision-of-2026-11-21),
decision of 2026-11-21). The app manages the box on the LAN; an **optional
account** (phone number verified by a WhatsApp code) lives on the hub and
carries the account's rights for each box.
Policies change every year: re-check each item against the official pages
linked in [RFC-003](../rfcs/RFC-003-mobile-app.md#sources) on the day of
submission. State as of November 2026.

Companion documents, to copy from:

| Document | What it holds |
|---|---|
| [Privacy policy](../../internal/hub/licensing/handler/privacy.html) | The French policy, served by the hub at `<hub>/licensing/v1/privacy` |
| [play-data-safety.md](play-data-safety.md) | Every App content answer: Data safety, payments (none), ads, rating, account deletion, foreground service |
| [play-listing.md](play-listing.md) | Title, short and full description, release notes, graphics and screenshot order, in French |

`<hub>` is the production hub URL (`vars.KERGUI_HUB_URL` of
`mobile-release.yml`).

## Pre-submission compliance checklist

Run it before every submission to review (closed test, production, and any
update that changes the listing or what the app sends).

**No payment anywhere**

- [ ] `cd mobile && bash scripts/check-play-bundle.sh` prints « app bundle: no
      payment wording, price or purchase link » (CI runs it on tags; run it
      locally too).
- [ ] The app shows no price, offer, « s'abonner », payment method, or link /
      text pointing to a website to pay. A locked action only says « Cette
      fonctionnalité n'est pas disponible pour votre compte. »
- [ ] The listing copy, release notes, screenshots, captions and feature
      graphic pass the check script of [play-listing.md](play-listing.md) (no
      price, offer, « gratuit », subscription, payment, link; no Orange /
      Livebox / ZTE).
- [ ] **Monetize → Products** has no in-app product and no subscription; the
      app is **Free**; the AAB has no `com.android.vending.BILLING` permission
      (Play Console → App bundle explorer → Permissions).

**Data safety and policy**

- [ ] `<hub>/licensing/v1/privacy` opens from a phone on mobile data, shows
      today's policy, and its contact address works.
- [ ] The privacy policy, the Data safety answers and the app agree: diff
      what the app sends today against the table "What leaves the phone" of
      [play-data-safety.md](play-data-safety.md) (new hub call, new SDK, new
      permission → update all three).
- [ ] Data safety: phone number + device or other IDs (box serial, phone
      name), optional, not shared, encrypted in transit, deletion on request.
- [ ] Account deletion: in the app, Réglages → Compte → « Supprimer mon
      compte » (an alert, then « SUPPRIMER » typed; `DELETE
      /licensing/v1/account`); web link `<hub>/licensing/v1/privacy#suppression`
      filled in the console.
- [ ] Réglages → « Politique de confidentialité » opens
      `<hub>/licensing/v1/privacy` (the demo opens
      `https://hub.kergui.click/licensing/v1/privacy`).
- [ ] Foreground service declarations (`specialUse` `WatchService`,
      `connectedDevice` `RelayService`) and their videos are up to date
      ([section 5](#foreground-services-and-the-battery-exemption)).
- [ ] `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` justified in the console with
      the text of [section 5](#foreground-services-and-the-battery-exemption).

**Reviewers can use the app**

- [ ] On a phone **not** on a box's Wi-Fi, a fresh install → « Essayer la
      démo » opens the device list (Laptop-Bureau, Phone-Salon, TV-Salon), and
      every tab works: devices, a device's sheet, Conso, Wi-Fi, Réglages,
      Compte. The demo box logs in by itself (`admin` / `admin`,
      `DEMO_CREDENTIALS` in `mobile/src/session/store.ts`).
- [ ] `maestro test mobile/.maestro/demo-device.yaml` green on a real phone
      (or `demo.yaml` on an emulator).
- [ ] App access instructions in the console mention « Essayer la démo » and
      `admin` / `admin`.

**Listing assets**

- [ ] Screenshots regenerated from `mobile/.maestro/screenshots.yaml` on the
      build being submitted; only placeholder MACs (`ac:bb:cc:00:00:xx`) and
      demo names; no operator or manufacturer name on any screen (the
      welcome says « votre box fibre » since #37).
- [ ] Release notes in French, no payment wording.

**Build**

- [ ] Target SDK 36; pre-launch report clean (no crash, 16 KB page size OK).
- [ ] `versionCode` higher than the last upload (`github.run_number`).
- [ ] The release build has `KERGUI_HUB_URL` (https) and
      `KERGUI_LICENSING_PUBLIC_KEY` set (repository variables), otherwise the
      Compte screen says the account is unavailable. During a signing key
      rotation it lists the current and the next key, comma separated
      ([runbook](../licensing-key-rotation.md)).

## Play Console, step by step

First time only (personal developer account, verified identity).

1. **Create app**: name « Kergui — Gestion box fibre », default language
   français (France), **App** (not game), **Free**, accept the declarations.
   Package `org.kergui.app` is fixed by the first upload.
2. **Upload a build**: push a `mobile-v*` tag; `mobile-release.yml` builds the
   AAB and uploads it to the **internal** track. Check App bundle explorer →
   permissions: no `BILLING`.
3. **Policy → App content**, one declaration at a time, from
   [play-data-safety.md](play-data-safety.md): Privacy policy, App access,
   Ads (No), Content rating questionnaire, Target audience (18+), News (No),
   Data safety, Government apps (No), Financial features (None), Health
   (None), Account deletion, Foreground service permissions.
4. **Grow users → Store presence → Main store listing**: paste from
   [play-listing.md](play-listing.md); upload icon, feature graphic and
   screenshots 2–8. **Store settings**: category Tools, contact e-mail.
5. **Monetize → Products**: leave In-app products and Subscriptions empty.
   No payments profile is needed.
6. **Testing → Closed testing**: create a track (e.g. « Test fermé »), pick
   the build from internal, add the testers' list (a Google Group or an
   e-mail list), countries: Senegal (and any where testers live), paste the
   release notes, **Review and roll out**. Send the opt-in link to every
   tester: each must accept it **and** install from Play.
7. **Wait 14 consecutive days with ≥ 12 opted-in testers**. Recruit 15–20 so
   the count never drops under 12 (if it does, the 14 days start again).
   Collect feedback in the console and in GitHub issues; ship fixes to the
   same closed track.
8. **Apply for production access** (Dashboard): answer the questionnaire on
   the closed test (how testers were recruited, what feedback changed),
   then create the production release with a **staged rollout**
   (10 % → 50 % → 100 %) and watch Android vitals.

## 1. Developer account

| | Personal account | Organization account |
|---|---|---|
| Fee | 25 USD, once | 25 USD, once |
| Identity | government ID verification | D-U-N-S number + organization documents |
| Before production | **closed test, ≥ 12 opted-in testers, 14 consecutive days** | no testing requirement |
| Public developer name | the person's legal name | the organization |

- Developer verification (identity) is also what keeps APKs installable outside
  Play on certified devices from 2027; the same verified account covers both.
- No merchant / payments profile for V1: the app sells nothing.

## 2. App identity

- Package / bundle id: `org.kergui.app` on both stores (permanent — cannot
  change after the first upload; `mobile/app.json`).
- Name: **« Kergui — Gestion box fibre »** (26 characters). Never "Orange",
  "Livebox", "Fiberbox", "ZTE" or a brand logo in the name, icon, screenshots,
  graphics or description: the impersonation policy rejects it. The
  description says « box fibre » and ends with a sentence stating the app is
  independent of the Internet provider and the box maker.
- Icon 512×512 and adaptive icon from `internal/webui/assets/logo.png`; feature
  graphic 1024×500; screenshots taken in **demo mode** — no real MAC
  addresses or names ([play-listing.md](play-listing.md#screenshots-in-demo-mode)).

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
| Foreground service | `WatchService`, type **`specialUse`** (new-device alerts and auto-reject while the app is closed), and `RelayService`, type **`connectedDevice`** (the phone relay): each needs its declaration and a video ([section 5](#foreground-services-and-the-battery-exemption)) |
| Restricted permissions | `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`: **kept** (owner's decision, #37), justified in the console ([section 5](#foreground-services-and-the-battery-exemption)). No background location |

## 5. Policy declarations (Play Console → App content)

All answers, with the code behind each, are in
[play-data-safety.md](play-data-safety.md). In short:

- **Privacy policy**: `<hub>/licensing/v1/privacy` (French, served by the hub
  from `internal/hub/licensing/handler/privacy.html`, tested by
  `privacy_test.go`). If the hub is not public yet, host the same HTML file
  anywhere stable (GitHub Pages) and use that URL.
- **Data safety**: phone number and device or other IDs (box serial number,
  phone name), collected for app functionality, account management and fraud
  prevention, **optional**, **not shared**, encrypted in transit, deletion in
  the app. With an account on a real box, the household's backup (#88/#89:
  the devices' MACs, names, notes and monthly usage) goes to the hub too:
  see [play-data-safety.md](play-data-safety.md). The box password and the
  journal stay on the phone.
- **Payments**: none. No in-app products, no subscriptions, Free app, no
  financial features.
- **Ads**: none. **Content rating**: Everyone / 3+ expected.
  **Target audience**: 18+.
- **Account deletion**: required (the WhatsApp sign-in creates an account).
  In the app: Réglages → Compte → « Supprimer mon compte », immediate. On
  the web: `<hub>/licensing/v1/privacy#suppression` (how to do it in the app,
  or by e-mail without it). What is erased: see
  [play-data-safety.md](play-data-safety.md#account-deletion).
- **Privacy policy in the app**: Réglages → « Politique de confidentialité ».

### Foreground services and the battery exemption

Play Console → App content → **Foreground service permissions**, one entry
per type. Paste the text, attach the video (an unlisted YouTube link of
30–90 s, recorded on a real phone and box; the demo cannot show the
notification with the app closed).

**`FOREGROUND_SERVICE_SPECIAL_USE`** (`WatchService`, subtype declared in
`mobile/modules/kergui-core/android/src/main/AndroidManifest.xml`)

- Task: *Other* (special use).
- Description: *"Kergui watches the user's home router (on the home Wi-Fi)
  for new devices joining the network. It alerts the owner as soon as an
  unknown device connects and, if the owner turned on auto-reject, refuses
  the devices they did not accept. Both must work while the app is closed,
  which only a foreground service allows: the router has no push, so the
  app asks it every 30 seconds. The service runs only after the user turned
  on « Alerter à chaque nouvel appareil » (or auto-reject) in the app, shows
  an ongoing notification (« Kergui surveille votre réseau ») the whole
  time, and stops when the user turns it off."*
- Why not WorkManager / JobScheduler: their 15-minute minimum lets an
  intruder use the network for a quarter of an hour before the owner knows
  or before auto-reject refuses it.
- Video script: (1) the app open on Réglages, turn on « Alerter à chaque
  nouvel appareil », accept the notification permission, then the battery
  explanation and Android's prompt; (2) the ongoing notification
  « Kergui surveille votre réseau » in the shade; (3) close the app (swipe
  it away from recents); (4) connect a new phone to the box's Wi-Fi; (5) the
  « Nouvel appareil » alert appears with the app closed; (6) reopen
  Réglages, turn the switch off, the ongoing notification goes away.

**`FOREGROUND_SERVICE_CONNECTED_DEVICE`** (`RelayService`, type declared in
`mobile/modules/kergui-core/android/src/main/AndroidManifest.xml`; its
prerequisite permission is `CHANGE_NETWORK_STATE`)

- Task: *Connected device* (the household's fibre router, reached on the
  local Wi-Fi).
- Description: *"A phone left at home can be made the household's relay.
  It stays connected to the household's home router on the local Wi-Fi and
  forwards to it the commands the owner signs on their own phone while they
  are away (block a device, cut its Wi-Fi, apply auto-reject), then reports
  the router's device list back. The router has no cloud API and accepts
  connections only from the local network, so the relay phone must hold a
  long-poll to our server and a session with the router continuously. The
  service starts only after the user made this phone a relay, read an
  in-app disclosure of what it sends and tapped « Accepter », and tapped
  « Démarrer le relais ». It shows an ongoing notification (« Relais
  Kergui ») with a Stop action the whole time, and stops from that action,
  from « Arrêter le relais » or from the switch in the app."*
- Why not WorkManager / JobScheduler: a command the owner sends from away
  (block an intruder, cut a child's Wi-Fi) must reach the router within
  seconds; the relay keeps a 25 s long-poll to the server and a 20 s
  heartbeat that lets the server pick one active relay. WorkManager's
  15-minute minimum would leave commands waiting a quarter of an hour and
  the household with no relay seen as active.
- Why `connectedDevice` and not `dataSync` or `specialUse`: the service
  exists to keep a link with an external device (the router) over the
  local network; it does not transfer a user's files and it never ends on
  its own.
- Video script (30–90 s, real phone and router; show the disclosure, Play
  checks it): (1) on the relay phone, the welcome screen → « Être un relais »;
  (2) the disclosure « Avant de faire de ce téléphone un relais », scroll
  through it, tap « Accepter »; (3) scan the QR code shown on the owner's
  phone, the same six digits on both screens, confirm on the owner's phone;
  (4) « Démarrer le relais », accept the notification and location prompts;
  (5) the ongoing notification « Relais Kergui » with « Arrêter » in the
  shade; (6) close the app (swipe it away from recents); (7) on the owner's
  phone, on mobile data (Wi-Fi off), block a device: it is blocked on the
  router; (8) tap « Arrêter » in the notification: the notification goes
  away.
- Prominent disclosure (User Data policy): the relay screen shows it before
  the first activation, from both entries; « Annuler » leaves the relay
  off. Details and code: [play-data-safety.md](play-data-safety.md#prominent-disclosure-for-the-relay-53).

**`REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`** (Play's restricted permission:
App content → Sensitive permissions, or the policy form Play opens on
review). Decision: keep it (#37). Justification to paste:

*"Kergui's core feature is a network watchdog for the user's home router:
it alerts the owner when an unknown device joins their Wi-Fi and, if the
owner enabled it, automatically refuses devices they did not approve. This
runs in a foreground service (`specialUse`) that must keep polling the
router while the app is closed. On many devices sold in our market (ColorOS,
MIUI, Transsion's HiOS/XOS) battery optimisation kills such services within
minutes, so alerts and auto-reject silently stop, which defeats the purpose
of the app. The exemption is requested only after the user turns on the
alerts or auto-reject in the app, and only after an in-app explanation the
user must accept (« Pour vous alerter et rejeter les nouveaux appareils même
quand l'app est fermée… » → Continuer); refusing it keeps the app working.
A Réglages entry lets the user ask again later. The app never asks at
launch."*

The code behind it: `mobile/src/alerts/switchOn.ts` (the explanation, then
`askBatteryUnrestricted`), `mobile/src/app/(tabs)/settings.tsx` (the
« Autoriser l'activité en arrière-plan » row, shown only while alerts are on
and the app is restricted), tested by `mobile/src/__tests__/batteryAsk.test.ts`.
If the console still refuses it: remove the permission from the module's
manifest and the `requestBatteryUnrestricted` call; the alerts then rely on
the foreground service alone and the Réglages row should send the user to
Android's battery settings instead.

### App access for reviewers

Reviewers have no box. In "App access" (Play) and "Notes for review"
(App Store), write: *« Sur le premier écran, touchez « Essayer la démo » :
l'application lance une box simulée, sans réseau ni compte. Si un identifiant
est demandé : admin / admin. Avec une vraie box, le téléphone doit être sur son
Wi-Fi. »* Attach a short video of the app on a real box if Play asks for one.

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
updates in v1). A Go core change and a JS change follow the same path. Any
change to what the app sends (a new hub call, SDK or permission) updates the
privacy policy and the Data safety form **before** the build is submitted.

## 8. Alternative distribution

None planned. The code being open source, anyone can build it; GitHub
Releases are not used for APKs. In-app purchase through Play Billing may come
back in V2; the hub's Play code stays off until then.

## 9. App Store specifics

- Apple Developer Program: 99 USD / year; App Store Connect app with bundle id
  `org.kergui.app`.
- `NSLocalNetworkUsageDescription` is set (`mobile/app.json`): iOS asks once
  before the first call to the box; if refused, the app explains how to allow it
  in Settings.
- `ITSAppUsesNonExemptEncryption = false`: the app only uses the standard TLS
  and the OS encryption (export compliance exemption).
- App Privacy: phone number and device ID linked to the user, for app
  functionality; not used for tracking.
- TestFlight: internal testers right away; external testers after a short beta
  review.
- Guideline 4.2 (minimum functionality) is met by native screens and the demo
  mode; never use a brand of the box or the operator (guideline 5.2).
