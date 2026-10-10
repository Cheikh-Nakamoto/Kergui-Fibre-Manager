# Getting started

## Requirements

- **Go** (the module targets a recent toolchain; the pure-Go SQLite driver pulls
  the toolchain it needs automatically). No cgo, no system SQLite, no C compiler.
- A terminal. Optionally a browser for the dashboard.

Nothing else: the binary is self-contained and has no cloud dependency.

## Build

```sh
make build            # -> bin/kergui   (CGO disabled, pure Go)
# or:
go build -o bin/kergui ./cmd/kergui
```

Other useful targets: `make test`, `make vet`, `make fmt`, `make cover`,
`make demo`, `make help`.

## Try it without a router

The repo bundles a mock ZTE F660 that serves synthetic fixtures, so you can see
the whole flow (read + write) with no hardware:

```sh
make demo
```

This builds `bin/kergui` and `bin/mockrouter`, starts the mock, and runs
`discover`, `login --test`, `devices`, and `inspect` against it.

To explore the **dashboard** against the mock:

```sh
make mock && ./bin/mockrouter -addr 127.0.0.1:18080 &     # terminal 1
export KERGUI_MASTER_KEY='demo'
./bin/kergui serve --addr 127.0.0.1:8080 --router http://127.0.0.1:18080   # terminal 2
# open http://127.0.0.1:8080, open "Réglages", log in with admin / admin
```

## Use it against your own router

> All endpoints are **UNVERIFIED** until confirmed on your device (see below).
> Use only on a router you own or administer.

```sh
export KERGUI_MASTER_KEY='a-strong-local-passphrase'   # encrypts stored credentials

# 1. Identify the router (no login, non-destructive)
kergui discover --router http://192.168.1.1

# 2. Test + store your credentials (encrypted at rest)
kergui login --router http://192.168.1.1 --username admin          # prompts for the password
#   or just test the connection without storing:
kergui login --router http://192.168.1.1 --username admin --test

# 3. List devices; flags new MACs since last run
kergui devices --router http://192.168.1.1
kergui devices --router http://192.168.1.1 --json      # machine-readable

# 4. Diagnostic report of how the protocol maps to the adapter
kergui inspect --router http://192.168.1.1

# 5. Give a device a friendly local name
kergui rename --router http://192.168.1.1 --mac AA:BB:CC:DD:EE:FF --name "Téléphone Maman"

# 6. Browser UI + REST API (same data, same core)
kergui serve --router http://192.168.1.1 --addr 127.0.0.1:8080
```

`block` / `unblock` exist but reply that the write path is not yet available
until you validate it (next section). See the full [CLI reference](cli.md).

## The validation loop (turning UNVERIFIED into working)

Because router firmware varies, the adapter's endpoints start as documented
hypotheses. To confirm them on your device:

1. Run `kergui inspect` and note which parts don't match.
2. Capture the real pages with your browser's DevTools / a HAR export
   (see the [reverse-engineering guide](reverse-engineering/README.md)).
3. **Sanitise** the captures (strip cookies, passwords, real MAC/IP).
4. Drop them in `internal/fixtures/<model>/`, adjust the model's `profile.go`,
   and run `make test`.
5. Once confirmed, set `Verified: true` (and, for write operations,
   `WriteReady`/wire the real POST) in the profile.

Please contribute validated profiles + sanitised fixtures back so others benefit.

## Where your data lives

- Inventory + access-rule mirror + encrypted credentials: a local SQLite file
  (`--db`, default `kergui.db`).
- The master passphrase (`KERGUI_MASTER_KEY`) is read from the environment and is
  never written to disk or returned by the API.

See [Configuration & security](configuration.md) for details.
