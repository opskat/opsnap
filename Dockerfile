# opsnap 生产镜像：opsnap（内嵌面板）+ restic、rclone、rsync、openssh-client 与各数据库导出工具。
#
#   docker build -t opsnap .
#   docker run -d -p 8080:8080 -v opsnap-data:/data -e OPSNAP_SECRET_KEY=… opsnap
#
# 数据目录 /data 存放 SQLite、文件镜像和 restic 缓存；本地目录仓库（NAS 共享目录）另行挂载，
# 并保证对容器内的 opsnap 用户（uid 10001）可写。

ARG GO_VERSION=1.27
ARG NODE_VERSION=24
ARG DEBIAN_RELEASE=bookworm

# ---- 前端：frontend/ → dist ----
FROM node:${NODE_VERSION}-${DEBIAN_RELEASE}-slim AS frontend
ARG PNPM_VERSION=10.11.0
RUN npm install -g pnpm@${PNPM_VERSION}
WORKDIR /src/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

# ---- 后端：前端产物复制到 web/dist 后内嵌进二进制 ----
FROM golang:${GO_VERSION}-${DEBIAN_RELEASE} AS backend
ARG VERSION=0.1.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN find web/dist -mindepth 1 -maxdepth 1 ! -name .keep -exec rm -rf {} + 2>/dev/null; mkdir -p web/dist
COPY --from=frontend /src/frontend/dist/ web/dist/
RUN test -f web/dist/index.html \
 && CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/cago-frame/cago/configs.Version=${VERSION}" \
      -o /out/opsnap ./cmd/app

# ---- restic 与 rclone：官方发布的静态二进制，按发布方的 SHA256SUMS 校验 ----
FROM debian:${DEBIAN_RELEASE}-slim AS tools
ARG TARGETARCH
ARG RESTIC_VERSION=0.19.1
ARG RCLONE_VERSION=1.75.2
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl bzip2 unzip \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp/tools
RUN set -eux; arch="${TARGETARCH:-amd64}"; \
    f="restic_${RESTIC_VERSION}_linux_${arch}.bz2"; \
    curl -fsSLO "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/${f}"; \
    curl -fsSL -o restic.sums "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/SHA256SUMS"; \
    grep " ${f}\$" restic.sums | sha256sum -c -; \
    bunzip2 -c "${f}" > restic; \
    f="rclone-v${RCLONE_VERSION}-linux-${arch}.zip"; \
    curl -fsSLO "https://github.com/rclone/rclone/releases/download/v${RCLONE_VERSION}/${f}"; \
    curl -fsSL -o rclone.sums "https://github.com/rclone/rclone/releases/download/v${RCLONE_VERSION}/SHA256SUMS"; \
    grep " ${f}\$" rclone.sums | sha256sum -c -; \
    unzip -q "${f}"; \
    install -D -m 0755 restic /out/restic; \
    install -D -m 0755 "rclone-v${RCLONE_VERSION}-linux-${arch}/rclone" /out/rclone

# ---- 运行时 ----
FROM debian:${DEBIAN_RELEASE}-slim
ARG DEBIAN_RELEASE
ARG MYSQL_SERIES=mysql-8.4-lts
ARG MONGODB_SERIES=8.0
# 软件源：PostgreSQL 官方 PGDG（postgresql-client 跟随最新大版本）、MySQL 官方（与 MySQL 8 完全兼容的 mysqldump）、
# MongoDB 官方（mongodb-database-tools）。Debian 自带的 redis-tools 为 7.x。
# MySQL 的签名密钥 B7B3B788A8D3785C 在 RPM-GPG-KEY-mysql-2023 里已过期，RPM-GPG-KEY-mysql-2025 是同一把密钥延长了有效期。
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg; \
    install -d -m 0755 /etc/apt/keyrings; \
    curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc | gpg --dearmor -o /etc/apt/keyrings/pgdg.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/pgdg.gpg] https://apt.postgresql.org/pub/repos/apt ${DEBIAN_RELEASE}-pgdg main" > /etc/apt/sources.list.d/pgdg.list; \
    curl -fsSL https://repo.mysql.com/RPM-GPG-KEY-mysql-2025 | gpg --dearmor -o /etc/apt/keyrings/mysql.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/mysql.gpg] http://repo.mysql.com/apt/debian ${DEBIAN_RELEASE} ${MYSQL_SERIES}" > /etc/apt/sources.list.d/mysql.list; \
    curl -fsSL "https://pgp.mongodb.com/server-${MONGODB_SERIES}.asc" | gpg --dearmor -o /etc/apt/keyrings/mongodb.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/mongodb.gpg] http://repo.mongodb.org/apt/debian ${DEBIAN_RELEASE}/mongodb-org/${MONGODB_SERIES} main" > /etc/apt/sources.list.d/mongodb.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      openssh-client rsync redis-tools tzdata \
      postgresql-client mysql-community-client mongodb-database-tools; \
    # mariadb-dump：Debian 的 mariadb-client 与 mysql-community-client 互斥，只取出 mariadb-dump 这一个二进制
    # （它只依赖 libc、libssl、libstdc++、zlib，均已安装）
    cd /tmp; apt-get download mariadb-client; \
    dpkg-deb -x mariadb-client_*.deb /tmp/mariadb-client; \
    install -m 0755 /tmp/mariadb-client/usr/bin/mariadb-dump /usr/local/bin/mariadb-dump; \
    rm -rf /tmp/mariadb-client /tmp/mariadb-client_*.deb; \
    mariadb-dump --version; \
    apt-get purge -y --auto-remove gnupg; \
    rm -rf /var/lib/apt/lists/*

COPY --from=tools /out/restic /out/rclone /usr/local/bin/
COPY --from=backend /out/opsnap /usr/local/bin/opsnap

# 非 root 运行；/data 与配置目录归 opsnap 用户（cago 的文件配置源可能回写配置文件）
RUN groupadd --gid 10001 opsnap \
 && useradd --uid 10001 --gid opsnap --home-dir /home/opsnap --create-home --shell /usr/sbin/nologin opsnap \
 && install -d -o opsnap -g opsnap -m 0700 /data /opt/opsnap/configs
COPY --chown=opsnap:opsnap configs/config.yaml /opt/opsnap/configs/config.yaml

# restic 缓存放进数据目录（$XDG_CACHE_HOME/restic）
ENV XDG_CACHE_HOME=/data/cache
WORKDIR /opt/opsnap
USER opsnap
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s CMD curl -fsS http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/usr/local/bin/opsnap"]
CMD ["-config", "/opt/opsnap/configs/config.yaml"]
