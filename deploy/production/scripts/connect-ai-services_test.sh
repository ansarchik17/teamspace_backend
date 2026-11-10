#!/usr/bin/env bash
set -Eeuo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT
export TRACE_FILE="$TEST_ROOT/trace"
docker() {
  printf '%s\n' "$*" >> "$TRACE_FILE"
  if [[ "$*" == 'container inspect iform-backend-1' ]]; then return 1; fi
  if [[ "$*" == 'container inspect --format {{.State.Running}} growy-api-1' ]]; then echo false; return 0; fi
  if [[ "$*" == 'container inspect --format {{.State.Running}} '* ]]; then echo true; return 0; fi
  if [[ "$*" == *'.NetworkSettings.Networks'*' sd-backend' ]]; then echo network-id; fi
  return 0
}
export -f docker
bash "$SCRIPT_DIR/connect-ai-services.sh"
grep -q '^network connect workspace_workspace odo-bff$' "$TRACE_FILE"
grep -q '^network connect workspace_workspace axis-mail-app-app-1$' "$TRACE_FILE"
if grep -q '^network connect .*\(sd-backend\|growy-api-1\|iform-backend-1\|postgres\|minio\)' "$TRACE_FILE"; then
  echo 'connected an existing, stopped, missing or database container' >&2
  exit 1
fi
echo 'AI Docker network regression tests passed'
