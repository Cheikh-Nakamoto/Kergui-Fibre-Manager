# Mobile app — signing and CI release

Part of [the mobile app](README.md), decision in
[RFC-004](../rfcs/RFC-004-standalone-mobile-app.md). Builds run only on GitHub
Actions; signing material lives in the secrets of the protected `release`
environment and is created once with `scripts/mobile/setup-signing.sh`.

## 1. One-time setup

Prerequisites on the workstation: `gh` (logged in with admin rights on the repo),
`openssl`, `keytool` (any JDK).

```sh
scripts/mobile/setup-signing.sh all          # Android upload keystore + iOS key and CSR
```

Then the manual steps it prints:

| Step | Where | Then run |
|---|---|---|
| Upload the CSR, type **Apple Distribution**, download `distribution.cer` | developer.apple.com → Certificates | `scripts/mobile/setup-signing.sh ios-p12 ~/Downloads/distribution.cer` |
| Create an API key, role **App Manager**, download `AuthKey_XXXX.p8` (one download only) | App Store Connect → Users and Access → Integrations | `scripts/mobile/setup-signing.sh asc-key AuthKey_XXXX.p8 <key-id> <issuer-id>` |
| Create the app (`org.kergui.app`), then a service account with the **Release manager** permission, download its JSON key | Play Console + Google Cloud | `scripts/mobile/setup-signing.sh play-sa key.json` |
| Add the repository **variable** `APPLE_TEAM_ID` (10 characters, not secret) | GitHub → Settings → Variables | — |
| Add **required reviewers** to the `release` environment | GitHub → Settings → Environments | — |

Check: `scripts/mobile/setup-signing.sh check` lists the ten secrets.

### What the script guarantees

- No secret is printed or put on a command line: passwords come from
  `openssl rand`, reach `keytool` / `openssl` through environment variables
  (`-storepass:env`, `-passout env:`) and reach `gh secret set` on stdin.
- Every generated file is copied to `~/.kergui-signing/` (or `--backup-dir`),
  mode 600, and the script refuses a backup directory inside the repository.
  Move that folder to a password manager or an encrypted drive.
- It refuses to overwrite an existing secret without `--force`, and refuses a
  `.cer` that was not issued for the stored key.
- `--dry-run` does everything except writing to GitHub; `--repo` and `--env`
  target another repository or environment.

### Secrets

| Secret | Made by | Used by |
|---|---|---|
| `ANDROID_UPLOAD_KEYSTORE_B64` | `android` (PKCS12, RSA 4096, alias `upload`, 10 000 days) | `mobile-release.yml` → `withReleaseSigning` |
| `ANDROID_UPLOAD_STORE_PASSWORD`, `ANDROID_UPLOAD_KEY_PASSWORD` | `android` (same value: PKCS12 has one password) | idem |
| `ANDROID_UPLOAD_KEY_ALIAS` | `android` | idem |
| `PLAY_SERVICE_ACCOUNT_JSON` | `play-sa` | upload to the Play internal track |
| `IOS_DIST_P12_B64`, `IOS_DIST_P12_PASSWORD` | `ios-p12` (3DES/SHA1 PKCS12, which the macOS keychain imports) | fastlane `import_certificate` |
| `ASC_KEY_P8_B64`, `ASC_KEY_ID`, `ASC_ISSUER_ID` | `asc-key` | fastlane: provisioning profile + TestFlight upload |

The keystore is only the **upload key**: with Play App Signing, Google holds the
key that signs what users install. A lost upload key can be reset from the Play
Console; a lost Apple certificate is revoked and re-created (`ios-csr --force`,
then `ios-p12 --force`). The `.p8` can only be downloaded once — keep the backup.

## 2. Workflows

| Workflow | Trigger | Does |
|---|---|---|
| `mobile-core.yml` | called by the two others | `make mobile-core-android` (Ubuntu, NDK 28) or `make mobile-core-ios` (macOS, Xcode) → artifact `kergui-core-<platform>` |
| `mobile-ci.yml` | PR / push touching `mobile/`, `mobilecore/`, `internal/`, `go.mod` | `make check`; `tsc`, `expo lint`, Jest; unsigned Android release build; **Maestro demo flow on an emulator**; iOS simulator build (`CODE_SIGNING_ALLOWED=NO`). No secrets |
| `mobile-release.yml` | **push to `main`** touching `mobile/`, `mobilecore/`, `internal/` or `go.mod`; tag `mobile-vX.Y.Z`; manual run | in the `release` environment: signed APK → **GitHub Release** (a tag gives a full release on that tag; `main` or a manual run gives a prerelease `mobile-vX.Y.Z-build.N`, with a `.sha256`); signed AAB → Play **internal** track (status `draft` until the app's first review) when `PLAY_SERVICE_ACCOUNT_JSON` is set; signed IPA → **TestFlight**. Android needs only the upload keystore; a platform whose secrets are missing is skipped with a notice |

Version numbers: the store version is the tag (`mobile-v1.2.0` → `1.2.0`), the
manual input, or `expo.version` of `mobile/app.json` for a push to `main`; the
build number is `github.run_number`, so each push to `main` uploads a new
internal build of the same version (`app.config.js` reads
`KERGUI_VERSION` and `KERGUI_BUILD_NUMBER`).

Android signing goes through the config plugin `mobile/plugins/withReleaseSigning.js`:
`expo prebuild` regenerates `android/`, so nothing is edited by hand. Without
`KERGUI_UPLOAD_STORE_FILE` the release build stays debug-signed — which is what
`mobile-ci.yml` builds and tests.

iOS: `mobile/fastlane/Fastfile`, lane `ios beta` — temporary keychain, import of
the `.p12`, App Store profile fetched (or created) with the API key, manual
signing, `build_app`, `upload_to_testflight`. The keychain and the `.p12` are
deleted in an `if: always()` step.

## 3. Cutting a release

Every merge to `main` that touches the app already produces an internal build
(Play internal track + TestFlight). For a new store version, bump
`expo.version` in `mobile/app.json` in the PR, or tag:

```sh
git tag mobile-v1.0.0 && git push origin mobile-v1.0.0
```

Approve the `release` environment in the Actions run (if reviewers are set). Then promote in the stores:
Play internal → closed → production; TestFlight → App Store review.

## 4. Cost

The repository is public, so standard GitHub-hosted runners, macOS included, are
free. If it ever becomes private, a macOS minute counts ten times: keep iOS jobs
to releases and native changes.
