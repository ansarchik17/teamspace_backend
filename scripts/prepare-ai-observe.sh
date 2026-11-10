#!/usr/bin/env bash
# Explicit operator step: prepare shared API/worker settings, without restarting.
set -Eeuo pipefail
ROOT=/opt/workspace
ENV_FILE="$ROOT/.env"
[[ -f "$ENV_FILE" && -f "$ROOT/compose.prod.yml.shared.yml" ]] || exit 1
umask 077
exec 9>"$ROOT/releases/.deploy.lock"
flock 9
BACKUP="$ROOT/backups/ai-observe-config-$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -m 0700 "$BACKUP"
cp -p "$ENV_FILE" "$BACKUP/.env"
TEMP="$(mktemp "$ROOT/.env.ai-observe.XXXXXX")"
trap 'rm -f -- "$TEMP"' EXIT
awk '!/^[[:space:]]*(export[[:space:]]+)?(AI_ACCOUNTING_ENABLED|AI_ENFORCEMENT_MODE|AI_RECONCILIATION_ENABLED)[[:space:]]*=/ {print}
END {print "AI_ACCOUNTING_ENABLED=true"; print "AI_ENFORCEMENT_MODE=observe"; print "AI_RECONCILIATION_ENABLED=true"}' "$ENV_FILE" > "$TEMP"
chmod --reference="$ENV_FILE" "$TEMP"
chown --reference="$ENV_FILE" "$TEMP"
mv -f -- "$TEMP" "$ENV_FILE"
COMPOSE=(docker compose --env-file "$ENV_FILE" --env-file "$ROOT/releases/images.env" --env-file "$ROOT/.shared-infra.env" -f "$ROOT/compose.prod.yml.shared.yml")
if ! "${COMPOSE[@]}" config --format json | python3 -c '
import json,sys
config=json.load(sys.stdin)
for name in ("api","ai-document-worker"):
    env=config["services"][name]["environment"]
    if str(env.get("AI_ACCOUNTING_ENABLED","")).lower()!="true" or env.get("AI_ENFORCEMENT_MODE")!="observe" or str(env.get("AI_RECONCILIATION_ENABLED","")).lower()!="true":
        raise SystemExit("API/worker observe settings are overridden; abort")
print("PASS: API and worker use accounting=true, mode=observe, reconciliation=true (unknown provider results still require manual evidence)")
'; then
  cp -p "$BACKUP/.env" "$ENV_FILE"
  echo 'Validation failed; original environment file restored. No services restarted.' >&2
  exit 1
fi
echo "Prepared observation mode. Backup: $BACKUP. Running services have NOT been restarted."
