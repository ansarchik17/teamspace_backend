#!/usr/bin/env bash
# Restore a production archive into an isolated temporary PostgreSQL and run
# ONLY the supplied candidate's migrations. Never mounts production data/env.
set -Eeuo pipefail
IMAGE="${1:?Usage: rehearse-ai-production.sh CANDIDATE_IMAGE BACKUP_DUMP}"
DUMP="${2:?Backup dump is required}"
[[ -f "$DUMP" ]] || { echo 'Backup archive not found' >&2; exit 1; }
docker image inspect "$IMAGE" >/dev/null
PG_IMAGE="$(docker inspect --format '{{.Image}}' shared-postgres)"
NAME="workspace-ai-rehearsal-$(date -u +%Y%m%dT%H%M%SZ)-$$"
LABEL="ai-production-rehearsal"
cleanup() {
  status=$?
  if docker inspect "$NAME" >/dev/null 2>&1; then
    if [[ "$status" -ne 0 ]]; then docker logs --tail 40 "$NAME" || true; fi
    owner="$(docker inspect --format '{{index .Config.Labels "ai-production-rehearsal"}}' "$NAME")"
    [[ "$owner" != "$NAME" ]] || docker rm -f -v "$NAME" >/dev/null
  fi
  return "$status"
}
trap cleanup EXIT
docker run -d --name "$NAME" --label "$LABEL=$NAME" --network none \
  --memory 512m --memory-swap 512m --cpus 1 --tmpfs /var/lib/postgresql/data:rw,size=256m \
  -e POSTGRES_USER=workspace_app -e POSTGRES_DB=workspace_restore_check \
  -e POSTGRES_HOST_AUTH_METHOD=trust "$PG_IMAGE" \
  -c shared_buffers=64MB -c max_connections=20 -c maintenance_work_mem=32MB >/dev/null
ready=false
for attempt in $(seq 1 90); do
  if docker exec "$NAME" pg_isready -h 127.0.0.1 -U workspace_app -d workspace_restore_check >/dev/null 2>&1; then
    ready=true; break
  fi
  sleep 1
done
[[ "$ready" == true ]] || { echo 'Temporary PostgreSQL is not ready' >&2; exit 1; }
docker exec -i "$NAME" pg_restore --exit-on-error --no-owner --no-privileges \
  -h 127.0.0.1 -U workspace_app -d workspace_restore_check < "$DUMP"
sql() { docker exec "$NAME" psql -X -h 127.0.0.1 -U workspace_app -d workspace_restore_check -At -v ON_ERROR_STOP=1 -c "$1"; }
[[ "$(sql "SELECT to_regclass('public.iform_operation_receipts') IS NOT NULL")" == t ]] || { echo 'Expected released iForm schema is absent' >&2; exit 1; }
[[ "$(sql "SELECT to_regclass('public.organization_ai_budgets') IS NULL")" == t ]] || { echo 'AI schema already exists: requires a separate reviewed upgrade path' >&2; exit 1; }
fingerprint_sql="SELECT md5(COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]')) FROM iform_operation_receipts t"
before="$(sql "$fingerprint_sql")"
run_migrations() {
  docker run --rm --network "container:$NAME" --memory 256m --memory-swap 256m --cpus 1 \
    -e GOMEMLIMIT=128MiB -e GOMAXPROCS=1 \
    -e POSTGRES_HOST=127.0.0.1 -e POSTGRES_PORT=5432 \
    -e POSTGRES_USER=workspace_app -e POSTGRES_PASSWORD=rehearsal-only \
    -e POSTGRES_DB=workspace_restore_check -e POSTGRES_SSL_MODE=disable \
    -e RUN_DB_MIGRATIONS=true -e LOG_OUTPUT=stdout \
    --entrypoint /app/migrate "$IMAGE"
}
run_migrations
[[ "$before" == "$(sql "$fingerprint_sql")" ]] || { echo 'iForm records changed' >&2; exit 1; }
[[ "$(sql "SELECT count(*) FROM goose_db_version WHERE is_applied AND version_id IN (20261020100100,20261020100200,20261020100300,20261020100400,20261020100500,20261020100600,20261020100700,20261020100800,20261020100900,20261020101000)")" == 10 ]] || { echo 'AI migration ledger is incomplete' >&2; exit 1; }
ledger="$(sql 'SELECT count(*) FROM goose_db_version')"
run_migrations
[[ "$ledger" == "$(sql 'SELECT count(*) FROM goose_db_version')" ]] || { echo 'Repeated migration changed the ledger' >&2; exit 1; }
echo 'PASS: production archive restored; ten AI migrations applied; iForm records preserved; repeated migration is a no-op. Production was not changed.'
