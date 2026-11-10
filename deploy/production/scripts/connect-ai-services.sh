#!/usr/bin/env bash
set -Eeuo pipefail

# Attach only running application backends. Databases stay on their private
# networks. Compose declarations in each project preserve this after recreation.
NETWORK="${1:-workspace_workspace}"
docker network inspect "$NETWORK" >/dev/null
for container in odo-bff sd-backend axis-mail-app-app-1 growy-api-1 iform-backend-1; do
  if ! docker container inspect "$container" >/dev/null 2>&1; then
    echo "AI service container is not installed: $container" >&2
    continue
  fi
  running="$(docker container inspect --format '{{.State.Running}}' "$container")"
  if [[ "$running" != "true" ]]; then
    echo "AI service container is stopped: $container" >&2
    continue
  fi
  attached="$(docker container inspect --format "{{with index .NetworkSettings.Networks \"$NETWORK\"}}{{.NetworkID}}{{end}}" "$container")"
  if [[ -z "$attached" ]]; then
    docker network connect "$NETWORK" "$container"
    echo "Connected $container to $NETWORK"
  fi
done
