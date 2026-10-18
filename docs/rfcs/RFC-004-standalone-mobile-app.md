# RFC-004 — Stand-alone mobile app (v1)

- Status: **Accepted** — implementation started (`mobilecore/`, `mobile/`, CI)
- Supersedes for v1: [RFC-003](RFC-003-mobile-app.md), which becomes the plan of an
  optional **v2 "hub"** (server, remote access, members, payments, push)
- Detailed docs: [docs/mobile/](../mobile/README.md)

## Context

RFC-003 designed an Android app that is a client of the Go server. The
product need changed:

| # | Requirement |
|---|---|
| S1 | On the **App Store and Google Play**. |
| S2 | **No payment inside the app**: the app is **sold once** (paid app, no in-app purchase, no subscription). |
| S3 | **No backend in v1**: the phone alone is enough; nothing to install on an always-on machine. |
| S4 | Keep the core's rules: credentials never returned to the UI nor logged, writes confirmed, read-after-write, endpoints `UNVERIFIED` until proven, 501 hides an action. |

So in v1 the phone talks **directly to the box** (`192.168.1.1`) on the home
Wi-Fi. Everything that needs an always-on machine — captive portal, members and
invoices, NabooPay, continuous traffic metering, push, access from 4G — moves to
the v2 hub.

The hard part is the box protocol. The F6600P serves **HTTPS with a
self-signed certificate**, a session cookie `SID_HTTPS_`, a
`sha256(password+token)` login, and signs writes with **RSA PKCS#1 v1.5** using a
key scraped from the page's JavaScript
(`internal/adapter/router/ztef6600p/gateway.go`). React Native's `fetch` rejects
self-signed certificates and handles cookies through the platform stack; all of
this is already written and tested in Go.

## Decision

**Expo (React Native, TypeScript) for the UI, and the existing Go core compiled
into the app with `gomobile bind`.**

```
Phone (home Wi-Fi)
 ┌───────────────────────────────────────────────┐
 │ Expo / RN screens  (mobile/src/app)           │
 │   src/core/client.ts ── KerguiCore.request()  │  only module that knows the bridge
 │ Expo module (Kotlin / Swift)                  │  mobile/modules/kergui-core
 │ gomobile library: mobilecore.Core             │  mobilecore/
 │   in-memory httpapi (same JSON API as serve)  │
 │   ├ use cases: list, block, rename, wifi,     │
 │   │  MAC filter, discover, inspect            │
 │   ├ adapters ZTE F6600P / F660 / F680         │
 │   ├ SQLite inventory + AES-GCM vault          │
 │   └ mock router (demo mode)                   │
 └──────────────────────┬────────────────────────┘
                        │ HTTP(S) on the LAN
                  ZTE box 192.168.1.1
```

### The core: one API, in memory

`mobilecore` is a second composition root next to `cmd/kergui`. Rather than a
new RPC surface, it serves the **same JSON API as `kergui serve`**
(`internal/adapter/controller/httpapi`) **in memory**: the app calls
`Request(method, path, body)` and gets the status and body the dashboard would
get, with the same error mapping (401 credentials, 501 not supported, 502 box
unreachable, 503 box busy). No socket is opened. Only the v1 routes are
reachable (an allow-list in `mobilecore/core.go`); billing, members, payments,
logs, the captive portal and the static dashboard answer 404.

gomobile-friendly surface (`mobilecore/core.go`):

| Go | Kotlin (`org.kergui.mobilecore`) | Swift |
|---|---|---|
| `Open(dataDir, vaultKey, logger) (*Core, error)` | `Mobilecore.open(...)` | `MobilecoreOpen(..., &err)` |
| `(*Core) Configure(url, adapterID, pinSHA256) error` | `core.configure(...)` | `try core.configure(...)` |
| `(*Core) Request(method, path, body) (*Response, error)` | `core.request(...)` | `try core.request(...)` |
| `CertificateFingerprint(url) (string, error)` | `Mobilecore.certificateFingerprint(...)` | `MobilecoreCertificateFingerprint(..., &err)` |
| `(*Core) StartDemo() error` / `StopDemo()` | `core.startDemo()` | `try core.startDemo()` |

Methods return either `error` or a pointer with `error`, so Swift imports them as
`throws`.

### Trust on first use for the box certificate

`port.RouterOptions` gains `PinSHA256`. With `InsecureTLS`, `httpkit` then
accepts the self-signed certificate only if its leaf SHA-256 matches the pin
(`VerifyConnection`). The app reads the fingerprint once
(`CertificateFingerprint`), shows it, stores it with the session, and passes it
to `Configure` from then on. A different certificate fails as a 502 naming "pinned
fingerprint", which the app turns into a warning. Go sockets do not go through
Android's network security config nor iOS ATS, so neither needs a clear-text or
self-signed exception; plain-HTTP F660 boxes work the same way.

### Secrets on the phone

- A random 32-byte **vault key** is generated on first launch and kept in
  `expo-secure-store` (Keychain `WHEN_UNLOCKED_THIS_DEVICE_ONLY` / Android
  Keystore). It is the passphrase of the existing AES-GCM vault.
- The router password goes to the core once (`POST /api/login`) and is stored
  encrypted in the core's SQLite file, in a non-backed-up directory
  (`noBackupFilesDir` on Android, Application Support excluded from backup on
  iOS; `allowBackup: false`).

### Demo mode

