# Hub in production: runbook (Dokploy)

The hub (RFC-005) runs on a server managed by [Dokploy](https://dokploy.com).
Dokploy builds `deploy/hub/Dockerfile` from this repository and runs it next
to a Postgres it manages; its Traefik serves the domain and the TLS
certificate. The website (`landing/`, back-office included) is another
Dokploy application on the same server, at `https://kergui.click`
(`landing/README.md`).

| Piece | Where | Role |
|---|---|---|
| hub | Dokploy **Application**, build type Dockerfile, `deploy/hub/Dockerfile` | the hub, container port `8090`, public at `https://hub.kergui.click` |
| Postgres | Dokploy **Database** (PostgreSQL 16) | the hub's data, the WhatsApp session included (`whatsmeow_*` tables) |
| TLS, domain | Dokploy's Traefik, Let's Encrypt | `hub.kergui.click` → hub:8090 |
| backups | the database's **Backups** tab (S3 destination), plus `deploy/hub/scripts/` | see [Backups](#backups) |

- **Image**: built by Dokploy on the server, from the Dockerfile, at the
  pushed tag. Nothing is pulled from a registry. The image has no shell and
  no secret: everything comes from the application's environment.
- **Migrations** run when the hub starts, before it listens. A version that
  cannot migrate exits; Swarm restarts it and `/ready` never turns green.
- **Health**: `GET /health` (liveness: the process answers, with its
  version) and `GET /ready` (readiness: the database answers). The image's
  `HEALTHCHECK` runs `/kergui-hub -healthcheck` against `/ready` every 15 s,
  so Dokploy (Docker Swarm) replaces a hub that stays unhealthy and waits for
  the new one to be healthy on a redeploy. External monitoring: an HTTP check
  (UptimeRobot, Better Stack...) on `https://hub.kergui.click/ready` every
  minute, alerting on anything but 200.
- **One replica, always.** The WhatsApp session lives in the hub process:
  two hubs take it from each other ("another process took over the WhatsApp
  session"), and the outbox dispatcher holds a database-wide lock. Keep
  Replicas = 1 and Swarm's default update order, `stop-first` (never
  `start-first`, which runs the old and new hub side by side).
- **Version**: `/health` and `kergui-hub -version` report the `VERSION`
  build argument of the Dockerfile. Dokploy does not pass the tag's name by
  itself, so unless `VERSION` is set in the application's build arguments the
  hub reports `"version":"dev"` (the case on 2026-11-23). See
  [Deploying a version](#deploying-a-version).

## Human steps (once)

Nobody but the owner can do these. Steps 1 to 4 are already done for
`hub.kergui.click`; they are here to rebuild it.

1. **Postgres**: Dokploy → project → Create Service → Database → PostgreSQL
   16. Database name `hub`, user `hub`, a generated password. Do not expose
   an external port. Note its **Internal Connection URL**
   (`postgresql://hub:<password>@<db app name>:5432/hub`).
2. **Application**: Create Service → Application. Provider: GitHub, this
   repository. Build type **Dockerfile**, Dockerfile path
   `deploy/hub/Dockerfile`, build context the repository root (`.`).
   Trigger: **on tag** (see [Deploying a version](#deploying-a-version)).
   Advanced: Replicas `1`.
3. **Domain**: Domains → `hub.kergui.click`, path `/`, container port
   `8090`, HTTPS on, certificate Let's Encrypt. DNS: an A record for
   `hub.kergui.click` to the server. No tunnel and no `cloudflared`: Traefik
   is the only way in. The server's firewall lets in 80, 443 and SSH only.
4. **Environment**: the application's Environment tab, from the
   [list below](#environment-variables). Generate the secrets on your own
   machine:

   | Variable | How to make it |
   |---|---|
   | `KERGUI_HUB_JWT_SECRET` | `openssl rand -base64 48` |
   | `KERGUI_HUB_KEY_SEED` | `openssl rand -hex 32` |
   | `KERGUI_HUB_LICENSING_SIGNING_KEY` | `openssl genpkey -algorithm ed25519 -out licensing.key`, then one line for Dokploy: `openssl pkey -in licensing.key -outform DER \| tail -c 32 \| base64` (the 32-byte seed). Its public key goes into the app build (`docs/licensing-key-rotation.md`) |
   | `KERGUI_HUB_NABOOPAY_API_KEY`, `KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET` | NabooPay dashboard; webhook URL `https://hub.kergui.click/licensing/v1/webhooks/naboopay` |
   | `KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON` | the Google service account key on one line: `jq -c . key.json` |
   | `KERGUI_HUB_RELEASES_TOKEN` | `openssl rand -hex 32`; the same value goes into the GitHub repository's secrets for the release workflow |

   Keep a copy of the environment and of `licensing.key` in a password
   manager: without them a restored database is of little use to a new hub
   (access tokens, "reply 1" keys, entitlement signatures).
5. **Version**: Environment → Build-time arguments: `VERSION=<x.y.z>` (see
   below).
6. **Deploy** (push a tag, below), then check
   `curl -s https://hub.kergui.click/ready` → `{"status":"ok",...}`.
7. **Back-office account**, on the server over SSH (the image has no shell,
   so Dokploy's container terminal cannot run it; `docker exec` can):

   ```sh
   C=$(docker ps -q -f name=<hub app name> | head -n1)   # the app name shown in Dokploy
   docker exec -e KERGUI_ADMIN_PASSWORD='at least 12 characters' "$C" /kergui-hub admin create you@example.org
   ```

8. **WhatsApp**: [link the number](#linking-the-whatsapp-number).
9. **Backups**: [Dokploy backups and a restore test](#backups).
10. **Monitoring**: the external check on `https://hub.kergui.click/ready`.

## Environment variables

Every variable the hub reads (`internal/hub/config/config.go`, `FromEnv`).
Values below are examples, never real secrets. **Secret** ones are only ever
typed into Dokploy, never committed. After changing any of them: Dokploy →
the application → **Deploy** (or Redeploy) so the container restarts with
them.

### Core

| Variable | Required | Role | Production value / example |
|---|---|---|---|
| `KERGUI_HUB_DATABASE_URL` | yes, **secret** | Postgres connection | the database's Internal Connection URL + `?sslmode=disable`, e.g. `postgresql://hub:<password>@kergui-hub-db-xxxx:5432/hub?sslmode=disable` |
| `KERGUI_HUB_JWT_SECRET` | yes, **secret** | HS256 secret of access tokens, ≥ 32 bytes | `openssl rand -base64 48` |
| `KERGUI_HUB_PUBLIC_URL` | yes | the hub's public base URL (links, webhook URLs, default return URLs) | `https://hub.kergui.click` |
| `KERGUI_HUB_ENV` | no (`production`) | `production` hides internal details in errors; `development` shows them | `production` |
| `KERGUI_HUB_ADDR` | no (`:8090`) | listen address; the domain's container port must match | leave unset |
| `KERGUI_HUB_CLIENT_IP_HEADER` | no | header the trusted proxy puts the client's address in, for the rate limits. Traefik sets `X-Real-Ip` | `X-Real-Ip` |
| `KERGUI_HUB_TRUST_CLOUDFLARE` | no (`false`) | take the client IP from `CF-Connecting-IP`. Only if the domain is proxied by Cloudflare **and** nothing else reaches Traefik; otherwise a client can forge it | `false` |
| `KERGUI_HUB_TOKEN_TTL` | no (`15m`) | access token lifetime | `15m` |
| `KERGUI_HUB_REFRESH_TTL` | no (`720h`) | refresh token lifetime | `720h` |
| `KERGUI_HUB_LOG_LEVEL` | no (`info`) | `debug`, `info`, `warn`, `error` | `info` |
| `KERGUI_HUB_SHUTDOWN_TIMEOUT` | no (`10s`) | grace period on stop | `10s` |
| `TZ` | no | time zone of the logs | `Africa/Dakar` |

### Email

Without `KERGUI_HUB_SMTP_HOST` the hub logs the emails instead of sending them.

| Variable | Role | Example |
|---|---|---|
| `KERGUI_HUB_MAIL_FROM` | From: header (default `Kergui <no-reply@kergui.local>`) | `Kergui <no-reply@kergui.click>` |
| `KERGUI_HUB_SMTP_HOST` | SMTP server | `smtp.example.org` |
| `KERGUI_HUB_SMTP_PORT` | port (default `587`) | `587` |
| `KERGUI_HUB_SMTP_USER` | login | `no-reply@kergui.click` |
| `KERGUI_HUB_SMTP_PASSWORD` | **secret** | |
| `KERGUI_HUB_SMTP_TLS` | `true` = implicit TLS (465); `false` = STARTTLS | `false` |

### WhatsApp alerts (`docs/hub-whatsapp.md`)

| Variable | Role | Example |
|---|---|---|
| `KERGUI_HUB_NOTIFIER` | `whatsmeow` (WhatsApp session inside the hub) or `console` (logs only; default) | `whatsmeow` |
| `KERGUI_HUB_WHATSAPP_NUMBER` | the Kergui number, E.164; required with `whatsmeow` | `+221770000000` |
| `KERGUI_HUB_KEY_SEED` | **secret**, ≥ 32 bytes; derives each household's "reply 1 to block" key. Empty: replying 1 does nothing | `openssl rand -hex 32` |
| `KERGUI_HUB_WEBHOOK_SECRET` | **secret**, ≥ 24 chars; only for the `console` provider's inbound route. Leave unset with `whatsmeow` | |
| `KERGUI_HUB_WHATSAPP_SEND_INTERVAL` | back-office « Messages » by WhatsApp (issue #81): the queue sends one message every this long (default `40s`). whatsmeow is unofficial and bulk sending gets the number banned: keep it at 40 s or more. Alerts never wait for it | `40s` |
| `KERGUI_HUB_WHATSAPP_GROUP_MAX` | the most numbers one back-office WhatsApp message may be sent to (default `20`); more is refused | `20` |
| `KERGUI_HUB_COMPLAINTS_SUMMARY_INTERVAL` | complaints on WhatsApp (issue #83): each admin number set in the back-office (« Plaintes » → numéros WhatsApp des admins) gets at most one summary per interval, and only when complaints came in since the last one: « 3 nouvelles plaintes (2 paiement, 1 box), dont 1 en attente depuis plus d'un jour. Répondez LISTE pour les voir. » (default `15m`). The summary goes through the queue above and its pace | `15m` |

Complaints on the Kergui number (issue #83). A message that is no alert reply (« 1 », « OK »), no STOP and no opt-in becomes a complaint, or joins the sender's open or answered complaint, and is acknowledged (« Plainte n° 12 reçue… »). Thanks alone (« Merci », an emoji) and media make none. From an admin number, the commands are, in any case, `LISTE` (the open complaints, longest waiting first, 15 at most), `VOIR 12` (the customer's full number, boxes, pass, last messages), `R 12 votre texte` (answers the customer: WhatsApp, plus push if they have the app) and `OK 12` (closes it); anything else gets the help. Answers and closes from WhatsApp are in the action log with the admin's number. Everything is also in the back-office.

### App push (issue #49)

| Variable | Role | Example |
|---|---|---|
| `KERGUI_HUB_PUSH_PROVIDER` | `expo` (Expo push → FCM/APNs) or `console` (logs; default) | `expo` |
| `KERGUI_HUB_EXPO_ACCESS_TOKEN` | **secret**; only if the EAS project has "enhanced security for push notifications" | |
| `KERGUI_HUB_EXPO_PUSH_URL` | override Expo's service (tests, proxy) | leave unset |

### Pro passes (RFC-006)

Without a signing key the licensing routes are off and no pass is sold.

| Variable | Role | Example |
|---|---|---|
| `KERGUI_HUB_LICENSING_SIGNING_KEY` | **secret**; Ed25519 key signing entitlement tokens (PEM, or 32-byte seed in base64/hex; one line is easiest in Dokploy) | see human step 4 |
| `KERGUI_HUB_LICENSING_SIGNING_KEY_FILE` | the same as a mounted file (Dokploy → Advanced → Mounts, a file mount readable by uid 65532). Never both | `/run/secrets/licensing.key` |
| `KERGUI_HUB_LICENSING_NEXT_PUBLIC_KEYS` | rotation: next public keys (base64), comma/space separated | empty outside a rotation |
| `KERGUI_HUB_LICENSING_RETIRING_PUBLIC_KEYS` | rotation: retiring public keys | empty outside a rotation |
| `KERGUI_HUB_LICENSING_REVOKED_KEY_IDS` | rotation: key ids whose tokens are refused | empty |
| `KERGUI_HUB_NABOOPAY_API_KEY` | **secret**; NabooPay (Wave / Orange Money). Empty: no checkout | |
| `KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET` | **secret**; checks NabooPay's `X-Signature` on `/licensing/v1/webhooks/naboopay`. Empty: the sweep alone settles orders | |
| `KERGUI_HUB_NABOOPAY_BASE_URL` | NabooPay's API (default production) | leave unset |
| `KERGUI_HUB_LICENSING_RETURN_URL` | where NabooPay sends the payer back. Default `<PUBLIC_URL>/licensing/v1/checkout/done`; production uses the website's page | `https://kergui.click/compte/retour` |
| `KERGUI_HUB_LICENSING_SWEEP_INTERVAL` | expiry reminders and pending-order re-checks (default `10m`) | `10m` |
| `KERGUI_HUB_LICENSING_PENDING_TTL` | a NabooPay order still pending after this long is **deleted**, at NabooPay (`DELETE /api/v2/transactions/{id}`: the payment link stops working) and in the hub's base; checked every minute. NabooPay is asked first: an order paid meanwhile is credited, never deleted; if NabooPay refuses the delete the order is kept and retried the next minute. A customer who has not paid within this time starts a new purchase (default `3m`, must be > 0) | `3m` |

### Google Play Billing (play flavor)

Off without a package name (`/licensing/v1/play` answers 404). Needs the
signing key above.

| Variable | Role | Example |
|---|---|---|
| `KERGUI_HUB_PLAY_PACKAGE_NAME` | the app's application id | `com.example.kergui` |
| `KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON` | **secret**; service account key, JSON on one line | `jq -c . key.json` |
| `KERGUI_HUB_PLAY_SERVICE_ACCOUNT_FILE` | the same as a mounted file. Never both | |
| `KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT` | email the Pub/Sub push signs its OIDC token as. Empty: no RTDN webhook | `rtdn@project.iam.gserviceaccount.com` |
| `KERGUI_HUB_PLAY_RTDN_AUDIENCE` | push audience (default `<PUBLIC_URL>/licensing/v1/webhooks/play`) | leave unset |

### App updates (website APK, issue #65)

Without a token the CI declare route answers 404; the app's check
(`POST /releases/v1/check`) and the back-office's « Versions » routes
(`/api/v1/admin/releases`) keep working on what was declared before.

| Variable | Role | Example |
|---|---|---|
| `KERGUI_HUB_RELEASES_TOKEN` | **secret**, ≥ 32 characters; the bearer token CI declares each website APK build with (`POST /releases/v1/declare`). Held only in Dokploy and in the GitHub repository's Actions secrets. Changing it: set the new value in both, then redeploy the hub | `openssl rand -hex 32` |

How a build reaches the hub: `mobile-release.yml` creates the GitHub Release,
then its `declare` job (environment `release`) runs
`scripts/mobile/declare-release.sh`, which posts the version, the build number
(the Android versionCode, `github.run_number`), the APK asset's download URL,
its SHA-256 and its size to `<KERGUI_HUB_URL>/releases/v1/declare` (repository
variable `KERGUI_HUB_URL`, default `https://hub.kergui.click`). The hub records
it **pending**: nothing reaches phones until an admin publishes it in the
back-office (« Versions »). Declaring the same build again is a no-op, so a
re-run of the job is safe.

- Secret missing in GitHub: the job is skipped with a notice and the run stays
  green; the build must then be declared by re-running the job once the secret
  exists.
- Hub refuses (401 wrong token, 404 no token in Dokploy, 400, 409 same build
  with another file) or is unreachable after 3 attempts: the `declare` job
  fails with the status and body; the GitHub Release is kept.

Setting it up (human, once): generate the value with `openssl rand -hex 32`,
put it in Dokploy as `KERGUI_HUB_RELEASES_TOKEN` and redeploy the hub, then
create the **same value** as the GitHub secret `KERGUI_HUB_RELEASES_TOKEN` in
the repository's `release` environment (Settings → Environments → release →
Add secret, or `gh secret set KERGUI_HUB_RELEASES_TOKEN --env release`).

### Not in the environment

- `VERSION`: a **build-time argument** (Environment → Build-time arguments),
  not a runtime variable. See below.
- `KERGUI_ADMIN_PASSWORD`: only on the `docker exec ... admin create` command
  line (human step 7), never in the application's environment.

The hub checks the whole environment at start and lists every problem at once
in its logs (Dokploy → Logs), then exits: a half-filled setting fails the
deploy instead of failing later.

## Deploying a version

A deploy is **pushing a `hub-v*` tag**:

```sh
make check && make hub-test          # locally: CI does not gate the deploy
git tag hub-v0.3.0 && git push origin hub-v0.3.0
```

What that tag sets off, side by side:

- **Dokploy** gets GitHub's push event and, because the hub application's
  trigger is "on tag", rebuilds `deploy/hub/Dockerfile` at that tag and
  replaces the running container (stop-first, then the new one runs its
  migrations and turns healthy). Check in the application's settings which
  tags it reacts to: if it is every tag, a `v*` (CLI) tag redeploys the hub
  too, which is harmless but needless.
- **GitHub Actions** (`.github/workflows/hub-ci.yml`) runs the hub tests
  against Postgres and builds the same Dockerfile with
  `VERSION=<tag without hub-v>`, checking the binary reports it. It pushes
  nothing anywhere and Dokploy does not wait for it: a red run means the
  version already deployed is broken, so roll back.

The version the hub reports: set the build-time argument `VERSION=0.3.0` in
Dokploy **before** pushing `hub-v0.3.0`. Without it, `/health` says
`"version":"dev"`, which works but makes the logs and the back-office's health
section unable to tell versions apart.

After every deploy:

```sh
curl -s https://hub.kergui.click/health   # {"status":"ok","version":"0.3.0",...}
curl -s https://hub.kergui.click/ready    # {"status":"ok",...}
```

and a glance at Dokploy → Logs (JSON lines; an `error` at start names the
variable to fix).

**Redeploy without a new version** (an environment change, a stuck
container): Dokploy → the application → Deploy. It rebuilds the same ref.

## Rollback

1. Dokploy → the application → **Deployments**: if your Dokploy has
   rollbacks enabled for the application (it needs a registry), roll back to
   the previous deployment there.
2. Otherwise, deploy the previous version's code under a new tag, which is
   the same mechanism as any deploy:

   ```sh
   git tag hub-v0.3.1 hub-v0.2.0^{commit}   # the last good version's commit
   git push origin hub-v0.3.1               # set VERSION=0.3.1 in Dokploy first
   ```

Migrations only go forward. If the newer version changed the schema in a way
the older one cannot read, the older hub exits at start: restore the backup
taken just before the deploy (below), then deploy the older version. Take a
manual backup (Dokploy → database → Backups → run now, or `backup.sh`) before
any deploy that adds a migration.

## Linking the WhatsApp number

Once the hub runs with `KERGUI_HUB_NOTIFIER=whatsmeow` and
`KERGUI_HUB_WHATSAPP_NUMBER` set, and a back-office account exists (human
step 7):

1. `https://kergui.click/admin/whatsapp` (the back-office), signed in as the
   platform admin. It shows the state and asks the hub for a **pairing
   code**.
2. On the phone that has the Kergui number: WhatsApp → **Appareils
   connectés** → **Connecter un appareil** → **Connecter avec un numéro de
   téléphone**, type the code (valid a few minutes).
3. The page then shows the number linked and connected.

The session is stored in Postgres, so redeploys keep it. To change the
number: unlink from the same page, then link again. The same routes with
`curl`, and the risks: `docs/hub-whatsapp.md`.

## Backups

### Dokploy (primary)

1. Dokploy → Settings → **S3 Destinations**: add a bucket that is **not** on
   this server (any S3-compatible storage).
2. The Postgres database → **Backups** → create: that destination, database
   `hub`, a daily schedule (e.g. `15 3 * * *`), and how many to keep (14).
3. Run one now and check it appears in the bucket.

Dokploy's backup is a `pg_dump` of the database, gzipped. To restore it
into the live database, use the database's restore action if your Dokploy has
one, or the scripts below.

### Scripts (`deploy/hub/scripts/`)

They run on the server over SSH and talk to the Postgres container with
`docker exec`. Point them at Dokploy's database container and its user and
database name:

```sh
export KERGUI_PG_CONTAINER=$(docker ps -q -f name=<db app name> | head -n1)
export KERGUI_PG_USER=hub KERGUI_PG_DB=hub    # the defaults
```

- `backup.sh [dir]`: a second, local copy (custom format, checked with
  `pg_restore --list`, kept `KERGUI_BACKUP_KEEP_DAYS`, default 14). Useful
  before a risky deploy, or from cron if you want one off-Dokploy:
  `15 3 * * * KERGUI_PG_CONTAINER=... /opt/kergui/deploy/hub/scripts/backup.sh /var/backups/kergui-hub`.
- `restore.sh <dump> [database]`: loads a dump. Without a database name it
  **replaces the live one** (it asks first; stop the hub before: Dokploy →
  the application → Stop).
- `restore-test.sh [dump]`: the restore test. Loads the dump into a scratch
  database `hub_restore_test`, checks it has tables, drops it.
  `KERGUI_RESTORE_COMPARE=1` also compares every table's row count with the
  live database (meaningful right after the backup).

### Restore test (monthly, and after any change to backups)

A backup nobody restored is not a backup. With a Dokploy backup:

```sh
# download the newest file from the S3 bucket, then
gunzip -k hub-backup.sql.gz            # if it is gzipped
pg_restore --list hub-backup.sql >/dev/null && echo "custom format"   # else it is plain SQL
deploy/hub/scripts/restore-test.sh hub-backup.sql
# restore-test: OK, hub-backup.sql restores (N tables)
```

`restore-test.sh` takes a custom-format dump (`pg_restore`). If the file is
plain SQL instead, test it by hand into a scratch database:

```sh
docker exec "$KERGUI_PG_CONTAINER" createdb -U hub hub_restore_test
docker exec -i "$KERGUI_PG_CONTAINER" psql -v ON_ERROR_STOP=1 -U hub -d hub_restore_test < hub-backup.sql
docker exec "$KERGUI_PG_CONTAINER" psql -U hub -d hub_restore_test -c '\dt'   # the tables, whatsmeow_* included
docker exec "$KERGUI_PG_CONTAINER" dropdb -U hub hub_restore_test
```

### Restoring for real

1. Dokploy → the hub application → **Stop**.
2. Restore: the database's restore action in Dokploy, or
   `deploy/hub/scripts/restore.sh <dump>` (custom format) on the server.
3. Dokploy → the hub application → **Deploy**; check `/ready`.

On a new server: create the database and the application with the saved
environment (human steps 1 to 5), restore, then deploy. The WhatsApp session
comes back with the database; if WhatsApp says the device was logged out,
link again.

## Households' Kergui backups (issue #88)

Not to be confused with the database backups above: the hub keeps, per
household and per box, the archive of `kergui export` / « Importer une
sauvegarde » (a zip of `appareils.csv` and `conso.csv`). No environment
variable; the tables are `hub_backups` (one current archive per household and
box id) and `hub_backup_versions` (the archives a merge replaced: five per box
at most, seven days at most, pruned on each save). The hub rewrites every
archive it receives from its rows, so only the backup's columns are stored (a
box password slipped into a file never lands in the database).

| Route | Who | Body / answer |
|---|---|---|
| `GET /api/v1/tenants/{tenantId}/router/backups` | any member (and support) | `{"backups": [{boxId, source: {kind, id}, updatedAt, devices, months}]}` |
| `GET /api/v1/tenants/{tenantId}/router/backups/{boxId}` | any member (and support) | 200 `{boxId, source, updatedAt, devices, months, size, archive (base64), name}`, 404 none yet |
| `PUT /api/v1/tenants/{tenantId}/router/backups/{boxId}` | owner or admin of the household (kernel JWT), not support | `{"archive": "<base64 zip>"}` → 200 `{boxId, source, updatedAt, devices, months, size, merged}` |
| `PUT /relay/v1/backups/{boxId}` | a relay of the household (kga_ token) | same body and answer |

`boxId` is the core's box id (the router URL as a slug, `[a-z0-9-]`, ≤ 128).
A second sender's archive is merged into the stored one with the import's
rules: its names and notes win (an empty one keeps the stored), devices from
both sides are kept, the earliest first sighting, the larger month. Errors:
400 `BAD_BACKUP` (not base64 or not a Kergui backup) or a bad box id, 403 a
member or another household, 401 no relay token, 413 an archive over 2 MiB.

The PC relay (`kergui serve --hub`) sends its `kergui export` archive on
`PUT /relay/v1/backups/{boxId}` while it holds the lease, at most once a day
(issue #90). A failure is logged and tried again on the next snapshot cycle;
a 400 or 413 waits for the next day.

The relays do not take names from this backup: a name accepts a device under
auto-reject, so the relay takes the household's names and notes only from a
command the owner signed (`POST /api/labels` on `POST router/commands`, issue
#95; RFC-005 « The household's names through the relay »). The backup keeps
carrying them to the household's phones, as before.

## Key rotation

After each change below: Dokploy → the application → Deploy, then check
`/ready` and the logs.

- **Licensing signing key**: `docs/licensing-key-rotation.md` (next, active,
  retiring, revoked; the app must ship the next public key first).
- **JWT secret**: change `KERGUI_HUB_JWT_SECRET`. Every access token in
  flight becomes invalid; users sign in again.
- **Hub key seed** (`KERGUI_HUB_KEY_SEED`): changing it only breaks the
  "reply 1 to block" shortcut for households until they relink WhatsApp.
- **WhatsApp session**: unlink in `/admin/whatsapp` (or
  `DELETE /api/v1/admin/whatsapp`), then link again.
- **NabooPay**, **Expo**, **Play** keys: regenerate at the provider, update
  the variable.
- **Release token** (`KERGUI_HUB_RELEASES_TOKEN`): a new `openssl rand -hex 32`
  in Dokploy and in the GitHub Actions secret, then redeploy. Only CI's
  declarations depend on it; phones are unaffected.
- **Postgres password**: change it on the Dokploy database, then update the
  password inside `KERGUI_HUB_DATABASE_URL`.

## What is not used any more

- No GHCR image: `hub-ci.yml` builds the image to test it but pushes nothing;
  Dokploy builds its own. Old `ghcr.io/.../kergui-hub` packages can be
  deleted on GitHub.
- No `cloudflared` for the hub and no `deploy.sh` (removed): Traefik serves
  the domain, Dokploy deploys. `deploy/cloudflared/config.yml` is unrelated:
  it belongs to the `kergui` box's own payment pages (`docs/mobile/security.md`).
- `deploy/hub/compose.yaml` + `hub.env.example`: a hub on your own machine
  (built from source), not production.
