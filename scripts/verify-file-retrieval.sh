#!/usr/bin/env bash
# Isolated verification: never connects to the production/shared PostgreSQL.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# Fail before registering cleanup or creating resources when tooling is unavailable.
for tool in docker go python3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "Required tool is unavailable: $tool" >&2
    exit 1
  fi
done
if ! docker info >/dev/null 2>&1; then
  echo "Docker daemon is unavailable. Start Docker and rerun this script." >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "Docker Compose v2 is required." >&2
  exit 1
fi
PROJECT="workspace-retrieval-test-$$"
COMPOSE=(docker compose --project-name "$PROJECT" -f "$ROOT/deploy/production/compose.retrieval-test.yml")
cleanup() { "${COMPOSE[@]}" down --volumes --remove-orphans >/dev/null; }
trap cleanup EXIT
"${COMPOSE[@]}" up -d --wait --wait-timeout 60
"${COMPOSE[@]}" exec -T db psql -v ON_ERROR_STOP=1 -U retrieval_test -d retrieval_test -c 'CREATE EXTENSION IF NOT EXISTS vector;'
ADDRESS="$("${COMPOSE[@]}" port db 5432)"
PORT="${ADDRESS##*:}"
unset TEST_MESSENGER_DATABASE_URL TEST_NOTES_DATABASE_URL IFORM_TEST_DATABASE_URL TEAM_AI_LIVE_EVAL
export TEST_DOCUMENT_SEARCH_DATABASE_URL="postgres://retrieval_test:local-test-only@127.0.0.1:$PORT/retrieval_test?sslmode=disable"
export GOCACHE="${GOCACHE:-$ROOT/.tmp-gocache}"
cd "$ROOT"
go test -p 1 ./internal/documentsearch ./internal/repository ./internal/service ./internal/handler ./internal/config ./cmd/api
python3 -m unittest discover -s deploy/production/reranker -p test_server.py -v
