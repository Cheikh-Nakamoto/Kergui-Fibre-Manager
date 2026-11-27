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
| `wifi_blocked` | read | yes | live write session 2026-11-27 (below), read-back of `WiFiUnblock` |
| `wifi_unblock` | write | yes | live write session 2026-11-27 (below, W8) |
| `wifi_block` | write | **no** | its rule and policy forms refused a phone live on 2026-11-27, but `WiFiBlock` itself did not run: the table was full (below); refused through the relay |
| `wifi_lock` | read | **no** | the table size and the `Allow` mode measured live 2026-11-27 (below, W1–W4); `WiFiLockBox` itself not run live (#105) |
| `wifi_lock_write` | write | **no** | the box's `Allow` behaviour measured live 2026-11-27 (below, W2–W4); `ApplyWiFiLock` / `SetWiFiMode` only run against the mock (#105); refused through the relay |
| `admin_password_first` | write | **no** | page captured 2026-11-25, the change itself not run live (below) |

Review of 2026-11-25 (#96): no flag was flipped. The Wi-Fi access control
(`wifi.go`, « Couper l'accès ») is only exercised against the mock
(`integration_test.go`, `mobilecore`); there is no capture of
`wlan_macfilterrule_lua.lua` / `wlan_macfilteraclpolicy_lua.lua` from a real
box in `internal/fixtures/zte_f6600p/`, and no dated live validation in the git
history. That the app reads `wifi_blocked` at home every day does not prove
the parsing: the app hides the section when the read fails. To flip one: run
it on the box, add the sanitised capture to the fixtures, date it in `Notes`,
and update `TestMeta_SSIDSetVerified` (`meta_test.go`). Since 2026-11-27 it
pins `wifi_blocked` and `wifi_unblock` as verified and `wifi_block` as not.

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

## Wi-Fi access control: live write session of 2026-11-27 (RFC-007 W1–W9, #101)

Owner present, account `user`, fw `ZTEGF660006SN`, between 00:12 and 00:30.
Every write went to AP3 (enabled for the session as `Kergui-Test`) or to fake
locally administered MACs `02:00:00:00:xx:yy`. A spare Android 15 phone
(adb) was the test station. The PC stayed on the household's Wi-Fi, which no
step touched. A snapshot was taken before; afterwards the modes, the 63 Wi-Fi
rules and the 11 firewall rules were identical to it, and AP3 was disabled
and named `SSID3` again. Only AP3's password differs: the box returns it
encrypted, so the old one could not be written back.

| # | Result |
|---|---|
| W1 | **The Wi-Fi rule table holds 64 rules, shared by all APs.** With 63 rules, one more was accepted, the next refused. |
| W1, W7 | **Full answer** (HTTP 200, `IF_ERRORID` -12, `IF_ERRORTYPE` 1), the same for both lists: « Le nombre d'entrées a atteint la limite maximale. Supprimez certaines entrées et saisissez-les à nouveau. » |
| W7 | **The firewall MAC filter holds 32 rules.** 11 + 21 fake were accepted, the 22nd refused. |
| W2 | **Switching to `Allow` with an empty list has no effect**: the phone joined as if the AP had no filter, and an AP restart was needed before a rule added later took effect. |
| W2 bis | **Emptying the list while the AP is already in `Allow` refuses everyone** (the phone was refused at association). |
| W3 | A connected station that is not in the list is **dropped about 40 s** after the switch to `Allow` (list not empty), and refused when it tries again. Removing a connected station's rule in `Allow` drops it within about 25 s. |
| W4 | **Adding a MAC in `Allow` takes effect at once**, no restart: the phone joined. |
| Modes | `Allow` ↔ `Disabled` ↔ `Ban` flips take effect at once, no restart, as long as the `Allow` list is not empty. |
| W5 | The refused phone logs `CTRL-EVENT-ASSOC-REJECT status_code=1`: the AP refuses at association. No refusal shows in the box's pages read by kergui; the box's own logs were not searched. |
| W6 | No visitors' page in the menu data the `user` account sees, and no `GUESTWIFI` object (as on 2026-11-26). |
| W8 | **`Ban` refuses at association**: the phone's MAC in a rule on AP3 in `Ban` got `ASSOC-REJECT`, and it went back to the household's network. `WiFiUnblock` ran on a fake MAC on AP3 in `Ban`, removed its rule and passed its read-back. `WiFiBlock` did not run: it needs one free slot per enabled AP (4) and the table had one. |
| W9 | Box back to the snapshot (above). |

For RFC-007:
- Places = `floor(64 / closed APs)` minus the rules left on open APs: 32 with
  2 closed APs, 21 with 3.
- Never switch an AP to `Allow` while its list is empty, and never remove its
  last rule while it is in `Allow`.
- Closing drops strangers within about 40 s, not at once.

## The closed Wi-Fi in the adapter (RFC-007 D4, D7, #105)

`port.WiFiAllowList`, in `wifi.go`, with `wifi_lock` / `wifi_lock_write`
unverified until `ApplyWiFiLock` runs once on a live box.

- **`WiFiLockBox`** reads the enabled APs (name, band) from the status page,
  each AP's mode, and the whole rule table by AP, with the limit of 64.
- **`ApplyWiFiLock`** converges the target APs to one mode and one list,
  from any state, so a run another session cut short (#94) is finished or
  undone by the next writer: snapshot; « liste pleine » before any write
  (`WiFiLockBox.CheckFits`); an AP that changes mode goes to `Disabled` first
  (never `Ban` straight to `Allow`: the trap); the rules (re-read before each
  delete; on an AP kept in `Allow` the missing ones are added before the
  extra ones go, never its last rule); read-back, exact, this phone
  included; the mode (never `Allow` on an empty list); read-back. Any failure
  goes back to the snapshot the same way. A box that answers « plein »
  midway is reported in places, as `domain.AccessListFullError`.
- **`SetWiFiMode`** is one policy POST (open light, the add window, the
  re-close), the rules kept. It refuses `Allow` on an AP with no rule and a
  flip straight between `Ban` and `Allow`.
- **`WiFiUnblock`** removes a MAC's rules on the APs in `Ban` only.
- **The mock** (`mockrouter/f6600p_wifilock.go`) records each write that
  reads a list the wrong way (`WiFiHazards`): the trap, an empty `Allow`
  (which filters nothing, as W2), the last rule removed in `Allow`. It drops
  unlisted stations at once on the switch to `Allow` (the box takes about
  40 s, W3), and can refuse the n-th write or lose a device's rules, for
  the rollback tests (`wifilock_test.go`).

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
  the devices the list holds). The F6600P's own text, measured on 2026-11-27,
  heads that list. The mock answers the same text (`mockrouter.FullAnswer`)
  beyond the limit `SetRuleLimits` gives it; `F6600PWiFiRuleLimit` (64) and
  `F6600PFirewallRuleLimit` (32) are the real sizes.
- **Purge** (`usecase.PurgeStaleRules`): once a day after a device list, and
  right before a block the full list refused. It removes the rules kergui
  wrote for devices in the inventory and away 30 days: the firewall rule, and
  the Wi-Fi refusals on every AP in `Ban` (#103, `WiFiUnblock`, which reads the table
  again before each delete since the box renumbers or leaves holes). It adds
  the missing firewall rule to the Wi-Fi refusals of devices seen in that
  time. Each removal is the journal line « Règle retirée : appareil absent
  depuis 30 jours ». The Wi-Fi part runs only while `wifi_unblock` is
  `Verified` in `Meta()` (on a model where it is not, the stale refusals are
  only listed), and not right before a firewall block (it frees no room for
  it). It never removes a MAC the inventory does not know, a device on the
  box, or any rule of a MAC the household has a rule for, in either list:
  `WiFiUnblock` deletes every rule of the MAC on every AP in `Ban`, the
  household's too (never on an AP in `Allow` or `Disabled`, where the rule
  admits the device or is the allow list kept for the next close, #105).
  The « Bloqués, absents du réseau » section frees such a MAC by hand,
  firewall rule and Wi-Fi refusals at once.

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
