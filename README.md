# Kergui Fibre Manager

> Open-source, **local** administration overlay for the home routers Orange
> Sénégal ships — the "Fiberbox" (ZTE ZXHN **F660**), **F680**, and the ZTE
> **Funbox**. It does **not** replace the router firmware; it sits above the
> existing `192.168.1.1` web interface and gives you a much simpler way to see
> connected devices and manage MAC-based block / allow rules.

```
        BOX ORANGE (192.168.1.1)
                 │   existing web UI (we do not replace it)
                 ▼
        Kergui Fibre Manager  ──►  local SQLite inventory (identity = MAC)
         kergui CLI (this repo)
```

The CLI is `kergui`. The brief that started this project called the product
"OpenRouter Manager"; the name is aligned here to the repository.

---

## Status — Milestone 1 (read-only)

This milestone is the **Router Research & Discovery tool**, on purpose: *observe
first, never invent endpoints.* It is **read-only** — it never changes a router.

| Command | What it does |
|---|---|
| `kergui discover` | Non-destructively fingerprints the router (HTTP headers, title, vendor/model markers) and suggests an adapter. No login. |
| `kergui login --test` | Tests credentials against the router and stores them **encrypted** locally. |
| `kergui devices` | Logs in (read-only) and lists connected devices (MAC / IP / hostname / status). |
| `kergui inspect` | Emits a diagnostic report of how the router's protocol maps to the adapter. |
| `kergui serve` | Starts a local JSON REST API over the same read-only use cases. |

A web dashboard is a later milestone (see
[`docs/architecture/overview.md`](docs/architecture/overview.md)).

### REST API (`kergui serve`)

```sh
kergui serve --addr 127.0.0.1:8080 --router http://192.168.1.1
```

| Method & path | Result |
|---|---|
| `GET /api/health` | liveness + version |
| `GET /api/discover` | router fingerprint (JSON) |
| `GET /api/devices` | device inventory (JSON; uses stored credentials) |
| `GET /api/inspect` | protocol diagnostic report |
| `POST /api/devices/{mac}/block` | `501` until the write path is verified on hardware |
| `POST /api/devices/{mac}/unblock` | `501` until the write path is verified on hardware |

The API never returns the router password to clients. `block`/`unblock` are wired
end-to-end but intentionally reply `501 Not Implemented` until a model's write
path is confirmed on a real device (observe-first).

> ⚠️ **Endpoints are `UNVERIFIED`.** They are documented hypotheses compiled from
> public sources (ZTE manuals, Orange SN assistance, legitimate open-source ZTE
> clients). They must be confirmed on *your own* router before they can be
> trusted. See the validation loop below and
> [`docs/reverse-engineering/README.md`](docs/reverse-engineering/README.md).
> This project uses only legitimate administration of a router you own — it never
> bypasses authentication or exploits vulnerabilities.

## Install / build

```sh
make build         # -> bin/kergui   (pure Go, CGO disabled, no system sqlite needed)
```

## Quick try without a router

```sh
make demo          # boots a bundled mock ZTE F660 and runs discover/login/devices/inspect
```

## Use against your own router

```sh
export KERGUI_MASTER_KEY='a-strong-local-passphrase'   # encrypts stored credentials
bin/kergui discover --router http://192.168.1.1
bin/kergui login    --router http://192.168.1.1 --username admin --test
bin/kergui devices  --router http://192.168.1.1
bin/kergui inspect  --router http://192.168.1.1
```

If a page does not parse, that is expected until the adapter is validated on real
hardware — capture it (see the reverse-engineering guide), drop the sanitized
file into `internal/fixtures/<model>/`, adjust the adapter `profile.go`, and
re-run `make test`.

## Architecture (Clean Architecture, fully modular)

Dependencies point **inward only**. A pure domain core, use-case interactors that
own the *ports*, and interchangeable outer adapters (router gateways, SQLite,
CLI) wired at a single composition root (`cmd/kergui`). Adding a new router model
is one self-registering package implementing `port.RouterPort` — zero changes to
the core. Details: [`docs/architecture/overview.md`](docs/architecture/overview.md)
and [`RFC-001-router-adapter.md`](RFC-001-router-adapter.md).

## Documentation

- [`RESEARCH.md`](RESEARCH.md) — router protocol research (per model, with every
  endpoint marked VERIFIED / UNVERIFIED and its source).
- [`docs/routers/`](docs/routers) — per-model reference.
- [`docs/reverse-engineering/`](docs/reverse-engineering) — how to legitimately
  capture your own router's requests and contribute a validated adapter.

## License

MIT — see [`LICENSE`](LICENSE).
