# ZTE ZXHN F6600P (Orange Livebox Fibre)

- Adapter id: `zte_f6600p` (`internal/adapter/router/ztef6600p`)
- Status: **verified** read path, MAC filter and SSID writes (firmware
  `ZTEGF660006SN`); the per-endpoint status is in `Meta()` (`gateway.go`) and
  `kergui inspect`.
- Mock: `internal/mockrouter/f6600p.go` (`go run ./cmd/mockrouter -model f6600p`).

The login, menuView/menuData and Check-header protocol is summarised at the top
of `gateway.go`. This page records the first sign-in on a new box.

## First sign-in: the box demands a new password (#92)

Captured on 2026-11-25 on a new F6600P signed into with the credentials on its
label. The default username of that box is **`user`** (`admin` on others). The
sanitised page is `internal/fixtures/zte_f6600p/forced_changepwd.html`. The change
itself is implemented but **not yet validated on a live box**
(`admin_password_first`, `Verified: false`).

### Detection

The login (`login_entry` → `login_token` → POST `login_entry`) is unchanged and
answers `login_need_refresh: true` with the `SID_HTTPS_` cookie. But `GET /` then
answers, instead of the interface, a page with form `#fLogin` (`Password` = new,
`NewPassword` = confirmation, `Username` read-only) whose `doChgpwd` posts to
`_tag=login_changepwd`. kergui recognises it by `_tag=login_changepwd` plus
`id="fLogin"` in the main page, right after the preempt check, and answers
`domain.ErrPasswordChangeRequired` with the session left open. Nothing else works
until the password is changed (the mock answers `SessionTimeout` everywhere).

### The change

`POST /?_type=loginData&_tag=login_changepwd`, form-urlencoded, `X-Requested-With:
XMLHttpRequest`, no `Check` header, fields in `doChgpwd`'s order:

| Field | Value |
|---|---|
| `action` | `changepwd`. The page's « Annuler » sends `changepwd_cancel`: **kergui never sends it** (owner's decision Q13). |
| `Password` | `encodeParaValue(new, key, iv)` |
| `Username` | the current username |
| `NewPassword` | `encodeParaValue(new, key, iv)` (the confirmation) |
| `_sessionTOKEN` | `doChgpwd`'s own `var sessionTmpToken = "\xNN.."` (hex-escaped JS string). The page-wide `_sessionTmpToken` and the hidden `_sessionTOKEN` input are fallbacks. |
| `encode` | `encodeKey(key, iv)` = base64(RSA PKCS#1 v1.5 (`key + "+" + iv`)) with the public key in the page's `asyEncode` (JSEncrypt) |

- `key`, `iv`: 16 random decimal digits each (`randomNum(16)`; kergui uses
  `crypto/rand`).
- `encodeParaValue` (CryptoJS): AES-256-CBC, key = SHA-256 of `key` as ASCII,
  IV = first 16 bytes of SHA-256 of `iv`, **zero padding** (not PKCS#7), output =
  base64 of the ciphertext alone (a WordArray key: no `Salted__` header). It is
  the same function the SSID write uses (`ssid.go`). Known vector, computed with
  OpenSSL and Node: `encodeParaValue("Kergui2026", "1234567890123456",
  "6543210987654321")` = `9fvCnBTbjZYX7fVxrMy7fQ==`.
- Answer (JSON): `login_need_refresh: true` = changed (the page reloads);
  otherwise `loginErrMsg` = refused, mapped to `domain.ErrPasswordRejected` with
  the box's message.
- kergui then signs in again with the new password (same username) and checks the
  normal interface opens.

### The box's rule

`checkPassStrong`: at least 8 characters, with a lower-case letter, an upper-case
letter and a digit; the form also requires ASCII. (The displayed hint speaks of 3
character types out of 4; the code checks the three above.) `domain.ValidateBoxPassword`
applies the same rule before anything is sent (`ErrWeakPassword`); the box remains
the judge.

### Where it is used

- Core API: `POST /api/login` → `409` `code: password_change_required` (nothing
  stored); `POST /api/box/first-password` `{username, password, newPassword}` sets,
  re-logs in and stores the new password (see [../api.md](../api.md)).
- Mobile core: the same routes through `Core.Request`; `Core.BoxPassword()` reads
  the stored login back for Réglages; the next `SealCredentials` moves the
  credentials version so the relays get the new password.
- PC CLI: `kergui login` only says, in French, to set the password from the app.
- Mock: `F6600P.RequirePasswordChange()` / `go run ./cmd/mockrouter -model f6600p
  -new-box`.
