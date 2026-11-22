# Play Console — App content answers (Data safety, payments, the rest)

Part of the [Play release checklist](release-playstore.md). One answer per
question of **Play Console → Policy → App content**, each with the code that
justifies it. State of the app on 2026-11-22 (V1: one free build, **no payment
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
| Notifications | Built on the phone (`expo-notifications`, `WatchService`); no push token, no push server | `mobile/src/alerts/alerts.ts`, `modules/kergui-core/android/.../WatchService.kt` |
| Journal | Leaves the phone only when the user taps « Partager » (system share sheet) | `mobile/src/app/journal.tsx` |
| IP address of hub requests | Hub, per-IP rate limit and request logs; not used for location | `internal/hub/app/routes.go` (`clientIP`) |

No analytics, ads, crash-reporting SDK or advertising ID in the app
(`mobile/package.json`). No location, contacts, photos or files permission
(`mobile/app.json`, `modules/kergui-core/android/src/main/AndroidManifest.xml`).

## Data safety form

**Data collection and security**

| Question | Answer | Why |
|---|---|---|
| Does your app collect or share any of the required user data types? | **Yes** | Phone number, device IDs (box serial, phone name) go to the hub when the user signs in |
| Is all of the user data collected by your app encrypted in transit? | **Yes** | The hub URL must be https (`mobile/config/licensing.js` refuses anything else outside localhost) |
| Do you provide a way for users to request that their data is deleted? | **Yes** | E-mail request, described at `<hub>/licensing/v1/privacy#suppression` |

**Data types** (tick only these)

| Category → type | Collected | Shared | Processed ephemerally | Required or optional | Purposes |
|---|---|---|---|---|---|
| Personal info → **Phone number** | Yes | No | No | **Optional** (the app works without an account) | **App functionality**, **Account management**, **Fraud prevention, security and compliance** (one trial per number) |
| Device or other IDs → **Device or other IDs** | Yes | No | No | **Optional** | **App functionality**, **Account management**, **Fraud prevention, security and compliance** (one trial per box, transfer limit) |

Answer **No / not collected** for every other type, in particular:

- **Device or other IDs is not the MAC addresses**: MACs never leave the phone
  (data processed only on the device is not "collected" for Play). It is the
  box serial number and the phone's device name.
- **Location**: the hub sees IP addresses but does not derive a location from
  them.
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
| **Account deletion** | The app creates accounts (WhatsApp sign-in), so this is **required**: web link `<hub>/licensing/v1/privacy#suppression`. Play also asks for an **in-app** path to request deletion: see the gap below |
| **Foreground service permissions** | `FOREGROUND_SERVICE_SPECIAL_USE` (`WatchService`): declare it, use case *"Watches the user's home router for new devices joining the network and alerts the owner; runs only while the user has turned alerts on, with an ongoing notification."* Play asks for a short video of the feature: record Réglages → « Alerter à chaque nouvel appareil » on, the ongoing notification, then a new-device alert |
| **Sensitive permissions** | `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` is on Play's restricted list: be ready to justify it (alerts must keep running on ColorOS / aggressive battery savers), or drop it before submission if the console refuses |

## Known gaps before production (app changes, not console answers)

1. **In-app account deletion**: Play's account-deletion policy wants a way to
   start the request from inside the app (a button or link on the Compte
   screen opening the deletion section). The app has none today.
2. **Privacy policy link inside the app**: Play's User Data policy asks for the
   policy to be reachable in the app too (e.g. Réglages → « Confidentialité »
   opening `<hub>/licensing/v1/privacy`). The app has none today. That link
   must not point at the purchase page.
