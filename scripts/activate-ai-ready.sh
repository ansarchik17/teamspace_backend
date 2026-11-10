#!/usr/bin/env bash
# Operator-controlled activation of the already rehearsed, local images only.
set -Eeuo pipefail
umask 077
MODE="${1:---check}"
[[ "$MODE" == --check || "$MODE" == --apply ]] || exit 2
ROOT=/opt/workspace
MAIL="$ROOT/mail"
READY=/root/ai-ready-20261020-01
VERSION=ai-observe-20261020-01
exec 9>"$ROOT/releases/.deploy.lock"
flock -n 9 || { echo 'Another deployment is running'; exit 1; }
exec 8>"$ROOT/releases/.images.lock"
flock -n 8 || exit 1
exec 7>"$MAIL/.images.lock"
flock -n 7 || exit 1
source "$ROOT/scripts/shared-mode.sh"
shared_infra_activate "$ROOT" "$ROOT/compose.prod.yml" workspace
shared_infra_activate "$MAIL" "$MAIL/compose.production.yaml" mail
[[ -s "$READY/REHEARSAL_BACKUP_PATH" && -s "$READY/REHEARSED_IMAGES.txt" ]]
[[ -s "$(cat "$READY/REHEARSAL_BACKUP_PATH")/workspace.dump" ]]
for pair in \
  "6160b4ffc528f009f6cfa42b2329d2b883d87bbed11e5a345ce0982cd53302c2 workspace-backend" \
  "31483214047fbc3e5d29c1c074f07dc017e7cde68b9570591202a8bd935c5685 workspace-front" \
  "9f995e29c4814cbd56ce03f82dd71304c0924cdaf9914ae7080f8e84f93d3024 workspace-front-mail"; do
  read -r digest name <<< "$pair"
  expected="sha256:$digest"
  tag="$name:$VERSION"
  grep -Fx "$expected $tag" "$READY/REHEARSED_IMAGES.txt" >/dev/null
  [[ "$(docker image inspect --format '{{.Id}}' "$tag")" == "$expected" ]]
done
STAMP="$(date -u +%Y%m%dT%H%M%SZ)-$$"
BACKUP="$ROOT/backups/ai-activate-$STAMP"
mkdir -m 0700 "$BACKUP"
cp -p "$ROOT/releases/images.env" "$BACKUP/workspace-images.env"
cp -p "$MAIL/images.env" "$BACKUP/mail-images.env"
cp -p "$ROOT/.env" "$BACKUP/workspace.env"
cp -p "$ROOT/compose.prod.yml.shared.yml" "$MAIL/compose.production.yaml.shared.yml" "$BACKUP/"
W=(docker compose --env-file "$ROOT/.env" --env-file "$ROOT/releases/images.env" --env-file "$ROOT/.shared-infra.env" -f "$ROOT/compose.prod.yml.shared.yml")
M=(docker compose --env-file "$MAIL/.env.production" --env-file "$MAIL/images.env" --env-file "$MAIL/.shared-infra.env" -f "$MAIL/compose.production.yaml.shared.yml")
python3 - "$BACKUP" "$VERSION" <<'PY'
import json,sys
p,v=sys.argv[1:]
json.dump({'services':{s:{'image':f'{image}:{v}'} for s,image in [('api','workspace-backend'),('ai-document-worker','workspace-backend'),('frontend','workspace-front')]}},open(p+'/candidate-workspace.json','w'))
json.dump({'services':{'app':{'image':f'workspace-front-mail:{v}'}}},open(p+'/candidate-mail.json','w'))
json.dump({'services':{'api':{'mem_limit':'256m','memswap_limit':'256m','cpus':1,'environment':{'RUN_DB_MIGRATIONS':'true','GOMEMLIMIT':'128MiB','GOMAXPROCS':'1','LOG_OUTPUT':'stdout'}}}},open(p+'/migration.json','w'))
PY
# Preserve immutable rollback references, including a mutable old :main tag.
for spec in 'workspace-api api workspace' 'workspace-ai-document-worker ai-document-worker workspace' 'workspace-frontend frontend workspace' 'axis-mail-app-app-1 app mail'; do
  read -r container service project <<< "$spec"
  [[ "$(docker inspect --format '{{.State.Health.Status}}' "$container")" == healthy ]]
  old_id="$(docker inspect --format '{{.Image}}' "$container")"
  rollback="workspace-ai-rollback:$STAMP-$service"
  docker tag "$old_id" "$rollback"
  python3 - "$BACKUP/rollback-$project.json" "$service" "$rollback" <<'PY'
import json,sys,os
p,s,i=sys.argv[1:]
d=json.load(open(p)) if os.path.exists(p) else {'services':{}}
d['services'][s]={'image':i}
json.dump(d,open(p,'w'))
PY
done
WC=("${W[@]}" -f "$BACKUP/candidate-workspace.json")
MC=("${M[@]}" -f "$BACKUP/candidate-mail.json")
"${WC[@]}" config --format json | python3 -c '
import json,sys
s=json.load(sys.stdin)["services"]
for name in ("api","ai-document-worker"):
 e=s[name]["environment"]
 assert str(e.get("AI_ACCOUNTING_ENABLED")).lower()=="true"
 assert e.get("AI_ENFORCEMENT_MODE")=="observe"
 assert str(e.get("AI_RECONCILIATION_ENABLED")).lower()=="true"
 assert e.get("POSTGRES_DB")=="workspace" and e.get("POSTGRES_HOST")=="shared-postgres"
