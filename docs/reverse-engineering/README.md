# Reverse-engineering guide (legitimate capture on your own router)

This project is built *observe-first*: adapter endpoints are hypotheses until you
confirm them against a real device. This guide explains how to capture your own
router's requests **legitimately** and feed them back as fixtures.

> **Ground rules.** Only do this on a router you own or administer. Use the normal
> web interface and your own credentials. **Never** bypass authentication or
> exploit a vulnerability — that is out of scope for this project and unnecessary.

## 1. Fingerprint first (no login)

```sh
kergui discover --router http://192.168.1.1
```

Note the vendor/model/adapter it reports.

## 2. Capture the real pages

Using your browser's **DevTools → Network** tab while you use the router UI:

1. Open `http://192.168.1.1`, log in normally.
2. Visit *Réseau → WLAN → Appareils associés* (associated devices) and the LAN /
   DHCP client list.
3. Visit *Réseau → WLAN → Liste de contrôle d'accès* (access control).
4. For each, right-click the request → **Save** (or **Copy → Save as HAR**).
5. Note the exact request **URL (path + query)**, **method**, **form fields**, and
   whether the password was sent in clear or hashed (look at the login POST).

## 3. Sanitise (required before committing anything)

Remove or replace:
- cookies and session ids (`SID`, etc.),
- the password and any login token values,
- real MAC and IP addresses (use placeholder values like `ac:bb:cc:00:00:11`).

Never commit real credentials or unsanitised captures — the repo `.gitignore`
already excludes `*.har` and `captures/`.

## 4. Turn captures into fixtures + a profile

1. Drop the sanitised HTML into `internal/fixtures/<model>/`.
2. Adjust the model's `profile.go`
   (`internal/adapter/router/<model>/`): set the real `LoginPage`,
   `LoginSubmit`, `StatusPage`, `DevicesPage`, `ACLPage`, the login `Fields`, and
   the correct `PasswordMode` (`plain` / `sha256` / `sha256_token`).
3. If the data shape differs from the ZTE JS-array pattern, adjust the parser in
   `internal/adapter/router/zte/parse.go` (or add a model-specific parser).
4. Once confirmed on hardware, set `Verified: true` and `ReadReady: true`.

## 5. Validate

```sh
make test                      # parsers + mock contract
kergui inspect --router http://192.168.1.1   # against the real device
```

When `inspect` shows the right model, firmware, device count and access-rule
count, the adapter is validated for your firmware. Please contribute the sanitised
fixtures and the corrected profile back so others benefit.
