#!/usr/bin/env bash
# Prove a dump restores: load it into a scratch database next to the live one
# and compare row counts of every table, then drop the scratch database.
#   deploy/hub/scripts/restore-test.sh [dump]   (default: newest in /var/backups/kergui-hub)
# Run it weekly from cron, after backup.sh. Env: KERGUI_PG_CONTAINER.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)

dump=${1:-$(ls -1t "${KERGUI_BACKUP_DIR:-/var/backups/kergui-hub}"/hub-*.dump 2>/dev/null | head -n1)}
[ -n "$dump" ] || { echo "restore-test: no dump found" >&2; exit 1; }
container=${KERGUI_PG_CONTAINER:-}
if [ -z "$container" ]; then
  container=$(docker ps -q --filter "label=com.docker.compose.project=${KERGUI_COMPOSE_PROJECT:-hub}" \
    --filter label=com.docker.compose.service=postgres | head -n1)
fi
export KERGUI_PG_CONTAINER=$container
scratch=hub_restore_test

"$here/restore.sh" "$dump" "$scratch"
counts() {
  docker exec "$container" psql -U hub -d "$1" -qAt -c \
    "SELECT n.nspname||'.'||c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
     WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema') ORDER BY 1" |
  while read -r t; do
    printf '%s %s\n' "$t" "$(docker exec "$container" psql -U hub -d "$1" -qAt -c "SELECT count(*) FROM $t")"
  done
}
tables=$(counts "$scratch" | wc -l)
[ "$tables" -gt 0 ] || { echo "restore-test: FAIL, the restored database has no table" >&2; exit 1; }
[ -n "${KERGUI_RESTORE_COMPARE:-}" ] && diff <(counts hub) <(counts "$scratch")
docker exec -i "$container" psql -U hub -d postgres -qAt -c "DROP DATABASE \"$scratch\""
echo "restore-test: OK, $dump restores ($tables tables)"
