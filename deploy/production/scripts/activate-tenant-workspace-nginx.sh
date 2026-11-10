#!/usr/bin/env bash
set -Eeuo pipefail

SOURCE=/opt/workspace/nginx/tenant-workspace-wildcard.conf
LIVE=/etc/nginx/conf.d/growy-tenant-wildcard.conf
BACKUP_DIR=/opt/workspace/nginx/backups

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run this script as root" >&2
  exit 1
fi

[[ -f "$SOURCE" && -f "$LIVE" ]] || {
  echo "Missing candidate or live wildcard configuration" >&2
  exit 1
}
grep -Fq 'server_name *.teamspace.kz;' "$LIVE" || {
  echo "Live file is not the expected wildcard virtual host" >&2
  exit 1
}
[[ -f /etc/letsencrypt/live/teamspace-wildcard/fullchain.pem ]] || {
  echo "Missing wildcard TLS certificate" >&2
  exit 1
}
[[ -f /opt/odo/nginx/odo.locations.conf && -f /etc/nginx/snippets/growy-locations.conf ]] || {
  echo "Missing ODO or Growy Nginx locations" >&2
  exit 1
}

if cmp -s "$SOURCE" "$LIVE"; then
  echo "Tenant Workspace wildcard configuration is already active"
  nginx -t
  exit 0
fi

install -d -m 0700 "$BACKUP_DIR"
BACKUP="$(mktemp "$BACKUP_DIR/growy-tenant-wildcard.$(date -u +%Y%m%dT%H%M%SZ).XXXXXX.conf")"
cp -p "$LIVE" "$BACKUP"
echo "Previous configuration saved to $BACKUP"

restore_previous() {
  echo "Restoring previous wildcard configuration" >&2
  cp -p "$BACKUP" "$LIVE"
  nginx -t && systemctl reload nginx
}

rollback_needed=false
trap 'if [[ "$rollback_needed" == true ]]; then restore_previous; fi' EXIT
install -m 0644 "$SOURCE" "$LIVE"
rollback_needed=true
nginx -t
systemctl reload nginx
rollback_needed=false

echo "Tenant Workspace wildcard routing is active"
