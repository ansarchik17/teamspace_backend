#!/usr/bin/env bash
set -Eeuo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
python3 "$SCRIPT_DIR/prepare_workspace_ai_credentials_test.py"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "$TEST_ROOT"' EXIT
export WORKSPACE_ROOT="$TEST_ROOT"
mkdir -p "$TEST_ROOT/releases"
mkdir -p "$TEST_ROOT/scripts"
cp "$SCRIPT_DIR/connect-ai-services.sh" "$TEST_ROOT/scripts/"
cat > "$TEST_ROOT/scripts/prepare-workspace-ai-credentials.sh" <<'SH'
#!/usr/bin/env bash
printf 'prepare-workspace-ai-credentials\n' >> "$TRACE_FILE"
SH
touch "$TEST_ROOT/.env" "$TEST_ROOT/compose.prod.yml"
printf 'BACKEND_IMAGE=example/backend:new\n' > "$TEST_ROOT/releases/images.env"
printf 'BACKEND_IMAGE=example/backend:old\n' > "$TEST_ROOT/releases/images.env.previous"
export TRACE_FILE="$TEST_ROOT/trace"
export FAIL_MIGRATION=0 CACHE_MISSING=0
docker() {
  printf '%s\n' "$*" >> "$TRACE_FILE"
  if [[ "$*" == 'image inspect '* ]]; then return "$CACHE_MISSING"; fi
  if [[ "$*" == *'run --rm --no-deps -T --entrypoint /app/migrate api'* ]]; then return "$FAIL_MIGRATION"; fi
  return 0
}
curl() { return 0; }
export -f docker curl
bash "$SCRIPT_DIR/connect-ai-services_test.sh"

export FAIL_MIGRATION=1
if bash "$SCRIPT_DIR/deploy.sh" backend; then echo 'migration failure must abort' >&2; exit 1; fi
if grep -q ' up ' "$TRACE_FILE"; then echo 'replaced API despite failed migration' >&2; exit 1; fi
if grep -q 'prepare-workspace-ai-credentials' "$TRACE_FILE"; then echo 'changed credentials despite failed migration' >&2; exit 1; fi

export FAIL_MIGRATION=0
: > "$TRACE_FILE"
bash "$SCRIPT_DIR/deploy.sh" backend
grep -q 'run --rm --no-deps -T --entrypoint /app/migrate api' "$TRACE_FILE"
grep -q ' up ' "$TRACE_FILE"
grep -q 'prepare-workspace-ai-credentials' "$TRACE_FILE"

: > "$TRACE_FILE"
bash "$SCRIPT_DIR/deploy.sh" backend rollback
grep -q 'image inspect example/backend:old' "$TRACE_FILE"
grep -q 'up -d --remove-orphans --pull never' "$TRACE_FILE"
if grep -q ' pull ' "$TRACE_FILE"; then echo 'rollback must not require registry' >&2; exit 1; fi

export CACHE_MISSING=1
: > "$TRACE_FILE"
if bash "$SCRIPT_DIR/deploy.sh" backend rollback; then echo 'missing rollback image must fail' >&2; exit 1; fi
if grep -q ' up ' "$TRACE_FILE"; then echo 'started missing rollback image' >&2; exit 1; fi
echo 'deployment script regression tests passed'
