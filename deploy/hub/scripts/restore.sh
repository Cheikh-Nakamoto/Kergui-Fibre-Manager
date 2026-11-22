#!/usr/bin/env bash
# Restore a backup.sh dump into the hub's Postgres container.
#   deploy/hub/scripts/restore.sh <dump> [database]
# Default database "hub" (stop the hub first: docker compose ... stop hub).
# Any other name is created fresh, which is how restore-test.sh checks a dump
# without touching the live data. Env: KERGUI_PG_CONTAINER as for backup.sh.
set -euo pipefail

dump=${1:?usage: restore.sh <dump> [database]}
db=${2:-hub}
[ -r "$dump" ] || { echo "restore: cannot read $dump" >&2; exit 1; }
container=${KERGUI_PG_CONTAINER:-}
if [ -z "$container" ]; then
  container=$(docker ps -q --filter "label=com.docker.compose.project=${KERGUI_COMPOSE_PROJECT:-hub}" \
    --filter label=com.docker.compose.service=postgres | head -n1)
fi
[ -n "$container" ] || { echo "restore: hub Postgres container not found" >&2; exit 1; }

psql() { docker exec -i "$container" psql -v ON_ERROR_STOP=1 -U hub -d postgres -qAt "$@"; }
if [ "$db" = hub ]; then
  if [ "${KERGUI_RESTORE_YES:-}" != 1 ]; then
    read -r -p "Replace the live database 'hub' with $dump? [y/N] " a
    [ "$a" = y ] || exit 1
  fi
  psql -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='hub' AND pid<>pg_backend_pid()" >/dev/null
fi
psql -c "DROP DATABASE IF EXISTS \"$db\"" -c "CREATE DATABASE \"$db\" OWNER hub"
docker exec -i "$container" pg_restore -U hub -d "$db" --no-owner --exit-on-error < "$dump"
echo "restore: $dump -> $db"
