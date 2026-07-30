# syntax=docker/dockerfile:1.7

ARG NODE_VERSION=22-bookworm-slim
ARG GO_VERSION=1.26-bookworm

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION} AS frontend-builder
WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY index.html tsconfig.json tsconfig.app.json vite.config.ts ./
COPY public ./public
COPY src ./src
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS api-builder
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH
ARG DATABRIDGE_VERSION=1.0.0
ARG DATABRIDGE_LICENSE_PUBLIC_KEY

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN test -n "${DATABRIDGE_LICENSE_PUBLIC_KEY}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags="-s -w -X main.version=${DATABRIDGE_VERSION} -X main.licensePublicKey=${DATABRIDGE_LICENSE_PUBLIC_KEY}" \
      -o /out/databridge-api ./cmd/api

FROM debian:bookworm-slim AS runtime

ARG TARGETARCH
ARG MONGO_TOOLS_VERSION=100.17.0
ARG MONGO_TOOLS_DISTRO=ubuntu2204
ARG ETCD_VERSION=3.5.21
ARG MC_VERSION=RELEASE.2025-08-13T08-35-41Z
ENV DATABRIDGE_ADDR=:8081 \
    DATABRIDGE_DB_PATH=/data/databridge.db \
    DATABRIDGE_KEY_PATH=/data/databridge.key \
    DATABRIDGE_LICENSE_DIR=/data/license \
    DATABRIDGE_BACKUP_WORK_DIR=/data/tmp \
    TZ=Asia/Shanghai \
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
    curl -fsSL https://packages.adoptium.net/artifactory/api/gpg/key/public | gpg --dearmor -o /etc/apt/keyrings/adoptium.gpg; \
    curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /etc/apt/keyrings/postgresql.gpg; \
    curl -fsSL https://debian.neo4j.com/neotechnology.gpg.key | gpg --dearmor -o /etc/apt/keyrings/neotechnology.gpg; \
    chmod a+r /etc/apt/keyrings/adoptium.gpg /etc/apt/keyrings/postgresql.gpg /etc/apt/keyrings/neotechnology.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/adoptium.gpg] https://packages.adoptium.net/artifactory/deb bookworm main" > /etc/apt/sources.list.d/adoptium.list; \
    echo "deb [signed-by=/etc/apt/keyrings/postgresql.gpg] http://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list; \
    echo "deb [signed-by=/etc/apt/keyrings/neotechnology.gpg] https://debian.neo4j.com stable latest" > /etc/apt/sources.list.d/neo4j.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      cypher-shell \
      default-mysql-client \
      postgresql-client-13 \
      postgresql-client-14 \
      postgresql-client-15 \
      postgresql-client-16 \
      postgresql-client-17 \
      postgresql-client-18 \
      temurin-21-jre \
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

RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) etcd_arch="amd64" ;; \
      arm64) etcd_arch="arm64" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/etcd-io/etcd/releases/download/v${ETCD_VERSION}/etcd-v${ETCD_VERSION}-linux-${etcd_arch}.tar.gz" -o /tmp/etcd.tgz; \
    mkdir -p /tmp/etcd; \
    tar -xzf /tmp/etcd.tgz -C /tmp/etcd --strip-components=1; \
    install -m 0755 /tmp/etcd/etcdctl /usr/local/bin/etcdctl; \
    rm -rf /tmp/etcd /tmp/etcd.tgz

RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) mc_arch="amd64" ;; \
      arm64) mc_arch="arm64" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mc_name="mc.${MC_VERSION}"; \
    mc_base="https://dl.min.io/client/mc/release/linux-${mc_arch}/archive"; \
    curl -fsSL "${mc_base}/${mc_name}" -o "/tmp/${mc_name}"; \
    curl -fsSL "${mc_base}/${mc_name}.sha256sum" -o "/tmp/${mc_name}.sha256sum"; \
    cd /tmp; \
    sha256sum -c "${mc_name}.sha256sum"; \
    install -m 0755 "/tmp/${mc_name}" /usr/local/bin/mc; \
    rm -f "/tmp/${mc_name}" "/tmp/${mc_name}.sha256sum"

WORKDIR /app
RUN mkdir -p /data && useradd --system --uid 10001 --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin databridge && chown -R databridge:databridge /data /app

COPY --from=api-builder /out/databridge-api /usr/local/bin/databridge-api
COPY --from=frontend-builder /src/dist ./dist

USER databridge
EXPOSE 8081
VOLUME ["/data"]

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/databridge-api"]
