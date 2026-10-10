# Documentation index

Kergui Fibre Manager — an open-source, local administration overlay for the
routers Orange Sénégal ships (ZTE ZXHN F660 "Fiberbox", F680, Funbox). It does
not replace firmware; it sits above the router's `192.168.1.1` web interface and
gives a much simpler way to see connected devices and manage MAC-based block /
allow rules, from **both** a web dashboard and a CLI, over one shared core.

## Start here
- [Getting started](getting-started.md) — install, build, try without a router,
  connect to your own router, and the validation loop.
- [CLI reference](cli.md) — every command, flag, environment variable, exit code,
  with examples.
- [REST API reference](api.md) — every endpoint, request/response shape, and
  status codes, with `curl` examples.
- [Configuration & security](configuration.md) — flags vs. env, where data is
  stored, how credentials are encrypted, and the TLS/proxy behaviour.

## Understand the design
- [Architecture overview](architecture/overview.md) — the Clean Architecture
  layering, the Dependency Rule, and how to add a router adapter.
- [RFC-001: router adapter architecture](../RFC-001-router-adapter.md) — the
  decision record.

## Router protocols
- [RESEARCH.md](../RESEARCH.md) — the protocol research, with every endpoint
  marked VERIFIED / UNVERIFIED and its source.
- [docs/routers/](routers/) — per-model reference (F660, F680, Funbox).
- [Reverse-engineering guide](reverse-engineering/README.md) — how to capture
  your own router's requests legitimately and contribute a validated adapter.

## The honesty contract (read this)
Every router endpoint in this project is a **documented hypothesis** until it is
confirmed on real hardware. The code was first developed without access to a real
router, so:

- `kergui inspect` and the dashboard surface endpoints as **`UNVERIFIED`**.
- **Write operations** (`block` / `unblock`) are wired end-to-end but return
  `ErrNotImplemented` / HTTP `501` until a model's write path is verified.
- The project only performs **legitimate administration of a router you own** —
  it never bypasses authentication or exploits a vulnerability.

Confirming endpoints on your device turns `UNVERIFIED` into verified and unlocks
writes — see the [reverse-engineering guide](reverse-engineering/README.md).
