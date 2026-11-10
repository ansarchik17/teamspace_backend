ARG GO_BUILDER_IMAGE=golang:1.24.7-alpine3.22
ARG RUNTIME_IMAGE=ubuntu:24.04
FROM ${GO_BUILDER_IMAGE} AS builder

# Workflow environment is not inherited by Docker build steps.
# Keep compiler working sets within the bounded BuildKit container.
ENV GOMAXPROCS=1 GOFLAGS=-p=1 GOMEMLIMIT=640MiB

RUN apk add --no-cache git ca-certificates tzdata
WORKDIR /app

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    for attempt in 1 2 3 4 5; do \
      if go mod download; then exit 0; fi; \
      if [ "$attempt" -lt 5 ]; then sleep "$((attempt * 10))"; fi; \
    done; \
    exit 1

COPY . .

ARG VERSION=dev
ARG BUILD_TIME=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags="-w -s -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
      -o /app/bin/workspace ./cmd/api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags="-w -s" \
      -o /app/bin/user-merge ./cmd/user-merge

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags="-w -s" \
      -o /app/bin/migrate ./cmd/migrate

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-w -s" \
      -o /app/bin/ai-accounting ./cmd/ai-accounting

FROM ${RUNTIME_IMAGE}

COPY docker/install-runtime.sh /tmp/install-runtime.sh
RUN sed -i 's/\r$//' /tmp/install-runtime.sh && sh /tmp/install-runtime.sh && rm /tmp/install-runtime.sh
ENV HOME=/home/app

WORKDIR /app

COPY --from=builder /app/bin/workspace /app/workspace
COPY --from=builder /app/bin/user-merge /app/user-merge
COPY --from=builder /app/bin/migrate /app/migrate
COPY --from=builder /app/bin/ai-accounting /app/ai-accounting
COPY --from=builder /app/db/migrations /app/db/migrations
COPY docker/entrypoint.sh /app/entrypoint.sh

RUN sed -i 's/\r$//' /app/entrypoint.sh && chmod +x /app/entrypoint.sh

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
