# RFC-001 — Router adapter architecture (Clean Architecture)

- Status: Accepted (Milestone 1)
- Scope: the modular, read-only foundation

## Context

Kergui Fibre Manager must support several router models (ZTE F660/F680/Funbox
now; Huawei/TP-Link/Nokia later) without the core knowing any HTTP/HTML detail of
a specific device (brief §9). The protocol for each model is only partially known
and must be validated per device, so corrections must be cheap and localised.

## Decision

Adopt a strict, fully modular **Clean Architecture** with the **Dependency Rule**:
source-code dependencies point inward only.

```
Frameworks & Drivers  cmd/kergui (composition root), modernc sqlite, net/http, flag
Interface Adapters    router gateways, persistence (sqlite), vault, cli, presenters
Use Cases             interactors + PORTS (interfaces owned here)
Domain                entities + value objects (pure, stdlib only)
```

### Key choices
1. **The brief's `RouterAdapter` becomes a port** — `port.RouterPort`, defined in
   the use-case layer. Concrete gateways in the outer ring implement it
   (Dependency Inversion). The core never imports a gateway.
2. **Ports owned by use cases**: `RouterPort`, `RouterFactory`, `DiscoveryPort`,
   `DeviceRepository`, `AccessRuleRepository`, `RouterRepository`,
   `CredentialVault`, `VendorLookup`, `Clock`, `Logger`, `Presenter`.
3. **Profile-driven ZTE gateway**: one `internal/adapter/router/zte` gateway is
   parameterised by a per-model `Profile` (endpoints, login fields, password
   mode). Adding a ZTE model is *data*. Every endpoint carries a `Verified` flag
   that stays `false` until confirmed on hardware.
4. **Modular registry**: `router.Factory` registers adapters; the `catalog`
   package wires the built-in set. Adding a model touches only its package + one
   line in the catalog — never the domain or use cases.
5. **Composition root only**: `cmd/kergui/main.go` is the one place that imports
   concrete adapters and injects them into interactors (manual DI, no framework).
6. **Presenters as output ports**: interactors return DTOs; the CLI controller
   renders them via a `Presenter` (table or JSON). Formatting never leaks inward.
7. **Credentials**: encrypted at rest (AES-256-GCM, key via argon2id from
   `KERGUI_MASTER_KEY`); the ciphertext store is abstracted behind a `Blobs`
   interface so crypto is independent of SQLite (brief §12).
8. **Read-only in M1**: `Block`/`Unblock` return `ErrNotImplemented` and are not
   wired to any command.

### Error taxonomy (brief §14)
Domain sentinels (`ErrRouterUnreachable`, `ErrAuthFailed`, `ErrSessionExpired`,
`ErrUnsupportedModel`, `ErrInvalidMAC`, `ErrNotImplemented`, …) are wrapped by
adapters and mapped to human messages by the CLI.

## Enforcement
`tests/architecture_test.go` runs `go list -deps` and fails if the domain depends
on any outer package, or if the use-case layer depends on anything but the domain
and its own ports.

## Dependencies
Minimal and confined to the outer ring: `modernc.org/sqlite` (pure Go),
`golang.org/x/crypto` (argon2id), `golang.org/x/net/html` (reserved for richer
parsing), `golang.org/x/term` (hidden password prompt). Domain and use cases use
the standard library only.

## Consequences
- Adding a model or swapping the storage engine is local and testable.
- The protocol can be wrong without risk: endpoints live in one `profile.go`, and
  the `Verified` flags keep the honesty contract visible to users (`inspect`).

## Later milestones
M2: write operations behind confirmation + REST controllers + Synchronize.
M3: web dashboard. M4: non-ZTE vendors (proving the core is vendor-neutral).
