#!/usr/bin/env bash
# Deploy (first time, update or rollback) a hub version on the VPS:
#   deploy/hub/scripts/deploy.sh <version>     e.g. 0.2.0 (the hub-v0.2.0 tag)
# Writes KERGUI_HUB_VERSION into hub.env, takes a backup first (if the
# database already runs), pulls the image, restarts the stack and waits for
# /ready. Migrations run when the hub starts. Profiles: KERGUI_PROFILES
# (default "evolution tunnel").
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
version=${1:?usage: deploy.sh <version>}
env_file=$here/hub.env
[ -f "$env_file" ] || { echo "deploy: $env_file missing (cp hub.env.example hub.env, fill it)" >&2; exit 1; }
grep -q '^KERGUI_HUB_JWT_SECRET=..' "$env_file" || { echo "deploy: KERGUI_HUB_JWT_SECRET is empty" >&2; exit 1; }

if grep -q '^KERGUI_HUB_VERSION=' "$env_file"; then
  sed -i "s/^KERGUI_HUB_VERSION=.*/KERGUI_HUB_VERSION=$version/" "$env_file"
else
  echo "KERGUI_HUB_VERSION=$version" >> "$env_file"
fi
profiles=()
for p in ${KERGUI_PROFILES:-evolution tunnel}; do profiles+=(--profile "$p"); done
dc() { docker compose -p "${KERGUI_COMPOSE_PROJECT:-hub}" -f "$here/compose.yaml" --env-file "$env_file" "${profiles[@]}" "$@"; }

if dc ps --status running --services 2>/dev/null | grep -qx postgres; then
  "$here/scripts/backup.sh" "${KERGUI_BACKUP_DIR:-/var/backups/kergui-hub}"
fi
dc pull hub
dc up -d --wait
got=$(dc exec -T hub /kergui-hub -version)
[ "$got" = "$version" ] || { echo "deploy: hub reports $got, expected $version" >&2; exit 1; }
echo "deploy: hub $version is up and ready"
