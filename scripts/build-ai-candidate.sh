#!/usr/bin/env bash
# Run only from the extracted, checksummed release bundle. No deploy or push.
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"
VERSION=ai-observe-20261020-01
sha256sum --quiet -c SOURCE_SHA256SUMS
for container in workspace-api workspace-ai-document-worker workspace-frontend axis-mail-app-app-1; do
  if docker inspect "$container" >/dev/null 2>&1; then
    active="$(docker inspect --format '{{.Config.Image}}' "$container")"
    [[ "$active" != *":$VERSION" ]] || { echo 'Candidate is already selected by a container; do not rebuild its tag' >&2; exit 1; }
  fi
done
docker build --tag "workspace-backend:$VERSION" --build-arg "VERSION=$VERSION" \
  --file backend/Dockerfile backend
docker build --tag "workspace-front:$VERSION" --build-arg "VERSION=$VERSION" \
  --build-arg VITE_TENANT_WORKSPACE_ENABLED=true --file front/Dockerfile front
docker build --tag "workspace-front-mail:$VERSION" \
  --file front/deploy/mail/source/Dockerfile front/deploy/mail/source
docker image inspect --format '{{.Id}} {{join .RepoTags ","}}' \
  "workspace-backend:$VERSION" "workspace-front:$VERSION" "workspace-front-mail:$VERSION" \
  > CANDIDATE_IMAGES.txt
cat CANDIDATE_IMAGES.txt
echo 'Candidate images built locally. Nothing was pushed or deployed.'
