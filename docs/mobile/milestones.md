# Mobile app — milestones

Part of [the mobile app](README.md). v1 is stand-alone
([RFC-004](../rfcs/RFC-004-standalone-mobile-app.md)); the server-backed "hub"
of [RFC-003](../rfcs/RFC-003-mobile-app.md) is v2. Estimates assume one
developer, part time.

## v1 — stand-alone, paid, App Store + Google Play

| Milestone | Scope | Exit criteria | Estimate | State |
|---|---|---|---|---|
| **M0 Spikes** | gomobile bind of the core with pure-Go SQLite; Expo module; merchant account check | `devices.list` on the real F6600P from an Android phone and an iPhone | 1 week | Android `.aar` and cross-compiles done; device and iOS tests and the merchant check open |
| **M1 Mobile core** | `mobilecore` (in-memory API, route allow-list), certificate pinning, demo mode, tests | `make check` green; `go test ./mobilecore` | 1–2 weeks | done |
| **M2 App** | welcome / demo, connect (TOFU), devices (search, badges, rename, block), Wi-Fi + MAC filter, settings; CI + signing | Maestro demo flow green in CI; real use on the box | 3–4 weeks | first version of every screen; CI and signing written |
| **M3 Stores** | listings, privacy policy, screenshots from demo mode, TestFlight + Play closed test (14 days), price | public, paid app on both stores | 3 weeks (14 days of test) | not started |

## v2 — hub (optional server)

The former Phase 4 plan, unchanged in substance: backend prerequisites (device
tokens, roles, `/api/v1`, TLS on the LAN, tunnel), remote access, push, member
space and payments. See [backend-prerequisites.md](backend-prerequisites.md),
[architecture.md](architecture.md) and [security.md](security.md). The v1 app
would gain a "hub" mode next to its direct mode, not be replaced.

## Risks (v1)

| Risk | Mitigation |
|---|---|
| gomobile is "experimental" upstream | pinned in `go.mod` (`tool`); the bridge is tiny (one `Request`); fallback: TS SQLite + stateless core |
| No Google merchant account from Senegal | check in the Play Console before M3; organization account elsewhere |
| Binary size (+~6 MB per ABI) | `-ldflags=-s -w`; Play serves one ABI per device |
| `UNVERIFIED` endpoints on other models | the UI hides what answers 501 |
| iOS local-network prompt refused | explain, link to Settings |
| Open-source code, paid app | business choice; the store build sells convenience and updates |
| A signing secret leaks | protected `release` environment, secrets never echoed, files removed in `if: always()`, backups outside the repo |
