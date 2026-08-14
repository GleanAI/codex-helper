# syntax=docker/dockerfile:1.7
FROM node:24.19.0-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json frontend/tsconfig.json frontend/vite.config.ts ./
COPY frontend/src ./src
COPY frontend/scripts ./scripts
COPY frontend/index.html ./index.html
RUN npm ci && npm run build

FROM golang:1.26.0-bookworm AS backend
ARG APP_VERSION=0.2.0
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/cmd ./cmd
COPY backend/internal ./internal
COPY --from=frontend /src/backend/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X codex-helper/internal/app.Version=${APP_VERSION}" -o /out/codex-helper ./cmd/server

FROM node:24.19.0-bookworm-slim AS codex
ARG CODEX_VERSION=latest
ADD https://registry.npmjs.org/@openai%2Fcodex/${CODEX_VERSION} /tmp/codex-package.json
RUN RESOLVED_CODEX_VERSION="$(node -p "require('/tmp/codex-package.json').version")" \
 && echo "Installing @openai/codex@${RESOLVED_CODEX_VERSION}" \
 && npm install -g "@openai/codex@${RESOLVED_CODEX_VERSION}" \
 && rm /tmp/codex-package.json \
 && npm cache clean --force

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --create-home helper && mkdir -p /data && chown helper:helper /data
COPY --from=backend /out/codex-helper /usr/local/bin/codex-helper
COPY --from=codex /usr/local/bin/node /usr/local/bin/node
COPY --from=codex /usr/local/lib/node_modules/@openai/codex /usr/local/lib/node_modules/@openai/codex
RUN ln -s /usr/local/lib/node_modules/@openai/codex/bin/codex.js /usr/local/bin/codex
USER helper
ENV DATA_DIR=/data LISTEN_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/usr/local/bin/codex-helper","healthcheck"]
ENTRYPOINT ["/usr/local/bin/codex-helper"]
