# Mobile app — development guide

Part of [Phase 4](README.md). How to build and work on the app in `mobile/`.

## Frozen stack

Versions are the baseline at the start of Phase 4 (October 2026); bump them
deliberately, one PR per major upgrade.

| Concern | Choice | Why |
|---|---|---|
| Framework | **Expo SDK 57** (React Native 0.85+, React 19.2, Hermes V1, New Architecture) | SDK 57 fixes SDK 56's Hermes V1 memory regression |
| Language | TypeScript, `strict: true`, `noUncheckedIndexedAccess` | |
| Navigation | `expo-router` (file-based, typed routes, deep links) | role route groups, `kergui://` and App Links |
| Server state | `@tanstack/react-query` v5 + persister | cache, retries, offline read |
| Client state | `zustand` | session only; everything else is server state |
| API client | `openapi-typescript` (types) + `openapi-fetch` (runtime, ~6 kB) | generated from `docs/openapi.yaml` |
| UI | `react-native-paper` (Material 3) + `react-native-safe-area-context` | native Android look, theming, accessibility |
| Charts | `victory-native` (Skia) | monthly usage per device |
| Secure storage | `expo-secure-store` | Keystore-backed token |
| Biometrics | `expo-local-authentication` | optional app lock |
| QR | `expo-camera` (barcode scanning) | pairing |
| Push | `expo-notifications` | Expo push token, channels |
| Payment | `expo-web-browser` | NabooPay checkout in a Custom Tab |
| i18n | `expo-localization` + `i18next` / `react-i18next` | `fr` default |
| Storage for cache | `react-native-mmkv` | fast persister |
| Errors (optional) | `@sentry/react-native` | must be declared in Data safety if enabled |
| Lint / format | ESLint (expo config) + Prettier | `no-restricted-imports` enforces layers |
| Unit / component tests | Jest (`jest-expo`) + React Native Testing Library + MSW | |
| E2E | Maestro | YAML flows on an emulator |
| Build / release | EAS Build, EAS Submit, EAS Update | AAB signing, Play upload, JS updates |

## Repository layout

```
Kergui-Fibre-Manager/
  cmd/ internal/ …            Go, unchanged
  docs/openapi.yaml           API contract (source of truth)
  mobile/
    app/ src/                 see architecture.md §2
    assets/                   icon, adaptive icon, splash (from internal/webui/assets/logo.png)
    e2e/                      Maestro flows
    app.config.ts             name, package, permissions, plugins, runtimeVersion
    eas.json                  build profiles: development, preview, production
    package.json  tsconfig.json  .eslintrc.cjs
```

`.dockerignore` must also exclude `mobile/` (it already excludes `docs/`), so the
server image and build context do not grow.

## Make targets (added)

| Target | Does |
|---|---|
| `make mobile-types` | `npx openapi-typescript docs/openapi.yaml -o mobile/src/api/schema.d.ts` |
| `make mobile-check` | `cd mobile && npm run typecheck && npm run lint && npm test` |
| `make mobile-dev` | `cd mobile && npx expo start --dev-client` |

## Local workflow

1. **Server with fake data** — no box needed:
   ```sh
   make demo                                  # mock ZTE router
   KERGUI_TRUST_LOOPBACK=true make serve      # API on 127.0.0.1:8080
   ```
2. **Pair the phone** — `bin/kergui pair --role admin` prints a QR.
3. **Reach the server from the phone**:
   - USB: `adb reverse tcp:8080 tcp:8080` and set the dev base URL to
     `http://127.0.0.1:8080` (the only clear-text exception, allowed in the
     `development` build profile only, via a network security config that permits
     `127.0.0.1`);
   - or the real path: `https://app.<domain>` on the LAN once Milestone 4.0 is deployed.
4. **Run the app** — a *development build* (`eas build --profile development`
   once, then `make mobile-dev`). Expo Go is not enough: the app uses native
   modules and config plugins.
5. **Contract change** — edit `docs/openapi.yaml` with the Go change in the same PR,
   run `make mobile-types`; TypeScript errors show every screen to update.

## Configuration

`app.config.ts` reads `APP_VARIANT` (`development` | `preview` | `production`):

| Variant | Package | Name | Clear text |
|---|---|---|---|
| development | `org.cheikhmodiouf.kergui.dev` | Kergui (dev) | `127.0.0.1` only |
| preview | `org.cheikhmodiouf.kergui.preview` | Kergui (test) | no |
| production | `org.cheikhmodiouf.kergui` | Kergui | no |

The server URL is **not** baked in: it comes from the pairing QR, so one build
works for any kergui installation.

Android specifics declared in `app.config.ts`:

- `permissions`: `CAMERA`, `POST_NOTIFICATIONS`, `USE_BIOMETRIC`; later
  `ACCESS_LOCAL_NETWORK` when targeting API 37 (see
  [release-playstore.md](release-playstore.md#4-technical-requirements));
- `blockedPermissions`: anything a plugin adds and the app does not need
  (e.g. `RECORD_AUDIO` from the camera module);
- `intentFilters` with `autoVerify: true` for `https://app.<domain>/pair`;
- `allowBackup: false`.

## Testing strategy

| Level | What | Where |
|---|---|---|
| Unit | formatters (bytes, XOF), MAC helpers, error mapping, QR payload parser | `src/**/*.test.ts` |
| Component | screens with MSW-mocked API: loading, empty, error, 501 hides action | `src/features/**/*.test.tsx` |
| Contract | `schema.d.ts` is up to date (`git diff --exit-code` after `make mobile-types`) | CI |
| E2E | pair → list devices → block (mock router) → unblock; member: pair → see amount → open checkout | `mobile/e2e/*.yaml` (Maestro) |
| Manual | real box F6600P on the LAN and through 4G before each production release | checklist in the release PR |

The Go mock router (`cmd/mockrouter`) is stateful, so E2E flows exercise the real
server and use cases end to end.

## CI (GitHub Actions)

`.github/workflows/mobile.yml`, triggered on changes under `mobile/` or
`docs/openapi.yaml`:

1. `npm ci`, typecheck, lint, Jest.
2. Regenerate types, fail on diff.
3. On tag `mobile-v*`: `eas build --platform android --profile production --non-interactive`
   then `eas submit --track internal`.

Secrets: `EXPO_TOKEN` only (EAS holds the upload key and the Play service account
JSON). The repository has no CI yet: add a `go.yml` running `make check` in the
same step, so the Go contract test guards the spec on every PR.

## Conventions

- Conventional commits, as in the rest of the repository (`feat(mobile): …`).
- One feature folder per API domain; a screen file stays thin (layout + hooks).
- No string literal shown to users outside `src/i18n/*.json`.
- `versionCode` is managed remotely by EAS (`autoIncrement`); `version` follows
  `MAJOR.MINOR.PATCH` of the app, independent of the server version. The app reads
  `api_version` from `/api/v1/health` and shows "update the server" if it is
  older than what it needs.
