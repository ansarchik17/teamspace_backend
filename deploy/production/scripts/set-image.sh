#!/usr/bin/env bash
set -Eeuo pipefail

SERVICE="${1:-}"
IMAGE="${2:-}"
IMAGE_FILE="/opt/workspace/releases/images.env"
LOCK_FILE="/opt/workspace/releases/.images.lock"

case "$SERVICE" in
  backend) KEY="BACKEND_IMAGE" ;;
  frontend) KEY="FRONTEND_IMAGE" ;;
  *) echo "Usage: set-image.sh backend|frontend IMAGE" >&2; exit 2 ;;
esac

[[ "$IMAGE" =~ ^[a-zA-Z0-9./:_-]+$ ]] || {
  echo "Invalid image reference" >&2
  exit 2
}

install -d -m 0750 /opt/workspace/releases
touch "$IMAGE_FILE"

exec 9>"$LOCK_FILE"
flock 9

cp -f "$IMAGE_FILE" "$IMAGE_FILE.previous"

if grep -q "^${KEY}=" "$IMAGE_FILE"; then
  sed -i "s#^${KEY}=.*#${KEY}=${IMAGE}#" "$IMAGE_FILE"
else
  printf '%s=%s\n' "$KEY" "$IMAGE" >> "$IMAGE_FILE"
fi

chmod 0640 "$IMAGE_FILE" "$IMAGE_FILE.previous"
echo "Selected ${KEY}=${IMAGE}"
