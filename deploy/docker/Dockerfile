# OpsNap 镜像：单二进制（内嵌前端与 kopia）+ 预装的多版本导出工具（spec「Docker 镜像」）。
# 在仓库根目录构建：
#   docker build -f deploy/docker/Dockerfile --build-arg VERSION=... --build-arg COMMIT=... -t opsnap:local .
# 或 make docker-build。支持 linux/amd64 与 linux/arm64，CI 在各自架构的 runner 上原生构建。
#
# 工具按 spec「目录约定」放在 /opt/opsnap/tools：
#   mysql-8.0、mysql-8.4、mysql-9.x  MySQL 官方二进制包中的 mysqldump（版本见下方 MYSQL_*_VERSION）
#   postgresql-14 … postgresql-18    PostgreSQL 官方 apt 源（PGDG）的 pg_dump、pg_dumpall
#   mariadb                          Debian 的 MariaDB 客户端 mariadb-dump
#
# 镜像里没有任何秘密：主密钥与元数据库在第一次启动时生成在挂载的 /data 中。

# 构建时最新的 MySQL 9.x 为 9.7（LTS）；升级补丁版本只改这里，包的签名在构建时校验
ARG MYSQL_80_VERSION=8.0.46
ARG MYSQL_84_VERSION=8.4.11
ARG MYSQL_9_VERSION=9.7.2
ARG PG_VERSIONS="14 15 16 17 18"
ARG DEBIAN_IMAGE=debian:trixie-slim

# ── 前端：与架构无关，在构建机的架构上执行 ───────────────────────────────
FROM --platform=$BUILDPLATFORM node:22-trixie-slim AS web
# corepack 按当前目录的 package.json（packageManager 字段）选用 pnpm 版本，所以在 frontend/ 下执行
WORKDIR /src/frontend
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY frontend/ ./
# 构建产物写到 ../internal/web/dist（vite.config.ts）
RUN mkdir -p ../internal/web/dist && pnpm build

# ── 后端：在构建机的架构上交叉编译到目标架构（CGO_ENABLED=0）───────────────
FROM --platform=$BUILDPLATFORM golang:1.26-trixie AS server
ARG TARGETOS
ARG TARGETARCH
ARG GOPROXY
# 版本号与提交短号经 Makefile 的 LDFLAGS 写入二进制，健康接口返回二者
ARG VERSION=dev
ARG COMMIT=
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}" go mod download
COPY . .
COPY --from=web /src/internal/web/dist/ internal/web/dist/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out \
    && GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}" GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    make build-server VERSION="$VERSION" COMMIT="$COMMIT" BIN=/out/opsnap

# ── MySQL 官方 mysqldump：每个版本一个阶段，并行下载 ────────────────────
FROM ${DEBIAN_IMAGE} AS mysql-base
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl xz-utils gnupg binutils \
    && rm -rf /var/lib/apt/lists/*
COPY --chmod=0755 deploy/docker/install-mysqldump.sh /usr/local/bin/install-mysqldump
COPY deploy/docker/keys/mysql-release.asc /usr/share/keyrings/mysql-release.asc

FROM mysql-base AS mysql-80
ARG MYSQL_80_VERSION
RUN install-mysqldump "$MYSQL_80_VERSION" /opt/opsnap/tools /usr/share/keyrings/mysql-release.asc

FROM mysql-base AS mysql-84
ARG MYSQL_84_VERSION
RUN install-mysqldump "$MYSQL_84_VERSION" /opt/opsnap/tools /usr/share/keyrings/mysql-release.asc

FROM mysql-base AS mysql-9
ARG MYSQL_9_VERSION
RUN install-mysqldump "$MYSQL_9_VERSION" /opt/opsnap/tools /usr/share/keyrings/mysql-release.asc

# ── 运行镜像 ─────────────────────────────────────────────────────────
FROM ${DEBIAN_IMAGE}
ARG PG_VERSIONS
ARG VERSION=dev
ARG COMMIT=
ARG DEBIAN_FRONTEND=noninteractive
LABEL org.opencontainers.image.source="https://github.com/opskat/opsnap" \
      org.opencontainers.image.title="OpsNap" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"

# ca-certificates：S3 与 OIDC 都走 HTTPS；tzdata：系统时区数据；curl：健康检查
# PostgreSQL 客户端来自 PGDG（公钥随仓库固定在 deploy/docker/keys），MariaDB 客户端来自 Debian
COPY deploy/docker/keys/pgdg.asc /usr/share/keyrings/pgdg.asc
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && . /etc/os-release \
    && echo "deb [signed-by=/usr/share/keyrings/pgdg.asc] https://apt.postgresql.org/pub/repos/apt ${VERSION_CODENAME}-pgdg main" \
       > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends mariadb-client \
       $(for v in $PG_VERSIONS; do printf 'postgresql-client-%s ' "$v"; done) \
    && rm -rf /var/lib/apt/lists/*

# 按目录约定链接到各客户端包的实际位置
RUN set -e; \
    for v in $PG_VERSIONS; do \
      mkdir -p "/opt/opsnap/tools/postgresql-$v/bin"; \
      for t in pg_dump pg_dumpall; do ln -s "/usr/lib/postgresql/$v/bin/$t" "/opt/opsnap/tools/postgresql-$v/bin/$t"; done; \
    done; \
    mkdir -p /opt/opsnap/tools/mariadb/bin; \
    ln -s /usr/bin/mariadb-dump /opt/opsnap/tools/mariadb/bin/mariadb-dump
COPY --from=mysql-80 /opt/opsnap/tools/ /opt/opsnap/tools/
COPY --from=mysql-84 /opt/opsnap/tools/ /opt/opsnap/tools/
COPY --from=mysql-9 /opt/opsnap/tools/ /opt/opsnap/tools/
# 每个工具都要能在这个镜像里运行（缺共享库时构建即失败）
RUN for f in /opt/opsnap/tools/*/bin/*; do "$f" --version || exit 1; done

COPY --from=server /out/opsnap /usr/local/bin/opsnap
COPY deploy/docker/config.yaml /opt/opsnap/configs/config.yaml

# 工作目录下的 configs/config.yaml 即默认配置，opsnap 与 opsnap admin 子命令不带 -c 也能找到
WORKDIR /opt/opsnap
# 元数据库、master.key、日志、kopia 缓存都在这里，部署时挂载为卷
VOLUME ["/data"]
EXPOSE 8210
# 以 root 运行（spec「Docker 镜像」→「运行」）：“本地目录”存储要写入用户挂入的宿主机目录
USER root
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --start-interval=2s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8210/api/v1/system/health | grep -q '"database":"ok"' || exit 1
ENTRYPOINT ["opsnap"]
