# RFC-002 — Parental control / web filtering

- Status: **Proposed** (analysis only; implementation awaits hardware validation)
- Depends on: a sanitised HAR capture of the target router's parental-control pages
- Relates to: [RFC-001](../../RFC-001-router-adapter.md) (router adapter architecture)

## Context

Add a "Parental control / web filtering" module to manage, per device:
enable/disable filtering, adult-content blocking, custom blocked/allowed domains,
different rules per device, and time schedules — from both the web UI and the CLI.

The current target is an **Orange Livebox Fibre on ZTE F6600P firmware**, a model
not yet wired in the project (only `zte_f660` has a validated-shape adapter; the
F6600P needs its own). As everywhere in this project: **do not assume the router
has a documented API**, and **never invent endpoints** — observe first.

## What exists today (reused, not rewritten)

- `port.RouterPort` (login, router info, devices, access rules, block/unblock,
  meta) implemented by a **profile-driven ZTE gateway** (`internal/adapter/router/zte`).
- Device discovery merges LAN-host + WLAN-associated tables by **MAC**.
- Session + credentials: cookie-jar `httpkit.Session`; `CredentialVault`
  (AES-256-GCM/argon2id); credentials never reach the frontend, never logged.
- Modular registry (`router.Factory` + `catalog`); SQLite persistence; REST API
  (`httpapi`) + embedded dashboard (`webui`).

## Detected F6600P capabilities — UNVERIFIED

This cloud environment has **no access to the LAN router**, so no capabilities or
endpoints have been observed. Publicly, this firmware family's "Contrôles
parentaux / Critères de filtrage" typically offers **domain/URL/keyword
filtering** and **MAC-scoped time schedules**, but usually **no real adult-content
categorisation**. Treat all of this as a hypothesis to confirm by capture.

Implication: "adult-content blocking" purely via the router is likely unavailable,
so the design must allow an **external DNS filtering** mechanism rather than
pretending a small blacklist is real content filtering.

## Proposed architecture

### Capability interface (do not bloat RouterPort)
```go
// internal/usecase/port
type Capability string
const (
    CapDevices         Capability = "devices"
    CapAccessControl   Capability = "access_control"
    CapParentalControl Capability = "parental_control"
)

// RouterPort gains:
//   Capabilities() []Capability

// New OPTIONAL port, implemented only by adapters that can:
type ParentalControlPort interface {
    Profiles(ctx) ([]domain.ParentalProfile, error)
    CreateProfile(ctx, domain.ParentalProfile) (domain.ParentalProfile, error)
    UpdateProfile(ctx, domain.ParentalProfile) error
    DeleteProfile(ctx, id string) error
    SetFiltering(ctx, profileID string, enabled bool) error
    FilteringRules(ctx, profileID string) ([]domain.FilterRule, error)
    AddFilteringRule(ctx, profileID string, r domain.FilterRule) error
    RemoveFilteringRule(ctx, profileID, ruleID string) error
}
```
Use cases type-assert `RouterPort` to `ParentalControlPort`; if absent →
`domain.ErrFeatureUnsupported` (the UI explains it). This honours "do not create
methods the router cannot back" and keeps the multi-model core intact. Mapping to
the requested names: `RouterProvider` = `RouterPort` + `Factory`; `ZTEF6600PProvider`
= a `zte_f6600p` adapter package that implements `RouterPort` (and
`ParentalControlPort` once its endpoints are known).

### Three strictly-separated mechanisms (never mixed)
```go
type FilterMechanism string
const (
    MechRouterNative    FilterMechanism = "router_native"   // the router's own filtering
    MechCustomBlacklist FilterMechanism = "custom_blacklist" // domains we push to the router / resolver
    MechExternalDNS     FilterMechanism = "external_dns"     // point the device at a filtering resolver
)
```
Each profile declares exactly one mechanism. Adult-content blocking maps to
`external_dns` unless the router proves it has native categorisation.

### Domain model (only fields the router can actually back)
```go
ParentalProfile { ID, Name, DeviceMACs []MAC, Enabled bool, Mechanism,
                  AdultContentBlocking bool, BlockedDomains, AllowedDomains []string,
                  Schedule, CreatedAt, UpdatedAt }
FilterRule      { ID, Kind (domain|url|keyword|ip), Value, Allow bool }
Schedule        { Enabled bool, Days []Weekday, Start, End string } // only if the router supports it
```
Profiles are stored **locally** (SQLite); the router remains the source of truth
for what it actually applies, reconciled explicitly.

### Multi-device
A profile targets N devices at the use-case level. If the router has no bulk
operation, the backend loops per device with **per-device error handling**
(one failure does not abort the others; the result reports each outcome).

## Integration points (files)
- `internal/domain`: `ParentalProfile`, `FilterRule`, `Schedule`, `FilterMechanism`,
  `ErrFeatureUnsupported`.
- `internal/usecase/port`: `ParentalControlPort`, `Capabilities()`,
  `ParentalProfileRepository`.
- `internal/usecase`: parental interactors (list/create/update/delete, enable/
  disable, add/remove rule), multi-device, confirmation on mutations.
- `internal/adapter/persistence/sqlite`: migration `0002_parental.sql` + repo.
- `internal/adapter/router/ztef6600p`: new profile + registration in `catalog`.
  Parental methods start as `ErrNotImplemented` until the capture lands.
- `internal/adapter/controller/httpapi`: `/api/parental/*` endpoints.
- `internal/webui`: "Contrôle parental" section (cards, Configure/Disable modal,
  confirmations).
- `internal/mockrouter` + `internal/fixtures/zte_f6600p`: model the documented
  contract for end-to-end tests.

## Error handling
Router unreachable, session expired, auth failed, feature unsupported, device
gone, rule rejected, timeout, unexpected firmware response — each mapped to a
domain error and surfaced clearly in the UI/CLI.

## Security
Credentials stay server-side (vault), never sent to the frontend, never logged.
All user input (domains, MAC, times) validated/sanitised. Mutations require a
prior router authentication and explicit confirmation.

## Implementation plan (phased)
1. Domain entities + mechanisms + `ErrFeatureUnsupported` (+ tests).
2. Ports (`ParentalControlPort`, `Capabilities`, repository).
3. SQLite migration + local profile repo (+ tests).
4. Use-case interactors (multi-device, confirmation) with port fakes.
5. `zte_f6600p` adapter: profile + registration + discovery; parental methods
   `NotImplemented` until the HAR capture is provided.
6. Mock + fixtures modelling the documented parental contract; end-to-end tests.
7. REST endpoints `/api/parental/*`.
8. Dashboard "Contrôle parental" section with confirmations.
9. (Optional) external DNS-filtering mechanism behind the same interface.
10. Docs: real F6600P capabilities, firmware limits, how to add another model.

## Tests
Input validation (domains/schedules/MAC); use cases with fakes (create/update/
delete/enable/disable, **device not found**, **session expired**, **feature
unsupported**, rule rejected); adapter integration against the mock; API status
codes. Never use a real password in fixtures.

## Open question (blocking implementation)
A sanitised HAR capture of the F6600P "Contrôles parentaux" and "Critères de
filtrage" pages (method, path+query, headers, cookies, token, payload, response)
is required to wire real endpoints. Until then, the adapter's parental capability
stays honestly `NotImplemented`.
