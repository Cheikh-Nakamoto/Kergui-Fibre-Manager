# Play Console — App content answers (Data safety, payments, the rest)

Part of the [Play release checklist](release-playstore.md). One answer per
question of **Play Console → Policy → App content**, each with the code that
justifies it. State of the app on 2026-11-24 (V1: one free build, **no payment
in the app**, [RFC-006](../rfcs/RFC-006-freemium-subscriptions.md#no-payment-in-the-app-decision-of-2026-11-21)).
Re-read Google's wording on the day of submission; if the app changes what it
sends, update this page, the [privacy policy](../../internal/hub/licensing/handler/privacy.html)
and the console together.

`<hub>` below is the production hub URL (`vars.KERGUI_HUB_URL` of the release
workflow).

## What leaves the phone (the facts behind every answer)

| Data | Where it goes | Code |
|---|---|---|
| Box password, MACs, IPs, hostnames, custom names, per-device usage, block / cut / auto-reject rules, journal | **Nowhere**: the phone's own core database and the box on the LAN. Password encrypted, key in SecureStore (Android Keystore); `allowBackup: false` | `mobile/src/session/store.ts` (`vaultKey`), `mobilecore/core.go`, `mobile/app.json` |
| Phone number | Hub, over HTTPS, when the user signs in (optional) | `mobile/src/licensing/account.ts` → `POST /licensing/v1/otp`, `otp/verify` |
| Phone's device name (`Constants.deviceName`, ≤ 64 chars) | Hub, stored with the session | `account.ts` `deviceName()`, `hub_licensing_sessions.device` |
| Box serial number | Hub, when signed in (binds the account's rights to the box) | `mobile/src/licensing/entitlement.ts` → `POST bind`, `POST token`, `POST transfer` |
| Session token | Issued by the hub, kept in SecureStore; hub stores only its hash | `account.ts`, `usecase/otp.go` |
| Entitlement token (tier, box, expiry) | Issued by the hub to the app; verified offline | `entitlement.ts`, `token.ts` |
| Notifications | Built on the phone (`expo-notifications`, `WatchService`). With an account and alerts on, in a build with an EAS project id (#49): this phone's Expo push token goes to the hub, so a new-device alert arrives with the app closed (via FCM); erased with the account | `mobile/src/alerts/alerts.ts`, `mobile/src/alerts/push.ts` → `PUT …/push-tokens`, `modules/kergui-core/android/.../WatchService.kt` |
| Journal | Leaves the phone only when the user taps « Partager » (system share sheet) | `mobile/src/app/journal.tsx` |
| **Only when the phone is a relay** (optional, « Être un relais » or Plus de fonctionnalités › « Ce téléphone sert de relais », [relay-android.md](relay-android.md); only after the [prominent disclosure](#prominent-disclosure-for-the-relay-53) is accepted): snapshots of the box's device list (MACs, IPs, hostnames, SSID used, connected or not) and observations, every 60 s and after each command; the box's Wi-Fi networks without their passwords, every 5 min; the command results; a heartbeat every 20 s with the phone's LAN IP, the name (SSID) and access point (BSSID) of the Wi-Fi it is on, the box certificate fingerprint, the app version; at pairing, the phone's name and the relay's public keys. The box serial is read on the box to check the household's rights, not sent by the relay | Hub, over HTTPS, signed by the relay's key; tied to the owner's household (the owner's account and phone number), the relay phone itself has no account; the hub keeps only the newest snapshot per kind. The box password never (it arrives sealed for the relay and stays in its vault) | `internal/agent` (`hubclient.go` `heartbeatRequest`, `snapshots.go`), `mobilecore/relayrun.go`, `internal/hub/relay` |
| **PC relay** (`kergui serve --hub`, a separate program, #90): the same as the phone relay, plus the household backup (row below) at most once a day | Hub, over HTTPS | `internal/agent/backup.go` |
| **Household backup** (with an account, on a real box, not the demo; #88/#89): per device its MAC, the name and notes the user gave it, first sighting, vendor, hostname, and its usage per month | Hub, over HTTPS, automatically (30 s after a rename or approval, at most once a day otherwise); kept per household and box, plus up to five replaced versions for seven days. Never the box password | `mobile/src/features/hubBackup.ts`, `internal/hub/backups` |
| **Owner's phones** (#87): the MAC this phone has on the box, when it is the owner's | Hub, so the household's phones leave it out of the Devices tab | `mobile/src/features/ownerDevices.ts`, `hub_inventory_owner_devices` |
| Complaints (« Signaler un problème », #84): the category, the message, the journal if the user ticks it | Hub, read by the Kergui team | `mobile/src/app/report.tsx`, `internal/hub/complaints` |
| Camera image | **Nowhere**: used only to read the owner's QR code on the relay phone; no picture is kept | `mobile/src/app/become-relay.tsx` (`expo-camera`) |
| IP address of hub requests | Hub, per-IP rate limit and request logs; not used for location | `internal/hub/app/routes.go` (`clientIP`) |

No analytics, ads, crash-reporting SDK or advertising ID in the app
(`mobile/package.json`). No contacts, photos or files permission
(`mobile/app.json`, `modules/kergui-core/android/src/main/AndroidManifest.xml`).

**Permissions added by the phone relay (#45)**, all asked only when the user
makes this phone a relay:

| Permission | Why | If refused |
|---|---|---|
| `CAMERA` (`expo-camera`, `RECORD_AUDIO` blocked) | Scan the owner's QR code | The code under the QR is typed instead |
| `ACCESS_FINE_LOCATION` / `ACCESS_COARSE_LOCATION` (while in use; **no background location**, `ACCESS_BACKGROUND_LOCATION` blocked) | Android gives the Wi-Fi's name (SSID/BSSID) only with it: the relay checks it is on the household's Wi-Fi | The relay checks the box certificate alone and says so |
| `POST_NOTIFICATIONS` | The relay's ongoing notification | Android hides it; the service still runs |
| `FOREGROUND_SERVICE_CONNECTED_DEVICE`, `CHANGE_NETWORK_STATE`, `ACCESS_NETWORK_STATE`, `ACCESS_WIFI_STATE` (normal) | `RelayService`, bound to the home Wi-Fi | — |

## Data safety form

**Data collection and security**

| Question | Answer | Why |
|---|---|---|
| Does your app collect or share any of the required user data types? | **Yes** | Phone number, device IDs (box serial, phone name) go to the hub when the user signs in |
| Is all of the user data collected by your app encrypted in transit? | **Yes** | The hub URL must be https (`mobile/config/licensing.js` refuses anything else outside localhost) |
| Do you provide a way for users to request that their data is deleted? | **Yes** | In the app: Réglages → Compte → « Supprimer mon compte », immediate ([Account deletion](#account-deletion)); without the app: e-mail, described at `<hub>/licensing/v1/privacy#suppression` |

**Data types** (tick only these)

| Category → type | Collected | Shared | Processed ephemerally | Required or optional | Purposes |
|---|---|---|---|---|---|
| Personal info → **Phone number** | Yes | No | No | **Optional** (the app works without an account) | **App functionality**, **Account management**, **Fraud prevention, security and compliance** (one trial per number) |
| Device or other IDs → **Device or other IDs** | Yes | No | No | **Optional** | **App functionality**, **Account management**, **Fraud prevention, security and compliance** (one trial per box, transfer limit). Also the household backup's MACs (#88/#89) |
| App activity → **Other user-generated content** | Yes (the names and notes the user gives devices, in the household backup; the complaints' text) | No | No | **Optional** | **App functionality** |

**The phone relay (#45, H3/H5)** ships in the Play build: tick these too.
They leave the phone only when the user made it a relay and accepted the
[disclosure](#prominent-disclosure-for-the-relay-53), so they are **optional**:

| Category → type | Collected | Shared | Purposes |
|---|---|---|---|
| Location → **Approximate location** | Yes (the Wi-Fi SSID/BSSID the relay is on, which Play treats as location; not used to locate anyone) | No | **App functionality** (checks the relay is on the household's Wi-Fi) |
| Device or other IDs | Yes (already ticked: now also the box devices' MACs relayed to the hub for the household's remote access, and the relay phone's name) | No | **App functionality** |

Answer **No / not collected** for every other type, in particular:

- **Device or other IDs** is the box serial number and the phone's device
  name, and, with an account on a real box, the devices' MACs in the
  household backup (#88/#89), the owner's phone's own MAC (#87) and, with
  the alerts on, the phone's push token (#49). A phone
  made a relay sends the device list to the hub (table above). Without an
  account, MACs never leave the phone.
- **Photos and videos**: the camera only reads a QR code; no image is kept or
  sent.
- **Location → Precise location**: not collected. Approximate location is
  ticked only for the relay's Wi-Fi SSID/BSSID (above); the hub sees IP
  addresses but does not derive a location from them, and the app never
  reads a GPS position.
- **Financial info / Purchase history**: the app sends no purchase data and
  has no purchase flow. The hub's records of rights credited outside the app
  are not collected *by the app*.
- **Messages**: the WhatsApp code is sent *to* the user; the app reads no
  message.
- **App activity, App info and performance (crash logs, diagnostics)**: none
  sent. The journal leaves the phone only through a share the user starts,
  to a recipient the user picks, which Play does not count as collection.
- **Personal info → User IDs**: the account is identified by the phone number
  (declared above); the hub's internal account id is not collected from the
  user. If a reviewer disagrees, add "User IDs", optional, App functionality +
  Account management.

**Sharing**: **No data shared.** WhatsApp (Meta) only delivers the hub's
messages on its behalf (service provider), which Play does not count as
sharing.

**Security practices** (shown on the listing)

- Data is encrypted in transit: **Yes**
- You can request that data be deleted: **Yes**
- Committed to follow the Play Families Policy: **No** (not for children)
- Independent security review: **No**

## Payments declaration: no in-app purchases

| Where | Answer |
|---|---|
| **Monetize → Products** (In-app products, Subscriptions) | Create **nothing**. Leave both lists empty |
| **Store settings / Pricing**: free or paid | **Free** |
| **App content → Ads**: does your app contain ads? | **No** |
| **App content → Financial features** | **My app doesn't provide any financial features** |
| Store listing: "Contains ads" / "In-app purchases" labels | Neither appears (follows from the two answers above) |
| Payments profile / merchant account | **Not needed** for V1 |

Why it is true: no Play Billing library (`expo-iap` is gone), the manifest
blocks `com.android.vending.BILLING` (`mobile/config/licensing.js`), and
`mobile/scripts/check-play-bundle.sh` fails the build on any payment wording,
price or purchase link in the JS bundle. Locked actions only say « Cette
fonctionnalité n'est pas disponible pour votre compte. »

If a reviewer asks how the Basic / Max levels are obtained: rights are
attached to the user's account by the Kergui service, outside the app; the
app sells nothing, shows no price and does not link to any payment. Do not
paste a purchase URL into the console.

## Other App content declarations

| Declaration | Answer |
|---|---|
| **Privacy policy** | `<hub>/licensing/v1/privacy` |
| **App access** | "All or some functionality is restricted" → add instructions: *« Sur le premier écran, touchez « Essayer la démo » : l'application lance une box simulée, sans réseau ni compte. Si un identifiant est demandé : admin / admin. Avec une vraie box, le téléphone doit être sur son Wi-Fi. »* (Demo credentials: `DEMO_CREDENTIALS` in `mobile/src/session/store.ts`) |
| **Ads** | No |
| **Content rating** | Category "Utility, productivity, communication or other"; no violence, sexuality, gambling, user-generated content, location sharing or purchases → expected **Everyone / 3+**. Answer "No" to "Does the app allow users to interact or exchange content" |
| **Target audience and content** | **18 and over** only; not designed for children; no appeal to children |
| **News app** | No |
| **COVID-19 apps** | Not a COVID app |
| **Data safety** | As above |
| **Government apps** | No |
| **Financial features** | None |
| **Health** | None |
| **Account deletion** | The app creates accounts (WhatsApp sign-in), so this is **required**. In-app path: Réglages → Compte → « Supprimer mon compte ». Web link (for people without the app): `<hub>/licensing/v1/privacy#suppression`. Data deleted: everything, nothing kept ([below](#account-deletion)) |
| **Foreground service permissions** | `FOREGROUND_SERVICE_SPECIAL_USE` (`WatchService`): new-device alerts and auto-reject with the app closed. Text and video script: [release-playstore.md](release-playstore.md#foreground-services-and-the-battery-exemption) |
| **Foreground service: `connectedDevice`** | `FOREGROUND_SERVICE_CONNECTED_DEVICE` (`RelayService`). Description, why not WorkManager and video script (it starts on the [disclosure](#prominent-disclosure-for-the-relay-53)): [release-playstore.md](release-playstore.md#foreground-services-and-the-battery-exemption) |
| **Sensitive permissions** | `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` is on Play's restricted list. **Kept** (owner's decision, #37); asked only after the user turned the alerts on and accepted an in-app explanation. Justification to paste: [release-playstore.md](release-playstore.md#foreground-services-and-the-battery-exemption) |

## Prominent disclosure for the relay (#53)

Play's User Data policy asks for an in-app disclosure, before the
collection starts, when data is collected in a way the user may not
expect: here the device list sent while the app is closed, from a phone
that is not the owner's. The relay screen (`mobile/src/app/become-relay.tsx`)
shows it before anything else the first time, from both entries (welcome ›
« Être un relais », and Plus de fonctionnalités › the switch « Ce téléphone
sert de relais », which opens that screen until it was accepted):

- what the relay does, what it sends to the Kergui server (the list above,
  in plain French), why, what never leaves (the box password), the
  permanent « Relais Kergui » notification of the foreground service, and
  how to stop it (the notification's « Arrêter », « Arrêter le relais », the
  switch);
- « Accepter » goes on to the pairing; « Annuler » goes back with the relay
  off and nothing sent; the switch does not start the service before it was
  accepted, even on a phone paired earlier;
- the answer is remembered (`mobile/src/features/relayConsent.ts`, a
  SecureStore flag) and « Ce que le relais envoie » on the relay screen
  shows the text again. The demo shows it too and keeps its answer in
  memory only.

Text: `relayDisclosure.*` in `mobile/src/i18n/fr.ts`; component
`mobile/src/ui/RelayDisclosure.tsx`; tests
`mobile/src/__tests__/relayDisclosure.test.tsx`; Maestro
`mobile/.maestro/relay-disclosure-steps.yaml`. If the text changes what is
sent, bump `RELAY_CONSENT_KEY` so everyone is asked again.

## Account deletion

**In the app** (#37): Réglages → Compte → « Supprimer mon compte ». A first
alert says what goes; then the person types « SUPPRIMER » and taps
« Supprimer définitivement ». The demo refuses it in a sentence (it has no
account). Code: `mobile/src/app/account.tsx`, `mobile/src/licensing/deleteAccount.ts`;
tests: `mobile/src/__tests__/accountDelete.test.tsx`, `deleteAccount.test.ts`.

**On the hub**: `DELETE <hub>/licensing/v1/account` with the kgs_ session,
one Postgres transaction (`internal/hub/identity/erase.go`), end-to-end test
`internal/hub/e2e/account_deletion_e2e_test.go`. It erases, with nothing
kept (owner's decision):

- the licensing account (the number), every session of every phone, the
  account's rights per box, trial, transfers, payment records, Play
  bindings, reminders, and the number's sign-in codes;
- the complaints of the account or of its number, and their messages;
- the kernel user (refresh tokens, memberships, push tokens, WhatsApp
  opt-ins and queued messages, member keys, alert deliveries);
- each household the account was in **when nobody else is in it**: the
  whole household (owner's phones #87, backups and their versions #88,
  relays, inventory, alerts, trust bundles, the kernel tenant);
- in a household **with other members**: only the account leaves; the owner's
  phones it declared go, the household and its backups stay for the
  members (handing the household to one of them is a support case).

One `ACCOUNT_DELETED` row goes to the action log (back-office « Journal »),
with the masked number and how many households were erased or left.

Then the phone forgets the account, both sessions, the box, the alerts,
the notifications inbox and the hub caches, and shows the welcome screen.

**On the web** (people who no longer have the app): the policy's section
`#suppression` explains the in-app path and the e-mail request
(feppdougou@gmail.com), confirmed from the number, done the same way.

## Known gaps before production

None left in the app for account deletion and the policy link (done in #37:
« Supprimer mon compte » on the Compte screen, « Politique de
confidentialité » in Réglages). The privacy policy's section 4 « Relais à la
maison » (`#relais`) covers the phone and the PC relay (#53), and section 2
the push token (#49). Left to a person: enter the relay rows above in the
console's Data safety form and the `connectedDevice` declaration with its
video ([release-playstore.md](release-playstore.md#foreground-services-and-the-battery-exemption)).
