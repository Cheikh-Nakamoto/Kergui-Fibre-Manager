# CLI reference

The binary is `kergui`. Every router endpoint is **UNVERIFIED** until confirmed on
real hardware; write operations require explicit confirmation.

```
kergui <command> [flags]
```

## Common flags (accepted by every command)

| Flag | Default | Meaning |
|---|---|---|
| `--router URL` | `http://192.168.1.1` | Router base URL |
| `--adapter ID` | *(auto-detect)* | Force an adapter (`zte_f660`, `zte_f680`, `zte_funbox`, …) |
| `--username NAME` | `admin` | Router username |
| `--password PASS` | *(prompt / env)* | Router password — prefer the env var or the prompt |
| `--router-insecure` | `false` | Skip TLS verification for the router's self-signed cert (router only) |
| `--json` | `false` | JSON output (where applicable) |
| `--db PATH` | `kergui.db` | Local SQLite database path |
| `--timeout DURATION` | `15s` | Per-request timeout |
| `--verbose` | `false` | Verbose logging on stderr |

## Environment variables

| Variable | Purpose |
|---|---|
| `KERGUI_MASTER_KEY` | Passphrase that encrypts stored router credentials (required to store or read them) |
| `KERGUI_ROUTER_PASSWORD` | Router password, so it never appears in argv/shell history |

## Commands

### `discover`
Non-destructive fingerprint (no login). Prints vendor/model/server/title and the
suggested adapter with a confidence score.
```sh
kergui discover --router http://192.168.1.1
```

### `login [--test]`
Authenticates. Without `--test` it stores the credentials **encrypted** and records
the router. `--test` only checks the connection (stores nothing). If no password
is given via `--password`/`KERGUI_ROUTER_PASSWORD`, it prompts (hidden).
```sh
export KERGUI_MASTER_KEY='…'
kergui login --router http://192.168.1.1 --username admin          # store
kergui login --router http://192.168.1.1 --username admin --test   # just test
```

### `devices [--json]`
Logs in, lists connected devices, reconciles them into the local inventory, and
flags never-seen-before MACs as new.
```sh
kergui devices --router http://192.168.1.1
kergui devices --router http://192.168.1.1 --json
```

### `inspect`
Diagnostic report: router identity, authentication, and how each operation maps to
the adapter's endpoints (each tagged `verified`/`UNVERIFIED`), plus warnings.

### `serve [--addr HOST:PORT]`
Starts the web dashboard **and** REST API (default `127.0.0.1:8080`). See the
[API reference](api.md).
```sh
kergui serve --router http://192.168.1.1 --addr 127.0.0.1:8080
```

### `block --mac MAC --yes` / `unblock --mac MAC --yes`
Adds/removes a MAC on the router's block list. A router write is refused without
`--yes`. Replies clearly if the model's write path is not yet wired/verified.
```sh
kergui block   --router http://192.168.1.1 --mac AA:BB:CC:DD:EE:FF --yes
kergui unblock --router http://192.168.1.1 --mac AA:BB:CC:DD:EE:FF --yes
```

### `rename --mac MAC --name NAME`
Sets a device's **local** custom name (never touches the router). The device must
have been seen at least once (`devices`).
```sh
kergui rename --router http://192.168.1.1 --mac AA:BB:CC:DD:EE:FF --name "Téléphone Maman"
```

### `version`, `help`
Print the version / usage.

## Exit codes
- `0` success
- `1` runtime error (unreachable, auth failed, not implemented, …) — a friendly
  message is printed to stderr
- `2` usage error (bad flags, missing required flag)

## Error messages (mapped from the domain error taxonomy)
Router unreachable · authentication failed · session expired · unsupported model
(`--adapter`) · device not found (run `devices` first) · not implemented (write
path not yet verified) · unexpected router response.
