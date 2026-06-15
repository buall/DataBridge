# syntax=docker/dockerfile:1.7

ARG NODE_VERSION=22-bookworm-slim
ARG GO_VERSION=1.26-bookworm

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION} AS frontend-builder
WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY index.html tsconfig.json tsconfig.app.json vite.config.ts ./
COPY src ./src
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS api-builder
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/databridge-api ./cmd/api

FROM debian:bookworm-slim AS runtime

ARG TARGETARCH
ARG MONGO_TOOLS_VERSION=100.17.0
ARG MONGO_TOOLS_DISTRO=ubuntu2204
ARG PG_MAJOR=18

ENV DATABRIDGE_ADDR=:8081 \
    DATABRIDGE_DB_PATH=/data/databridge.db \
    DATABRIDGE_KEY_PATH=/data/databridge.key \
    DATABRIDGE_PG_JOBS=4

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      gnupg \
      lsb-release \
      xz-utils; \
    install -d -m 0755 /etc/apt/keyrings; \
    curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /etc/apt/keyrings/postgresql.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/postgresql.gpg] http://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      default-mysql-client \
      postgresql-client-${PG_MAJOR} \
      tini \
      xz-utils; \
    apt-get purge -y --auto-remove gnupg lsb-release; \
    rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) mongo_arch="x86_64" ;; \
      arm64) mongo_arch="arm64" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://fastdl.mongodb.org/tools/db/mongodb-database-tools-${MONGO_TOOLS_DISTRO}-${mongo_arch}-${MONGO_TOOLS_VERSION}.tgz" -o /tmp/mongodb-tools.tgz; \
    mkdir -p /tmp/mongodb-tools; \
    tar -xzf /tmp/mongodb-tools.tgz -C /tmp/mongodb-tools --strip-components=1; \
    install -m 0755 /tmp/mongodb-tools/bin/mongoexport /usr/local/bin/mongoexport; \
    install -m 0755 /tmp/mongodb-tools/bin/mongoimport /usr/local/bin/mongoimport; \
    install -m 0755 /tmp/mongodb-tools/bin/mongodump /usr/local/bin/mongodump; \
    install -m 0755 /tmp/mongodb-tools/bin/mongorestore /usr/local/bin/mongorestore; \
    install -m 0755 /tmp/mongodb-tools/bin/mongostat /usr/local/bin/mongostat; \
    install -m 0755 /tmp/mongodb-tools/bin/mongotop /usr/local/bin/mongotop; \
    rm -rf /tmp/mongodb-tools /tmp/mongodb-tools.tgz

WORKDIR /app
RUN mkdir -p /data && useradd --system --uid 10001 --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin databridge && chown -R databridge:databridge /data /app

COPY --from=api-builder /out/databridge-api /usr/local/bin/databridge-api
COPY --from=frontend-builder /src/dist ./dist

USER databridge
EXPOSE 8081
VOLUME ["/data"]

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/databridge-api"]