print("PASS: candidate API and worker configuration")'
"${MC[@]}" config --quiet
echo "Validated local candidates; recovery files: $BACKUP"
[[ "$MODE" == --apply ]] || { echo 'CHECK ONLY: services and database unchanged'; exit 0; }
wait_health() {
  local container="$1" expected="$2" attempt
  for attempt in $(seq 1 60); do
    if [[ "$(docker inspect --format '{{.Image}} {{.State.Health.Status}}' "$container" 2>/dev/null || true)" == "$expected healthy" ]]; then return 0; fi
    sleep 2
  done
  echo "Health verification failed: $container" >&2
  return 1
}
CHANGED=false
rollback() {
  result=$?
  trap - EXIT
  if [[ "$result" -ne 0 && "$CHANGED" == true ]]; then
    echo 'Activation failed. Restoring previous images; accounting data and schema are preserved.' >&2
    cp -p "$BACKUP/workspace-images.env" "$ROOT/releases/images.env"
    cp -p "$BACKUP/mail-images.env" "$MAIL/images.env"
    "${W[@]}" -f "$BACKUP/rollback-workspace.json" up -d --no-deps --no-build --pull never api ai-document-worker frontend || true
    "${M[@]}" -f "$BACKUP/rollback-mail.json" up -d --no-deps --no-build --pull never app || true
    echo "Recovery attempted; inspect container health. Recovery files: $BACKUP" >&2
  fi
  exit "$result"
}
trap rollback EXIT
echo 'Creating a fresh database backup before migrations...'
docker exec shared-postgres pg_dump -U workspace_app -d workspace -Fc > "$BACKUP/workspace.dump.partial"
docker exec -i shared-postgres pg_restore --list < "$BACKUP/workspace.dump.partial" >/dev/null
mv "$BACKUP/workspace.dump.partial" "$BACKUP/workspace.dump"
sha256sum "$BACKUP/workspace.dump" > "$BACKUP/workspace.dump.sha256"
echo 'Applying rehearsed migrations; existing API is still running...'
"${WC[@]}" -f "$BACKUP/migration.json" run --rm --no-deps -T --entrypoint /app/migrate api
count="$(docker exec shared-postgres psql -XAt -U workspace_app -d workspace -v ON_ERROR_STOP=1 -c 'SELECT count(*) FROM goose_db_version WHERE is_applied AND version_id BETWEEN 20261020100100 AND 20261020101000;')"
[[ "$count" == 10 ]]
CHANGED=true
echo 'Updating API and worker...'
"${WC[@]}" up -d --no-deps --no-build --pull never api ai-document-worker
backend_id="$(docker image inspect --format '{{.Id}}' "workspace-backend:$VERSION")"
wait_health workspace-api "$backend_id"
wait_health workspace-ai-document-worker "$backend_id"
echo 'Updating frontend...'
"${WC[@]}" up -d --no-deps --no-build --pull never frontend
wait_health workspace-frontend "$(docker image inspect --format '{{.Id}}' "workspace-front:$VERSION")"
echo 'Updating mail application...'
"${MC[@]}" up -d --no-deps --no-build --pull never app
wait_health axis-mail-app-app-1 "$(docker image inspect --format '{{.Id}}' "workspace-front-mail:$VERSION")"
curl -fsS --max-time 10 http://127.0.0.1:18081/health >/dev/null
curl -fsS --max-time 10 http://127.0.0.1:13000/health >/dev/null
docker inspect workspace-api workspace-ai-document-worker | python3 -c '
import json,sys
for c in json.load(sys.stdin):
 e=dict(x.split("=",1) for x in c["Config"]["Env"])
 assert e.get("AI_ACCOUNTING_ENABLED")=="true" and e.get("AI_ENFORCEMENT_MODE")=="observe" and e.get("AI_RECONCILIATION_ENABLED")=="true"
print("PASS: running API/worker use observation mode")'
# Persist the verified selection atomically, preserving unrelated settings and permissions.
python3 - "$ROOT/releases/images.env" "$MAIL/images.env" "$VERSION" <<'PY'
import os,sys,tempfile
v=sys.argv[3]
for p,values in [(sys.argv[1],{'BACKEND_IMAGE':f'workspace-backend:{v}','FRONTEND_IMAGE':f'workspace-front:{v}'}),(sys.argv[2],{'MAIL_APP_IMAGE':f'workspace-front-mail:{v}'})]:
 st=os.stat(p)
 lines=[l for l in open(p).read().splitlines() if l.split('=',1)[0].strip() not in values]
 lines += [k+'='+value for k,value in values.items()]
 fd,t=tempfile.mkstemp(dir=os.path.dirname(p))
 with os.fdopen(fd,'w') as f: f.write('\n'.join(lines)+'\n')
 os.chmod(t,st.st_mode & 0o777); os.chown(t,st.st_uid,st.st_gid); os.replace(t,p)
PY
CHANGED=false
echo "PASS: release activated in OBSERVE mode. Backup: $BACKUP"
echo 'No financial blocking enabled. Browser login/chat/admin checks are still required.'
echo 'Do not deploy older main images over this release. Unknown usage remains reserved.'
