# Fixtures — SYNTHETIC router responses

These files are **not** captured from a real router. They model the documented
ZTE ZXHN structure well enough to exercise the parsers and the end-to-end flow,
and they contain **no real credentials, cookies, or MAC addresses**.

They are embedded by `fixtures.go` and consumed by:
- the ZTE parser unit tests (`internal/adapter/router/zte`), and
- the bundled mock router (`internal/mockrouter`, served by `cmd/mockrouter`).

## Replacing them with real captures (the validation loop)

1. On your own router, capture the relevant pages with browser DevTools / a HAR
   export (see [`../../docs/reverse-engineering/README.md`](../../docs/reverse-engineering/README.md)).
2. **Sanitise**: remove cookies, passwords, tokens, and real MAC/IP addresses, or
   replace them with placeholder values like the ones here.
3. Drop the sanitised files in `zte_f660/` (or a new `<model>/` directory).
4. Adjust the adapter `profile.go` to match the real endpoints, and re-run
   `make test`. The parsers — not these fixtures — are what the tests verify.

Never commit real credentials or unsanitised captures (see the repo `.gitignore`).
