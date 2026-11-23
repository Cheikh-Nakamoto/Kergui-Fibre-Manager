# Roadmap

Living plan for Kergui Fibre Manager, as of **2026-11-23**. Every "done" line
below points at the commit(s) that built it; the issue tracker
(<https://github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/issues>) holds the
detail. Router endpoints stay **UNVERIFIED** until confirmed on real hardware
(the honesty contract in [docs/README.md](README.md)); the ZTE F6600P is the
only box confirmed live so far.

## In production

| What | Where | Notes |
|---|---|---|
| Hub (`cmd/kergui-hub`) | `https://hub.kergui.click`, on Dokploy | Built by Dokploy from `deploy/hub/Dockerfile` when a `hub-v*` tag is pushed; Postgres managed by Dokploy. Runbook: [hub-production.md](hub-production.md). `/health` still answers `"version":"dev"` until the `VERSION` build argument is set in Dokploy (human step, #60). |
| Website (`landing/`) | `https://kergui.click`, on Dokploy | Landing page (#61), customer area `/compte` (#63), back-office `/admin` (#62), APK download `/telecharger`. |
| Passes | on sale on `kergui.click` | NabooPay prepaid passes, Basic 650 F / Max 2000 F a month (3 and 12 months too), 7-day Max trial. |
| Android app | **0.1.3** (tag `mobile-v0.1.3`) | Signed APK from GitHub Releases, downloaded through `https://kergui.click/telecharger`. Not on Google Play yet (#37). |

## Done

### Local tool: CLI, REST API, web dashboard (milestones 1–3)
- Clean Architecture core with the Dependency Rule enforced by
  `tests/architecture_test.go`; discovery, read path, SQLite inventory,
  encrypted credential vault (`6270b1f`…`93451a6`).
- `kergui serve`: JSON API and embedded dashboard (`2209cad`, `8633ad5`);
  block / unblock / rename with read-after-write checks (`1ed2cdc`, `e06707f`).
- `kergui export` moves a household's data from the PC to the phone
  (`1cff5c1`).

### Routers
- **ZTE F6600P** adapter (`d9f3bba`): login, devices, MAC filter block /
  unblock, SSIDs on/off, rename and Wi-Fi password **validated live**; box
  serial as identity (`68d5d27`). "Couper l'accès" (Wi-Fi access control) is
  implemented but not yet confirmed on a live box.
- MAC filter turned on at setup, blacklist mode only (`2b3a716`).
- F660, F680, Funbox: adapters or skeletons, all **UNVERIFIED**.

### Mobile app v1 (RFC-004, `mobile/` + `mobilecore/`)
- Expo app with the Go core compiled in by gomobile; builds and signing on
  GitHub Actions (`5f9fdf4`, `b24f223`). Website APK cut from 141 MB to 42 MB
  (`7707cb4`).
- Devices in three groups (cut, blocked by the filter, with access) with a
  « Connectés » / « Bloqués » filter (`67216a1`, `0f1be91`); block / unblock,
  rename, « Couper l'accès » (`ef0f75c`).
- Consumption: current month free, 6-month history per device for Max
  (`3fe78ee`, `d3f0401`). Wi-Fi networks: on/off free, rename and password for
  Max (`3399163`).
- Device approvals (#58): an alert on every arrival of a device nobody
  approved; devices already there are never new and a device left alone
  15 min is approved (`8ab7293`, `75b6847`).
- Notifications screen behind a bell, new-device alerts only there
  (`86c981e`, `320a3ab`); continuous watch on Android (`6127445`).
- Settings lists the box's features by level, active or locked (`5f2908d`).
- « Faire une sauvegarde » / « Importer une sauvegarde » for changing phones
  (`c19b906`).
- No payment surface in the app (#38, `7844bcb`): one build, an Account
  screen, passes bought on the website.

### Hub v2 (RFC-005, `internal/hub/`), H0–H5
- **H0–H2**: hub on the agile-factory kernel (`cd3cd38`); relays with pairing,
  lease and command queue (`9020a2c`, `30e0a21`); owner-rooted keys and signed
  commands (`43cd177`); inventory diff and new-device alerts (`dffe51c`,
  `9193080`); "1 = block" reply on WhatsApp (`8c4b077`).
- **Identity and household**: the WhatsApp-verified number is the hub account
  and creates the household (#41, `bf79076`); members by phone number with
  roles (#42, `32ad8c9`).
- **H3 — remote control**: owner keys and QR relay pairing with the 6-digit
  code (#43, `7ae4761`); box password sealed for every relay (#44, `e198b28`);
  an Android phone can be the household relay (#45, `d5060ff`); devices and
  block / unblock through the hub (#46, `df8fd93`); delegations view / control
  with expiry and revocation (#47, `5c23bbe`); recovery of a lost owner phone
  (#48, `02999d1`); push alerts and WhatsApp opt-in from the app (#49,
  `3d9c547`). Relays have names (#57, `edad615`) and bound their box work to
  their lease (#55, `2b0c808`, `8001d69`).
- **H4**: consumption, SSIDs and Internet cut / restore through the relay
  (#50, `8efdfff`).
- **H5**: production on Dokploy with backups and runbook (#60, `1557f42`,
  superseding the GHCR / VPS / cloudflared plan of #52).
- **Auto-reject**: on the phone for Max (#35, `cbbf32e`); on the relay agent
  with enforcer election and a Max token check (`8f83850`). The app does not
  yet publish the signed policy to the hub (#36).
- **WhatsApp** through whatsmeow inside the hub; Evolution Go removed (#59,
  `ce80c21`). The Meta Cloud API adapter (#51) was dropped.

### Licensing (RFC-006)
- Tiers Free / Basic / Max, entitlement tokens checked in the core (#24,
  `06b4a84`, `d70355e`); WhatsApp OTP sign-in (#25, `61beb09`).
- NabooPay prepaid passes with stacking, pro-rata upgrade and J-3 reminder
  (#27, #29, `42e76cf`, `a40f917`); 7-day Max trial asked on the web, started
  at box binding (#26, `f9f67b2`); household phones and box transfer (#30,
  `9035533`); signing key rotation (#39, `9dccf5b`).
- Google Play Billing verification and RTDN on the hub (`4e0895a`); the Play
  flavor itself is not shipped (#28).

### Website and back-office (`landing/`)
- Next.js landing page for SEO (#61, `2adfa12`); the download button streams
  the newest APK from kergui.click (`748a168`, `96d96c2`).
- Back-office `/admin` for the Kergui team with the hub admin API (#62,
  `d4b29f7`): hub health, households, passes and payments, WhatsApp session.
- Customer area `/compte` (#63, `29a6ff1`): WhatsApp sign-in, passes, buy,
  renew, trial, history.

## Next

### Product
- #65 In-app updates for the website APK through the hub (CI declares,
  back-office publishes, minimum version).
- #66 Livelier colours and « Plus de fonctionnalités » out of Réglages.
- #67 Back-office: action log, customer support, payments and revenue,
  messages.
- #68 Complaints from the app, the site and WhatsApp; admin WhatsApp summary
  and commands.
- #69 Devices tab leaves out this phone and the owner's phones.
- #70 Automatic backup of a household's data on the hub, restored on a new
  phone.
- #36 The app publishes the signed auto-reject policy and asks the hub who
  enforces.

### Production
- #60 Human steps left in Dokploy: `VERSION` build argument (so `/health`
  reports a real version), `KERGUI_HUB_LICENSING_RETURN_URL`,
  `KERGUI_HUB_CLIENT_IP_HEADER`, tag trigger, one replica stop-first, S3
  backups and a restore test.

### Google Play
- #37 Play listing and closed test.
- #53 Play declarations for the relay (foreground service, data collected).
- #28 Play Billing subscriptions (play flavor).

### Later
- Validate the other boxes (F660, F680, Funbox) and the F6600P's Wi-Fi access
  control on real hardware ([reverse-engineering guide](reverse-engineering/README.md)).
- Parental control / web filtering ([RFC-002](rfcs/RFC-002-parental-control.md)),
  blocked on a capture of the F6600P parental-control pages.
- iPhone app and iPhone relay (out of scope for V1, RFC-006).
- More vendors (Huawei, TP-Link, Nokia).

## Principles (unchanged across milestones)
- Observe first; never invent endpoints.
- The core never learns a router's HTTP/HTML details.
- Credentials encrypted at rest, never returned to the frontend, never logged.
- Read-only is safe by default; writes require explicit confirmation.
