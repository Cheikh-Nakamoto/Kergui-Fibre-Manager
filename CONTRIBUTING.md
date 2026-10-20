# Contributing to Kergui Fibre Manager

Thanks for being here. This project talks to the router that sits in someone's
living room and handles its administration password, so contributions are held
to one rule above all others: **never assume how a router works — observe,
capture, understand, document, then build.** Everything below follows from that.

Issues and pull requests are welcome in **French or English**; the code,
identifiers and commit subjects stay in English, and mobile user-facing strings
stay in French (`mobile/src/i18n/fr.ts`).

---

## Ways to contribute

They are listed by how much they help the project, not by how hard they are.

### 1. Confirm endpoints on your own router (the most valuable thing you can do)

Every router endpoint in this repository is a **documented hypothesis** marked
`UNVERIFIED`, compiled from public sources (ZTE manuals, Orange SN assistance,
legitimate open-source ZTE clients). Much of the code was written without access
to a real device. Confirming an endpoint on hardware you own turns `UNVERIFIED`
into verified and unlocks the write path for that model.

What this looks like:

1. `bin/kergui discover --router http://192.168.1.1` and
   `bin/kergui inspect --router …` on your box.
2. Capture the real pages by following
   [`docs/reverse-engineering/README.md`](docs/reverse-engineering/README.md).
3. **Sanitise the capture** — step 3 of that guide is not optional. No MAC
   addresses, IPs, cookies, tokens, SSIDs, serial numbers or passwords of real
   people, yours included.
4. Drop the sanitised file into `internal/fixtures/<model>/`, adjust the
   adapter's `profile.go`, flip the status in
   [`docs/routers/research.md`](docs/routers/research.md) with the capture as its
   source, and run `make test`.

Only legitimate administration of a router **you own** is in scope. Anything
that bypasses authentication, exploits a vulnerability, or targets a device you
are not responsible for will be refused, regardless of how well it works.

### 2. Add a router model

Adding a model is one self-registering package implementing `port.RouterPort`,
with **zero changes to the core** — that is the whole point of the layering. See
[`docs/architecture/overview.md`](docs/architecture/overview.md) and
[RFC-001](docs/rfcs/RFC-001-router-adapter.md), then copy
`internal/adapter/router/ztef660/` as a starting shape.

A new adapter is expected to ship with: fixtures from a real capture, a parser
test against those fixtures, a `WriteReady` flag that is `false` until writes
have been verified by rereading the ACL, and a page in
[`docs/routers/`](docs/routers/).

### 3. Report a device-recognition miss

If Kergui shows one physical device as two, or merges two devices into one
(private/randomised MAC, DHCP churn, a phone that rotates its address), that is
a concrete, useful bug. Describe the scenario and what you expected; see
[`docs/recognition/`](docs/recognition/) for how identity is currently derived
and where its limits are. Please do not paste real MACs — mask the last three
bytes.

### 4. Code

| Area | Where | Checks |
|---|---|---|
| Domain / use cases / adapters (Go) | `internal/`, `cmd/kergui` | `make check` |
| Web dashboard (no build step, no deps) | `internal/webui/` | `make check`, then `make serve` |
| Hub, relays, trust, notifier (RFC-005) | `internal/hub`, `internal/agent`, `internal/trust`, `cmd/kergui-hub` | `make hub-test` (needs Docker) |
| Mobile app (Expo / React Native) | `mobile/`, Go core in `mobilecore/` | `npx expo lint`, `npx tsc --noEmit`, `npm test` |

Two things to know before touching the Go code:

- **The Dependency Rule is enforced by a test.** `tests/architecture_test.go`
  fails if `internal/domain` imports anything outer, or if `internal/usecase`
  imports a concrete adapter or framework. Dependencies point inward only.
- **Pure Go, `CGO_ENABLED=0`.** Do not introduce a dependency that needs cgo or
  a system library (that includes the usual sqlite bindings).

For the mobile app, read [`mobile/AGENTS.md`](mobile/AGENTS.md) first: install
packages with `npx expo install`, never edit the generated `android/` or `ios/`
directories, keep every user-visible string in `src/i18n/fr.ts`, and put a
`confirm()` in front of every router write.

Heavy native builds (gomobile `.aar`, the Android app, the iOS framework) run in
GitHub Actions — see `.github/workflows/` and
[`docs/mobile/development.md`](docs/mobile/development.md).

### 5. Documentation and translation

Docs live in [`docs/`](docs/README.md) and the index there is the map. Fixing a
stale command, clarifying a page, or translating one is a real contribution —
and if a document contradicts the code, the bug is worth reporting even if you
do not fix it.

### 6. Triage

Reproducing someone else's report on your own hardware, confirming which model
and firmware it happens on, or narrowing a parse failure to one page, moves
things forward more than it looks.

---

## Development setup

```sh
make build         # -> bin/kergui (pure Go, CGO disabled)
make demo          # boots a bundled mock ZTE F660 and runs the read-only flow
make check         # fmt-check + vet + test — run this before every PR
make help          # all targets
```

Go 1.26 or later. No router is needed to develop: `cmd/mockrouter` serves
mock F660 and F6600P pages (`make mock`), and the hub tests spin up a throwaway
Postgres with `make hub-db`.

For the mobile app:

```sh
cd mobile && npm install && npx expo start
```

## Pull requests

- Branch off `main`, named after the change: `feat/hub-notifier`,
  `fix/device-rows`, `ci/ndk-retry`, `docs/licence`.
- **Conventional commits** with a scope that names the area:
  `feat(hub): …`, `fix(mobile): …`, `ci(mobile): …`, `docs: …`.
- `make check` green. If you touched the hub, `make hub-test` too; if you
  touched `mobile/`, lint and typecheck.
- Keep the PR to one subject. A new adapter, a UI change and a refactor belong
  in three PRs.
- Say in the description what you ran it against: the mock, or a real model and
  its firmware version. "Not tested on hardware" is a perfectly acceptable
  answer — a wrong claim is not.
- Never widen an endpoint's status from `UNVERIFIED` without a capture to back
  it, and never commit an unsanitised capture, a `.env`, a database, a log, or a
  signing key. `.gitignore` covers the usual ones; check `git status` anyway.

## Security and privacy

If you find something that exposes stored credentials, the master key, or a
router's administration session, please report it privately by
[opening an issue](https://github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/issues)
asking for a contact rather than describing the flaw in public first.

Kergui's inventory is a list of the devices in someone's home. Treat every
sample, screenshot and test fixture as if it described your own household.

## Licensing of contributions

The project is released under the **Business Source License 1.1** (see
[`LICENSE`](LICENSE) and [`docs/licence.md`](docs/licence.md) for what it means
in plain words). By proposing a contribution you accept that it is published
under the project's licence, including the automatic change to **Apache-2.0**
four years after each release, and that the author may grant commercial licences
covering the whole project.

Commits published before 2026-10-19 were released under the MIT License and stay
available under it ([`LICENSE-MIT`](LICENSE-MIT)).
