# ZTE ZXHN F6600P (Orange Livebox Fibre)

- Adapter id: `zte_f6600p` (`internal/adapter/router/ztef6600p`)
- Status: **verified** read path, MAC filter and SSID writes (firmware
  `ZTEGF660006SN`); the per-endpoint status is in `Meta()` (`gateway.go`) and
  `kergui inspect`.
- Mock: `internal/mockrouter/f6600p.go` (`go run ./cmd/mockrouter -model f6600p`).

The login, menuView/menuData and Check-header protocol is summarised at the top
of `gateway.go`. This page records which operations are validated on a live box
and the first sign-in on a new box.

## Validated operations (`Verified` in `Meta()`)

`Verified: true` means the operation was run on a live F6600P and its effect
checked, with the date recorded in its `Notes`. Through the household's relay
(RFC-005 decision 44, #96) an operation that is not verified is refused with
501 `unverified` **only for a write**; a read of it runs, since it cannot change
the box. Locally nothing is refused on this ground.

| Operation | Kind | Verified | Evidence |
|---|---|---|---|
| `login`, `router_info`, `devices`, `access_rules` | read | yes | read path validated live (fw `ZTEGF660006SN`); fixtures in `internal/fixtures/zte_f6600p/` |
| `block`, `unblock` | write | yes | validated live 2026-10-10, fw `ZTEGF660006SN` |
| `wifi_ssids` | read | yes | read live 2026-10-12 |
| `wifi_ssid_set` | write | yes | on/off, rename and password validated live by the owner 2026-11 (#34) |
| `wifi_blocked` | read | **no** | see below; runs through the relay anyway (#96) |
| `wifi_block`, `wifi_unblock` | write | **no** | see below; refused through the relay |
| `admin_password_first` | write | **no** | page captured 2026-11-25, the change itself not run live (below) |

Review of 2026-11-25 (#96): no flag was flipped. The Wi-Fi access control
(`wifi.go`, « Couper l'accès ») is only exercised against the mock
(`integration_test.go`, `mobilecore`); there is no capture of
`wlan_macfilterrule_lua.lua` / `wlan_macfilteraclpolicy_lua.lua` from a real
box in `internal/fixtures/zte_f6600p/`, and no dated live validation in the git
history. That the app reads `wifi_blocked` at home every day does not prove
the parsing: the app hides the section when the read fails. To flip one: run
it on the box, add the sanitised capture to the fixtures, date it in `Notes`,
and update `TestMeta_SSIDSetVerified` (`meta_test.go`), which pins these three
as unverified.

## Wi-Fi access control: live read of 2026-11-26 (RFC-007 step 0)

Read only (GETs, account `user`, fw `ZTEGF660006SN`), nothing written, no flag
flipped. Sanitised captures: `internal/fixtures/zte_f6600p/wlan_macfilteraclpolicy.xml`,
`wlan_macfilterrule.xml`, `wlan_wlanstatus.xml` (every MAC and BSSID replaced,
broadcast names replaced).

- 8 APs: AP1–AP4 on `DEV.WIFI.RD1` (2.4 GHz), AP5–AP8 on `DEV.WIFI.RD2`
  (5 GHz). Enabled: AP1, AP5 and AP7 (5 GHz only, another name).
- Mode per AP (`ACLPolicy`): AP1, AP5, AP7 `Ban`, the rest `Disabled`. The page
  labels them « No Filter / Liste noire / Liste blanche ».
- One shared rule table (`OBJ_ACLCFG_ID`, `DEV.ACLn`), each rule bound to one AP
  by `Interface`: **63 rules = 21 MACs × 3 APs**, all named by kergui
  (`<mac> APn`). 19 of the 21 MACs are locally administered (private).
- `_InstID`s have gaps (ACL40–42, ACL52–54).
- No rule limit in the page's HTML/JS. `MaxUserNum = 32` on each AP is the
  associated-station limit, not a rule limit.
- The SSID page has no `GUESTWIFI` object, and lists AP1, AP3, AP4, AP5, AP7,
  AP8 (no AP2, AP6).
- Firewall MAC filter: on, `Discard`, 11 rules.

Still to measure with a write session (owner present, PC on a cable, a spare
test SSID on AP3): the rule limits and the box's answer when full, `Allow` with
an empty list, what happens to devices already connected when the mode switches,
whether a refused device leaves a trace, the visitors' network, and
`wifi_block` / `wifi_unblock` (list W1–W9 in RFC-007).

## Stale rules and « liste pleine » (RFC-007 D6, #102)

- **Which rules kergui wrote.** A firewall rule kergui writes is named after
  the MAC itself (`changeACLOnce`); a Wi-Fi rule `<mac> APn` (`wlanRuleName`).
  The adapter reports this as `AccessRule.ByKergui`; any other name was typed
  by the household in the box's page, and kergui never removes it.
  `WiFiRules` (`port.WiFiRuleReader`) lists the Wi-Fi refusals one rule at a
  time, read only.
- **Full.** When the box refuses an add with one of the answers in
  `fullAnswers` (`gateway.go`), `Block` and `WiFiBlock` return a
  `domain.AccessListFullError` (kind `firewall` or `wifi`, `Limit = Used` =
  the devices the list holds). The F6600P's own text is not known yet: W1/W7
  of the write session (#101) adds it to that list. The mock answers
  `mockrouter.FullAnswer` beyond the limit `SetRuleLimits` gives it.
- **Purge** (`usecase.PurgeStaleRules`): once a day after a device list, and
  right before a block the full list refused. It removes the firewall rules
  kergui wrote for devices in the inventory and away 30 days, adds the missing
  firewall rule to the Wi-Fi refusals of devices seen in that time, and only
  lists the Wi-Fi refusals of devices away 30 days: removing them is a
  `wifi_unblock`, not verified yet (#103). It never writes to the Wi-Fi.

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