`StartDemo` runs the bundled mock router (`internal/mockrouter`) on
`127.0.0.1:<random>` and points the core at it (login `admin` / `admin`). Store
reviewers have no ZTE box: this is how they review the app, and it doubles as a
try-before-you-understand screen. The Maestro flow runs the same path in CI.

### Build and release: GitHub Actions only

No EAS account. `expo prebuild` generates `android/` and `ios/` in CI (they are
not committed); Gradle builds on Ubuntu, Xcode + fastlane on macOS. Signing
material is created once by `scripts/mobile/setup-signing.sh` and stored as
secrets of the protected `release` environment with `gh secret set`. Details in
[mobile/release-signing.md](../mobile/release-signing.md).

## Scope of v1

In: discovery and model detection, credential test and storage, device list
(search, sort, new / blocked badges), local rename, block / unblock, Wi-Fi
networks (on/off, name, password), MAC filter switch, "new since last time"
(inventory diff), demo mode, French, light / dark theme.

Out (v2 hub, RFC-003): members, invoices, NabooPay, captive portal, metering
over time, push, remote access, parental control.

## Options considered

| | **Go core via gomobile** (chosen) | Rewrite the adapters in TypeScript | Flutter + gomobile |
|---|---|---|---|
| Box protocol | reused, tested, one implementation for CLI, server and app | second implementation to keep in sync with every firmware finding | reused |
| Self-signed HTTPS, cookies | Go `net/http`, pinned | needs native TLS overrides on both platforms | reused |
| Binary size | +~6 MB per ABI downloaded (13.7 MB `.so` uncompressed) | small | same as chosen |
| Toolchain | Go + NDK + Xcode in CI | JS only | Dart (new language) |
| Risk | gomobile is "experimental" upstream | protocol drift | language switch |

The spike (M0) confirmed the risky parts on 2026-10-18: `mobilecore` with the
pure-Go SQLite (`modernc.org/sqlite`) cross-compiles for `android/arm64`, `arm`,
`amd64` and `ios/arm64`, and `gomobile bind -target=android` produces a working
`.aar`, and `expo prebuild` wires the module and the release signing. The full
Gradle build of the app has not completed yet (the workstation ran out of disk);
it runs in `mobile-ci.yml`. The iOS `.xcframework` and the iOS app build only
run on the macOS CI runners.

### SQLite on Android

The first emulator run crashed at launch: Android's seccomp filter killed the
app on `lstat` (x86_64 syscall 6). `modernc.org/libc` issues legacy system calls
(`lstat`, `stat`, `open`, `access`) directly on `linux/amd64`, and Android
forbids them for apps; on arm64 it goes through transpiled musl and its `*at`
calls. Rather than ship an app that dies on x86_64 (emulators, Chromebooks), the
Android build uses **`github.com/ncruces/go-sqlite3`**: real SQLite compiled to
WebAssembly, run by wazero, with I/O through Go's `os` package. The driver is
chosen by build tag (`internal/adapter/persistence/sqlite/driver_*.go`): `android`
or `kergui_wasmsqlite` → ncruces; otherwise modernc, unchanged for the CLI, the
server and iOS. CI runs the whole Go suite with both drivers.

## Stores and selling

- **Paid app** on both stores, a fixed price, no IAP: no billing SDK and no
  receipt validation (there is no server to do it).
- **Blocking check**: Google Play paid apps need a **merchant (payments) profile**
  in a country where Google supports merchant registration. Senegal is listed
  among the countries where users can *buy* paid apps; whether a *developer*
  based there can register as a merchant must be confirmed in the Play Console
  before the store work starts. Fallback: an organization account in a
  supported country. Apple: sign the *Paid Apps Agreement*, banking and tax forms.
- Fees: Apple Developer 99 USD / year, Google Play 25 USD once. Enrol in Apple's
  Small Business Program and Google's 15 % tier.
- Google, personal account: closed test with 12 testers for 14 days before
  production (unchanged from RFC-003).
- No Orange name or logo anywhere (impersonation policy): "compatible with ZTE
  F6600P / F660 boxes, independent of Orange and ZTE".
- Privacy: no data collected, no analytics, no network calls beyond the LAN →
  "Data not collected" (App Store) and an empty Data safety form (Play).
- The code is open source while the app is paid: anyone can build the APK. That
  is a business choice — the store build sells convenience and updates.

## Consequences

- `mobilecore/` is the third consumer of the use cases (CLI, server, app); a
  breaking change in `httpapi` JSON shapes now also breaks the app — the Jest
  client tests and the Maestro flow catch it.
- `go.mod` gains `golang.org/x/mobile` as a **tool** dependency (`tool` block); it
  is not linked into `kergui`.
- `port.RouterOptions.PinSHA256` is available to the CLI and server too (unused
  there for now).
- iOS builds cannot be produced on the Linux workstation; they need the macOS
  runner, or a Mac.

## Follow-ups

- Android: bind the process to the Wi-Fi network (`bindProcessToNetwork`) so
  router calls do not go to mobile data when the Wi-Fi has no Internet.
- Android 17 (API 37): request `ACCESS_LOCAL_NETWORK` at runtime once the app
  targets 37 (declared in the manifest already).
- Pin third-party GitHub Actions to commit SHAs in `mobile-release.yml`.
- F6600P Wi-Fi in demo mode: the mock router imitates the F660, so Wi-Fi shows
  "not supported" there; a mock F6600P would demo that screen too.
