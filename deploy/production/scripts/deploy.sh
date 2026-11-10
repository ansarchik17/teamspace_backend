#!/usr/bin/env bash
set -Eeuo pipefail

SERVICE="${1:-}"
MODE="${2:-deploy}"
ROOT="${WORKSPACE_ROOT:-/opt/workspace}"
COMPOSE_FILE="$ROOT/compose.prod.yml"
ENV_FILE="$ROOT/.env"
IMAGE_FILE="$ROOT/releases/images.env"

case "$SERVICE" in
  backend)
    # The API only handles HTTP requests in production. Document ingestion is
    # deliberately processed by the durable worker service, so both services
    # must be released together. Keeping the worker out of this list leaves
    # uploaded documents stuck in `uploaded`/`queued` forever because the API
    # is configured with AI_DOCUMENT_WORKER_DISABLED=true.
    COMPOSE_SERVICES=(api ai-document-worker keycloak-postgres workspace-keycloak)
    HEALTH_URL="http://127.0.0.1:18081/health"
    ;;
  frontend)
    COMPOSE_SERVICES=(frontend)
    HEALTH_URL="http://127.0.0.1:13000/health"
    ;;
  *) echo "Usage: deploy.sh backend|frontend [deploy|rollback]" >&2; exit 2 ;;
esac

[[ "$MODE" == "deploy" || "$MODE" == "rollback" ]] || { echo "Invalid deployment mode" >&2; exit 2; }

SHARED_ENV_ARGS=()
ORPHAN_ARGS=(--remove-orphans)
SHARED_INFRA_ACTIVE=false
if [[ -f "$ROOT/.shared-infra.env" ]]; then
  source "$(dirname -- "${BASH_SOURCE[0]}")/shared-mode.sh"
  shared_infra_activate "$ROOT" "$COMPOSE_FILE" "workspace"
fi

[[ -f "$COMPOSE_FILE" ]] || { echo "Missing $COMPOSE_FILE" >&2; exit 1; }
[[ -f "$ENV_FILE" ]] || { echo "Missing $ENV_FILE" >&2; exit 1; }
[[ -f "$IMAGE_FILE" ]] || { echo "Missing $IMAGE_FILE" >&2; exit 1; }

exec 9>"$ROOT/releases/.deploy.lock"
flock 9

if [[ "$MODE" == "rollback" ]]; then
  [[ -f "$IMAGE_FILE.previous" ]] || { echo "No previous release" >&2; exit 1; }
  IMAGE_KEY="BACKEND_IMAGE"
  [[ "$SERVICE" != "frontend" ]] || IMAGE_KEY="FRONTEND_IMAGE"
  previous_image="$(sed -n "s/^${IMAGE_KEY}=//p" "$IMAGE_FILE.previous")"
  [[ -n "$previous_image" ]] || { echo "No previous $SERVICE image" >&2; exit 1; }
  # The last working image is already on the host. A registry outage or expired
  # job token must not prevent recovery. Do not guess a different image/tag.
  docker image inspect "$previous_image" >/dev/null || {
    echo "Previous image is not cached: $previous_image; restore registry access before rollback" >&2
    exit 1
  }
  cp -f "$IMAGE_FILE.previous" "$IMAGE_FILE"
fi

docker compose --env-file "$ENV_FILE" --env-file "$IMAGE_FILE" ${SHARED_ENV_ARGS[@]+"${SHARED_ENV_ARGS[@]}"} \
  -f "$COMPOSE_FILE" config >/dev/null

pull_images() {
  local attempt delay
  delay=10

  for attempt in 1 2 3 4 5; do
    if docker compose --env-file "$ENV_FILE" --env-file "$IMAGE_FILE" ${SHARED_ENV_ARGS[@]+"${SHARED_ENV_ARGS[@]}"} \
      -f "$COMPOSE_FILE" pull "${COMPOSE_SERVICES[@]}"; then
      return 0
    fi

    if [[ "$attempt" -lt 5 ]]; then
      echo "Image pull failed (${attempt}/5); retrying in ${delay}s..." >&2
      sleep "$delay"
      delay=$((delay * 2))
    fi
  done

  echo "Unable to pull deployment images after 5 attempts" >&2
  return 1
}

UP_PULL_POLICY=()
if [[ "$MODE" == "rollback" ]]; then
  UP_PULL_POLICY=(--pull never)
else
  pull_images
fi

if [[ "$SERVICE" == "backend" && "$MODE" == "deploy" ]]; then
  # Validate/apply the candidate migrations while the old API remains running.
  # Do not replace working services when the migration runner rejects an upgrade.
  docker compose --env-file "$ENV_FILE" --env-file "$IMAGE_FILE" ${SHARED_ENV_ARGS[@]+"${SHARED_ENV_ARGS[@]}"} \
    -f "$COMPOSE_FILE" run --rm --no-deps -T --entrypoint /app/migrate api
  bash "$ROOT/scripts/prepare-workspace-ai-credentials.sh" "$ENV_FILE"
  bash "$ROOT/scripts/connect-ai-services.sh"
fi

docker compose --env-file "$ENV_FILE" --env-file "$IMAGE_FILE" ${SHARED_ENV_ARGS[@]+"${SHARED_ENV_ARGS[@]}"} \
  -f "$COMPOSE_FILE" up -d ${ORPHAN_ARGS[@]+"${ORPHAN_ARGS[@]}"} ${UP_PULL_POLICY[@]+"${UP_PULL_POLICY[@]}"} "${COMPOSE_SERVICES[@]}"

for attempt in $(seq 1 45); do
  if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
    echo "Healthcheck passed: $HEALTH_URL"
    docker compose --env-file "$ENV_FILE" --env-file "$IMAGE_FILE" ${SHARED_ENV_ARGS[@]+"${SHARED_ENV_ARGS[@]}"} \
      -f "$COMPOSE_FILE" ps
    exit 0
  fi
  echo "Waiting for healthcheck ${attempt}/45..."
  sleep 2
done

echo "Healthcheck failed: $HEALTH_URL" >&2
docker compose --env-file "$ENV_FILE" --env-file "$IMAGE_FILE" ${SHARED_ENV_ARGS[@]+"${SHARED_ENV_ARGS[@]}"} \
  -f "$COMPOSE_FILE" ps >&2 || true
# Include the API and worker logs in the CI output. Without this, a restart
# loop is reduced to an unhelpful healthcheck timeout and the actual startup
# error remains invisible in GitHub Actions.
docker compose --env-file "$ENV_FILE" --env-file "$IMAGE_FILE" ${SHARED_ENV_ARGS[@]+"${SHARED_ENV_ARGS[@]}"} \
  -f "$COMPOSE_FILE" logs --no-color --tail=200 api ai-document-worker >&2 || true
exit 1
