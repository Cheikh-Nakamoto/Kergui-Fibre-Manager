# Fixtures — router responses

Two kinds of files live here, and the difference matters when you read a test:

| Directory | Origin |
|---|---|
| `zte_f660/` | **Synthetic.** Modelled on the documented ZXHN structure, never captured. |
| `zte_f6600p/` | **Real captures, sanitised**, from the reference box (firmware ZTEGF660006SN). |

Neither kind contains credentials, cookies, session tokens or real MAC/IP
addresses: everything identifying has been replaced with placeholders.

They are embedded by `fixtures.go` and consumed by:

- the ZTE parser unit tests (`internal/adapter/router/zte`,
  `internal/adapter/router/ztef6600p`);
- the bundled mock routers (`internal/mockrouter`, served by `cmd/mockrouter`):
  `New` imitates the F660 on the synthetic files, `NewF6600P` replays the
  captured F6600P protocol;
- the mobile core's demo mode (`mobilecore.Core.StartDemo`).

A synthetic fixture proves the parser is internally consistent with the
documented protocol. Only a captured one proves it matches the hardware, which
is why the F6600P — the reference box of
[RFC-005](../../docs/rfcs/RFC-005-semi-distributed-hub.md) — is captured.

## Replacing them with real captures (the validation loop)

1. On your own router, capture the relevant pages with browser DevTools / a HAR
   export (see [`../../docs/reverse-engineering/README.md`](../../docs/reverse-engineering/README.md)).
2. **Sanitise**: remove cookies, passwords, tokens, and real MAC/IP addresses, or
   replace them with placeholder values like the ones here.
3. Drop the sanitised files in `zte_f660/`, `zte_f6600p/` or a new `<model>/`
   directory, and embed them in `fixtures.go`.
4. Adjust the adapter to match the real endpoints and re-run `make test`. The
   parsers and the mocks — not these files — are what the tests verify.

Never commit real credentials or unsanitised captures (see the repo `.gitignore`).
