# ZTE ZXHN F680

- Adapter id: `zte_f680`
- Status: **skeleton** — discovery works; data-page parsers await a real capture
  (`ReadReady=false`, so read operations return `ErrNotImplemented`).

## Notes
- Same ZXHN web-UI family as the F660.
- The public `ZTE-F680-API` client documents a 3-step login with **SHA256
  password + one-time token** and cookie sessions; the profile sets
  `PasswordMode = sha256_token`.
- To complete this adapter: capture the login + data pages (see
  [`../reverse-engineering/README.md`](../reverse-engineering/README.md)), fill the
  endpoints in `internal/adapter/router/ztef680/ztef680.go`, add fixtures, set
  `ReadReady: true` and (once confirmed) `Verified: true`.
