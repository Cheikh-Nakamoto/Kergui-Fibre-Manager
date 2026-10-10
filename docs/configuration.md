# Configuration & security

## Where settings come from

Every setting is a **flag** (see the [CLI reference](cli.md)); two secrets also
read the **environment** so they never land in argv or shell history:

| Env var | Used for |
|---|---|
| `KERGUI_MASTER_KEY` | Encrypts/decrypts stored router credentials (required for `login` to store, and for `devices`/`inspect`/writes to use stored credentials) |
| `KERGUI_ROUTER_PASSWORD` | Supplies the router password without `--password` |

Defaults: router `http://192.168.1.1`, username `admin`, db `kergui.db`, timeout
`15s`, adapter auto-detected.

## Local data (SQLite)

A single pure-Go SQLite file (`--db`, default `kergui.db`) holds:

- `routers` — managed routers (base URL, adapter, model) — **no credentials**.
- `devices` — the inventory, identity `(router_id, mac)`; IP and state are mutable
  attributes, the MAC is the identity.
- `access_rules` — a local mirror of the router's ACL.
- `credentials` — **only ciphertext** (salt, nonce, encrypted blob).

The file is excluded by `.gitignore`. Delete it to reset local state.

## Credential security (brief §12)

- Router credentials are encrypted at rest with **AES-256-GCM**; the key is
  derived per-record from `KERGUI_MASTER_KEY` via **argon2id** (random salt).
- The plaintext password is never written to disk, never logged, and **never
  returned by the API or the dashboard** (the response DTOs carry no password).
- Decryption fails closed: a wrong `KERGUI_MASTER_KEY` yields an error, not a
  silent empty credential.
- The vault's ciphertext storage is abstracted behind an interface, so the crypto
  is independent of SQLite.

## Router TLS and the network path

- Home routers often present a **self-signed certificate**. `--router-insecure`
  skips verification **for the router connection only**; it affects nothing else.
- The HTTP client keeps `ProxyFromEnvironment`. LAN/localhost hosts are excluded
  by `NO_PROXY`, so router traffic goes direct even when an outbound proxy is set.

## Input validation

MAC addresses are parsed/canonicalised and rejected if malformed (`400`/exit `2`).
Access modes and IPs are validated in the domain layer. Further user input for the
planned parental-control feature (domains, schedules) will be validated the same
way — see [RFC-002](rfcs/RFC-002-parental-control.md).

## Safety posture

- Read operations are safe by default and never modify the router.
- Writes (`block`/`unblock`, and future parental-control mutations) require
  explicit confirmation (`--yes` on the CLI; an explicit POST on the API) and a
  prior successful router authentication.
- A model's write path stays disabled (`501` / `ErrNotImplemented`) until its
  endpoints are verified on real hardware.
