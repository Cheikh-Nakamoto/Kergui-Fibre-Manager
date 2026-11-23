# Hub in production: runbook

The hub (RFC-005) runs on one VPS as a docker compose stack,
`deploy/hub/compose.yaml`:

| Service | Image | Role |
|---|---|---|
| `postgres` | `postgres:16-alpine` | the hub's database (volume `hub-db`) |
| `hub` | `ghcr.io/cheikh-nakamoto/kergui-hub:<version>` | the hub, on `127.0.0.1:8090` |
| `cloudflared` | profile `tunnel` | publishes the hub; no port is opened on the VPS |

- **Image**: CI (`.github/workflows/hub-ci.yml`) runs the hub tests, builds
  the image and pushes it to GHCR **only on a `hub-v*` tag** (or by hand).
  Tag `hub-v0.2.0` gives `ghcr.io/cheikh-nakamoto/kergui-hub:0.2.0` and
  `:latest`; the binary reports it: `docker run --rm <image> -version`.
- **Migrations** run when the hub starts, before it listens. A version that
  cannot migrate exits; compose restarts it and `/ready` never turns green.
- **Health**: `GET /health` (liveness, the process answers) and `GET /ready`
  (readiness, the database answers). The compose healthcheck runs
  `/kergui-hub -healthcheck` against `/ready` (the image has no shell).
  Monitoring: point an external HTTP check (UptimeRobot, Better Stack,
  Cloudflare health check...) at `https://<hub host>/ready`, every minute,
  alert on anything but 200.
- **Secrets** come only from `deploy/hub/hub.env` on the VPS (gitignored,
  `chmod 600`), or mounted files for the `_FILE` variables. Nothing secret is
  in the repository or in the image.

All commands below run from the repository checkout on the VPS
(`/opt/kergui`), with the project name `hub`:

```sh
alias dc='docker compose -p hub -f deploy/hub/compose.yaml --env-file deploy/hub/hub.env --profile tunnel'
```

## Human steps (once)

Nobody but the owner can do these.

1. **VPS**: Debian 12 / Ubuntu 24.04, 2 vCPU, 2 GB RAM, 20 GB disk. Install
   Docker Engine and the compose plugin (`https://docs.docker.com/engine/install/`),
   create a user in the `docker` group, SSH keys only. Firewall: only SSH in
   (the tunnel is outbound): `ufw default deny incoming; ufw allow OpenSSH; ufw enable`.
2. **DNS**: the domain is on Cloudflare (nameservers moved to Cloudflare).
   Choose the hub's host name, e.g. `hub.<domain>`. No A record to create:
   the tunnel adds its CNAME.
3. **Cloudflare tunnel**: Zero Trust > Networks > Tunnels > Create a tunnel
   (cloudflared), name `kergui-hub`. Copy the **token** it shows (the
   `eyJ...` after `--token`). Public hostname: `hub.<domain>`, service
   `HTTP`, URL `hub:8090`. Do not publish
   anything else.
4. **GHCR**: after the first `hub-v*` tag, the package
   `ghcr.io/cheikh-nakamoto/kergui-hub` is private. Either make it public
   (GitHub > Packages > kergui-hub > Package settings > Change visibility), or
   on the VPS `docker login ghcr.io -u <github user>` with a personal access
   token (classic) that has only `read:packages`.
