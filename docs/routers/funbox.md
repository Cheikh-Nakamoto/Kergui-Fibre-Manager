# ZTE Funbox (Orange Sénégal)

- Adapter id: `zte_funbox`
- Status: **skeleton** — discovery works; data-page parsers await a real capture
  (`ReadReady=false`, so read operations return `ErrNotImplemented`).

## Notes
- Orange's ZTE "Funbox". Orange SN documents the same Black List / White List MAC
  filtering as the Fiberbox.
- To complete this adapter: capture the login + data pages (see
  [`../reverse-engineering/README.md`](../reverse-engineering/README.md)), fill the
  endpoints in `internal/adapter/router/funbox/funbox.go`, add fixtures, set
  `ReadReady: true` and (once confirmed) `Verified: true`.
