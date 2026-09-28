# syntax=docker/dockerfile:1.7
#
# One image holding the Riochat server and the statically exported web client.
#
#   docker build -t riochat:local .
#   docker build --build-arg GIT_COMMIT=$(git rev-parse HEAD) -t riochat:local .
#
# Build arguments
#   NEXT_PUBLIC_API_BASE_URL   API origin baked into the web client; empty means
#                              "same origin as the page" (the normal setup).
#   GIT_COMMIT                 commit id embedded in the server's build info.
#   BUILD_TIME                 build timestamp; empty uses the current UTC time
#                              as YYYY-MM-DDTHH:MM:SSZ.
#   REFRESH_CATALOG_SNAPSHOTS  "true" refreshes the bundled model catalog from
#                              upstream before compiling. A failed refresh keeps
#                              the committed data and never fails the build.

ARG NODE_VERSION=24
ARG GO_VERSION=1.26.8
ARG PNPM_VERSION=11.22.0

# ---- web client ----------------------------------------------------------------
FROM node:${NODE_VERSION}-bookworm-slim AS web
ARG PNPM_VERSION
ENV npm_config_store_dir=/pnpm-store \
    NEXT_TELEMETRY_DISABLED=1 \
    CI=true
RUN npm install --global "pnpm@${PNPM_VERSION}"
WORKDIR /src

# Dependency layer: only manifests and the lockfile, so source edits do not
# invalidate it. The frozen lockfile makes a stale lockfile fail the build.
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY web/package.json web/package.json
RUN --mount=type=cache,id=riochat-pnpm-store,target=/pnpm-store \
    pnpm install --frozen-lockfile --prefer-offline --filter @riochat/web...

COPY web/ web/
ARG NEXT_PUBLIC_API_BASE_URL=""
ENV NEXT_PUBLIC_API_BASE_URL=${NEXT_PUBLIC_API_BASE_URL}
RUN --mount=type=cache,id=riochat-next-cache,target=/src/web/.next/cache \
    pnpm --filter @riochat/web build

# ---- server --------------------------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS server
WORKDIR /src/backend

# Module download is cached separately from the source copy. The SQLite driver
# compiles its C amalgamation, so cgo (and the gcc in this image) is required.
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,id=riochat-go-mod,target=/go/pkg/mod \
    go mod download

COPY VERSION /src/VERSION
COPY backend/ ./

ARG GIT_COMMIT=unknown
ARG BUILD_TIME=""
ARG REFRESH_CATALOG_SNAPSHOTS=false
RUN --mount=type=cache,id=riochat-go-mod,target=/go/pkg/mod \
    --mount=type=cache,id=riochat-go-build,target=/root/.cache/go-build \
    set -eu; \
    if [ "${REFRESH_CATALOG_SNAPSHOTS}" = "true" ]; then \
      go run ./cmd/catalog-snapshot \
        || echo "warning: catalog refresh failed; keeping the committed snapshot"; \
    fi; \
    version="$(tr -d '[:space:]' < /src/VERSION)"; \
    built="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"; \
    pkg=github.com/riochat/riochat/backend/internal/platform/buildinfo; \
    CGO_ENABLED=1 go build -trimpath \
      -ldflags "-s -w -X ${pkg}.Version=${version} -X ${pkg}.Commit=${GIT_COMMIT} -X ${pkg}.BuildTime=${built}" \
      -o /out/riochat ./cmd/riochat

# ---- runtime dependencies (certificates and time zones only) --------------------
FROM debian:bookworm-slim AS runtime-deps
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

# ---- final image ---------------------------------------------------------------
FROM debian:bookworm-slim
WORKDIR /app

COPY --from=runtime-deps /etc/ssl/certs /etc/ssl/certs
COPY --from=runtime-deps /usr/share/ca-certificates /usr/share/ca-certificates
COPY --from=runtime-deps /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=runtime-deps /etc/localtime /etc/localtime
COPY --from=runtime-deps /etc/timezone /etc/timezone

# The server runs unprivileged. The two data directories exist and are owned by
# that user before they are declared as volumes, so fresh named volumes inherit
# the right ownership.
RUN groupadd --system --gid 10001 riochat \
    && useradd --system --uid 10001 --gid riochat --home-dir /app --no-create-home --shell /usr/sbin/nologin riochat \
    && mkdir -p /app/storage /app/data /app/frontend /app/licenses/Riochat \
    && chown riochat:riochat /app/storage /app/data

COPY --from=server /out/riochat /app/riochat
COPY --from=web /src/web/out /app/frontend/out
COPY NOTICE /app/licenses/Riochat/NOTICE

# Existing configs and environment overrides depend on this exact path.
ENV FRONTEND_DIST_DIR=/app/frontend/out

EXPOSE 8080
VOLUME ["/app/storage", "/app/data"]
USER riochat

# /app/config.yaml is read automatically when it exists (mount it read-only).
CMD ["/app/riochat"]