5. **Secrets**, into `deploy/hub/hub.env` (from `hub.env.example`):

   | Variable | How to make it |
   |---|---|
   | `KERGUI_HUB_DB_PASSWORD` | `openssl rand -hex 24` |
   | `KERGUI_HUB_JWT_SECRET` | `openssl rand -base64 48` |
   | `KERGUI_HUB_KEY_SEED` | `openssl rand -hex 32` |
   | `KERGUI_HUB_WEBHOOK_SECRET` | `openssl rand -hex 24` |
   | `KERGUI_HUB_LICENSING_SIGNING_KEY` | `openssl genpkey -algorithm ed25519 -out licensing.key` (see below). Its public key goes into the app build (`docs/licensing-key-rotation.md`) |
   | `KERGUI_HUB_NABOOPAY_API_KEY`, `KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET` | NabooPay dashboard; webhook URL `https://hub.<domain>/licensing/v1/webhooks/naboopay` |
   | `KERGUI_CLOUDFLARE_TUNNEL_TOKEN` | step 3 |
   | `KERGUI_HUB_SMTP_*` | the mail provider, if emails are sent |

   And the non-secret ones: `KERGUI_HUB_PUBLIC_URL=https://hub.<domain>`,
   `KERGUI_HUB_TRUST_CLOUDFLARE=true`, `KERGUI_HUB_ENV=production`,
   `KERGUI_HUB_NOTIFIER=whatsmeow`, `KERGUI_HUB_WHATSAPP_NUMBER=+221...`.
   Keep a copy of `hub.env` and `licensing.key` in a password manager:
   without them the backups cannot be used by a new hub (tokens, reply keys,
   entitlement signatures).

   The licensing key: inline it in `KERGUI_HUB_LICENSING_SIGNING_KEY` (the
   PEM, or its 32-byte seed in base64). `deploy.sh` uses only
   `compose.yaml`; to use `_FILE` instead, mount the file on the `hub`
   service (read-only, owned by uid 65532, the image's `nonroot` user) in a
   compose override on the VPS and run compose with it.
6. **WhatsApp** (whatsmeow, inside the hub): once the hub runs, link the
   Kergui number from the admin route and type the pairing code on that
   phone. Steps: `docs/hub-whatsapp.md`.
7. **Monitoring**: the external check on `https://hub.<domain>/ready`.

## First deploy (clean VPS)

```sh
sudo mkdir -p /opt/kergui /var/backups/kergui-hub && sudo chown "$USER" /opt/kergui /var/backups/kergui-hub
git clone https://github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager.git /opt/kergui
cd /opt/kergui
cp deploy/hub/hub.env.example deploy/hub/hub.env && chmod 600 deploy/hub/hub.env
$EDITOR deploy/hub/hub.env          # human step 5
deploy/hub/scripts/deploy.sh 0.2.0  # the hub-v0.2.0 tag
```

`deploy.sh <version>` writes the version into `hub.env`, takes a backup if
the database already runs, pulls the image, `up -d --wait`s the whole stack
(profile `tunnel`; `KERGUI_PROFILES` to change) and checks that the
running hub reports that version. Then do human step 6, then
`curl https://hub.<domain>/ready` must answer `{"status":"ok",...}`.

Daily backup and weekly restore test, `crontab -e`:

```cron
15 3 * * *  /opt/kergui/deploy/hub/scripts/backup.sh /var/backups/kergui-hub >> /var/log/kergui-backup.log 2>&1
45 3 * * 0  KERGUI_BACKUP_DIR=/var/backups/kergui-hub /opt/kergui/deploy/hub/scripts/restore-test.sh >> /var/log/kergui-backup.log 2>&1
```

Backups stay 14 days (`KERGUI_BACKUP_KEEP_DAYS`). Copy them off the VPS too
(`rclone` / `rsync` to another machine or object storage): a backup on the
disk it protects is not enough.

## Update

1. Tag the release on `main`: `git tag hub-v0.3.0 && git push origin hub-v0.3.0`.
   Wait for `hub-ci` to be green (it pushes the image).
2. On the VPS: `cd /opt/kergui && git pull && deploy/hub/scripts/deploy.sh 0.3.0`
   (`git pull` only matters when `deploy/hub/` changed).

## Rollback

`deploy/hub/scripts/deploy.sh 0.2.0`, the previous version. Migrations only
go forward: if the newer version migrated the schema in a way the older one
cannot read, restore the backup `deploy.sh` took just before the update
(below), then deploy the older version.

## Restore

```sh
dc stop hub
deploy/hub/scripts/restore.sh /var/backups/kergui-hub/hub-<date>.dump   # asks before replacing "hub"
dc start hub
curl -s http://127.0.0.1:8090/ready
```

`restore.sh <dump> <other name>` loads into another database instead, which
is what `restore-test.sh` does (`hub_restore_test`, row counts checked, then
dropped; `KERGUI_RESTORE_COMPARE=1` also compares every table's count with
the live database, meaningful right after a backup). On a new VPS: first
deploy with the saved `hub.env` and `licensing.key`, then restore.

## Key rotation

- **Licensing signing key**: `docs/licensing-key-rotation.md` (next, active,
  retiring, revoked; the app must ship the next public key first).
- **JWT secret**: change `KERGUI_HUB_JWT_SECRET`, `dc up -d hub`. Every access
  token in flight becomes invalid; users sign in again (or the app refreshes,
  if the refresh path does not depend on that secret).
- **Hub key seed** (`KERGUI_HUB_KEY_SEED`): changing it only breaks the
  "reply 1 to block" shortcut for households until they relink WhatsApp.
- **WhatsApp session**: `DELETE /api/v1/admin/whatsapp`, then link again
  (`docs/hub-whatsapp.md`).
- **NabooPay** keys, **tunnel
  token**: regenerate at the provider, update `hub.env`, `dc up -d`.
- **Postgres password**: `dc exec postgres psql -U hub -d hub -c "ALTER USER hub PASSWORD '<new>'"`,
  then set `KERGUI_HUB_DB_PASSWORD` and `dc up -d hub`.

After any change to `hub.env`: `dc up -d` recreates the containers whose
settings changed; check `/ready` and `dc logs --tail 50 hub`.
