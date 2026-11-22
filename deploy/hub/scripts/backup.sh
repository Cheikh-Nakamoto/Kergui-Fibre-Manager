#!/usr/bin/env bash
# Dump the hub's Postgres (custom format, compressed) from its container.
#   deploy/hub/scripts/backup.sh [backup-dir]
# Env: KERGUI_PG_CONTAINER (default: the compose "postgres" service of project
# KERGUI_COMPOSE_PROJECT, default "hub"), KERGUI_BACKUP_KEEP_DAYS (default 14).
# Daily from cron:  15 3 * * * /opt/kergui/deploy/hub/scripts/backup.sh /var/backups/kergui-hub
set -euo pipefail

dir=${1:-/var/backups/kergui-hub}
keep=${KERGUI_BACKUP_KEEP_DAYS:-14}
container=${KERGUI_PG_CONTAINER:-}
if [ -z "$container" ]; then
  container=$(docker ps -q --filter "label=com.docker.compose.project=${KERGUI_COMPOSE_PROJECT:-hub}" \
    --filter label=com.docker.compose.service=postgres | head -n1)
fi
[ -n "$container" ] || { echo "backup: hub Postgres container not found" >&2; exit 1; }

mkdir -p "$dir"
umask 077
out="$dir/hub-$(date -u +%Y%m%dT%H%M%SZ).dump"
docker exec "$container" pg_dump -U hub -d hub -Fc -Z 6 > "$out.part"
# A dump that pg_restore cannot list is no backup.
docker exec -i "$container" pg_restore --list < "$out.part" > /dev/null
mv "$out.part" "$out"
find "$dir" -name 'hub-*.dump' -mtime +"$keep" -delete
echo "backup: $out ($(du -h "$out" | cut -f1))"
