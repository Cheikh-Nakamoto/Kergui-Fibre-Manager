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

## Status — Milestone 3 (read + write + dashboard)

*Observe first, never invent endpoints.* The read and write paths are implemented
and gated by per-model `WriteReady` flags. Writes are verified by rereading the
ACL after each change. All endpoints remain **UNVERIFIED** until confirmed on a
real device — the project's core honesty contract.

| Command | What it does |
|---|---|
| `kergui discover` | Non-destructively fingerprints the router (HTTP headers, title, vendor/model markers) and suggests an adapter. No login. |
| `kergui login --test` | Tests credentials against the router and stores them **encrypted** locally. |
| `kergui devices` | Logs in and lists connected devices (MAC / IP / hostname / status). |
| `kergui inspect` | Emits a diagnostic report of how the router's protocol maps to the adapter. |
| `kergui serve` | Starts a local JSON REST API + web dashboard. |
| `kergui block` | Blocks a device by MAC on the router (requires `--mac` and `--yes`). |
| `kergui unblock` | Unblocks a device by MAC on the router (requires `--mac` and `--yes`). |
| `kergui rename` | Sets a device's local custom name (`--mac` and `--name`). |

### Web dashboard + REST API (`kergui serve`)

`kergui serve` runs a local REST API **and** serves a lightweight web dashboard
(no frontend build step, no dependencies) at the same address — so everything is
manageable both from the browser and from the CLI, over one Clean Architecture
core.

```sh
export KERGUI_MASTER_KEY='a-strong-local-passphrase'
kergui serve --addr 127.0.0.1:8080 --router http://192.168.1.1
# then open http://127.0.0.1:8080 in a browser
```

The dashboard (brief §7) shows summary tiles, search / filter / sort, per-device
cards with status and new/blocked badges, details, **rename** (a local custom
name), block/unblock buttons, and a settings panel to test/store credentials. The
password is sent to the backend (stored encrypted) and is **never** returned to
the browser.

| Method & path | Result |
|---|---|
| `GET /api/health` | liveness + version |
| `GET /api/discover` | router fingerprint (JSON) |
| `GET /api/devices` | device inventory (JSON; uses stored credentials) |
| `GET /api/inspect` | protocol diagnostic report |
| `POST /api/devices/{mac}/block` | Blocks a device (gated by `WriteReady`; UNVERIFIED on hardware) |
| `POST /api/devices/{mac}/unblock` | Unblocks a device (gated by `WriteReady`; UNVERIFIED on hardware) |
| `PATCH /api/devices/{mac}` | Sets a device's local custom name |

The API never returns the router password to clients. `block`/`unblock` are
implemented and verified by rereading the ACL after each write; a model whose
write path has not been wired returns `501 Not Implemented`.

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
