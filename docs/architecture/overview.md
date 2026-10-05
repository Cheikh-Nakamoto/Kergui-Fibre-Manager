# Architecture overview

Kergui Fibre Manager is built as a strict, fully modular **Clean Architecture**.
The one rule that governs everything: **dependencies point inward only.**

```
┌───────────────────────────────────────────────────────────────┐
│ Frameworks & Drivers   cmd/kergui (composition root + DI)       │
│ modernc sqlite · net/http · flag · os                           │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Interface Adapters                                        │  │
│  │ router gateways (zte/*)  ·  persistence (sqlite) · vault  │  │
│  │ discovery · cli controller · presenters (table/json)      │  │
│  │  ┌─────────────────────────────────────────────────────┐ │  │
│  │  │ Use Cases (interactors) + PORTS                      │ │  │
│  │  │ Discover · Authenticate · ListDevices · Inspect      │ │  │
│  │  │  ┌────────────────────────────────────────────────┐ │ │  │
│  │  │  │ Domain (entities + value objects, pure stdlib)  │ │ │  │
│  │  │  └────────────────────────────────────────────────┘ │ │  │
│  │  └─────────────────────────────────────────────────────┘ │  │
│  └──────────────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────────────┘
```

## Layers → packages

| Layer | Packages |
|---|---|
| Domain | `internal/domain` (entities, `MAC`/`IP`/`AccessMode` value objects, errors) |
| Use cases + ports | `internal/usecase`, `internal/usecase/port` |
| Interface adapters | `internal/adapter/router/*`, `internal/adapter/discovery`, `internal/adapter/persistence/{sqlite,vault}`, `internal/adapter/controller/{cli,httpapi}`, `internal/adapter/presenter/cli`, `internal/adapter/vendor`, `internal/webui` (embedded dashboard) |
| Frameworks & drivers | `cmd/kergui` (composition root), `internal/infra/*` |

The Dependency Rule is enforced by `tests/architecture_test.go`.

## Request flow (e.g. `kergui devices`)

```
CLI controller → ListDevices interactor → RouterFactory.New(adapter)
     → RouterPort gateway (ZTE) --HTTP--> router
     → interactor reconciles with DeviceRepository (SQLite), flags new MACs
     → returns []DeviceView → Presenter (table/json) → stdout
```

The interactor talks only to ports; the gateway, repositories and presenter are
injected by `cmd/kergui/main.go`.

## How to add a router adapter

1. Create `internal/adapter/router/<model>/<model>.go`.
2. For a ZTE model, return a `zte.Profile` (endpoints, login fields, password
   mode, discovery markers) and a `Register(*router.Factory)` function. For a
   non-ZTE vendor, implement `port.RouterPort` directly in the new package.
3. Add one line to `internal/adapter/router/catalog/catalog.go`.
4. Add fixtures under `internal/fixtures/<model>/` and tests.

The domain and use-case layers never change.

## Honesty contract

Every adapter endpoint carries a `Verified` flag (`false` until confirmed on real
hardware). `kergui inspect` surfaces these as `UNVERIFIED`, and write operations
are not implemented in Milestone 1. See
[`../reverse-engineering/README.md`](../reverse-engineering/README.md) for the
capture/validation loop.
