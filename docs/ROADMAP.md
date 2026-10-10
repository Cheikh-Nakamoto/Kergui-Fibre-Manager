# Roadmap

Living plan for Kergui Fibre Manager. Status reflects what is in the repository;
every router endpoint remains **UNVERIFIED** until confirmed on real hardware (see
the honesty contract in [docs/README.md](README.md)).

## Done

### Milestone 1 — Research & read-only discovery tool
- Clean Architecture skeleton (domain → use cases + ports → adapters → composition
  root), with the Dependency Rule enforced by `tests/architecture_test.go`.
- Domain entities + value objects (`MAC`, `IP`, `AccessMode`) and error taxonomy.
- Non-destructive discovery (fingerprint), profile-driven ZTE gateway, read path
  (login, router info, devices merged by MAC, access rules).
- SQLite inventory + AES-256-GCM/argon2id credential vault.
- CLI: `discover`, `login [--test]`, `devices`, `inspect`.
- Mock router, unit + integration tests, docs (RESEARCH, RFC-001, per-router,
  reverse-engineering guide).

### Milestone 2 — REST API
- JSON API + `kergui serve` over the same use cases.
- Block/unblock interactors (confirmation-required).

### Milestone 3 — Web dashboard
- Embedded dependency-free dashboard served by `serve` (summary, search/filter/
  sort, cards, details, badges, settings/login panel).
- Local rename (custom name) via use case + `PATCH /api/devices/{mac}`.

### Write activation
- ZTE ACL write path implemented (real POST), gated by `Profile.WriteReady`;
  CLI `block` / `unblock` (`--yes`) and `rename`; end-to-end tested against the
  stateful mock. Endpoints still UNVERIFIED on hardware.

## In progress / next

### Validate on real hardware (highest priority)
Confirm the ZTE F660 (and new models) endpoints on a real router via the capture
loop ([reverse-engineering guide](reverse-engineering/README.md)); flip
`Verified: true` and wire any per-request CSRF token. Nothing downstream is
trustworthy until this is done.

### New model: ZTE F6600P (Orange Livebox Fibre)
Add a `zte_f6600p` adapter (profile + registration). Discovery/read first, then
write, each validated by capture.

### Feature: Parental control / web filtering
Planned in [rfcs/RFC-002-parental-control.md](rfcs/RFC-002-parental-control.md).
Capability-interface design, three strictly-separated mechanisms
(router-native / custom blacklist / external DNS), local profile store,
per-device profiles with schedules. Blocked on a HAR capture of the F6600P
"Contrôles parentaux / Critères de filtrage" pages.

### Later
- More adapters (Huawei, TP-Link, Nokia) — proving the core is vendor-neutral.
- Periodic background Synchronize as its own scheduled interactor.
- Optional OUI vendor database.

## Principles (unchanged across milestones)
- Observe first; never invent endpoints.
- The core never learns a router's HTTP/HTML details.
- Credentials encrypted at rest, never returned to the frontend, never logged.
- Read-only is safe by default; writes require explicit confirmation.
