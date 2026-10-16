# opsnap

自托管的备份编排服务：从线上服务器拉取数据库和文件，用 restic 加密后写入群晖或飞牛 NAS，并提供 Web 管理面板和 HTTP API。

需求以 [`docs/specs/2026-10-15-opsnap-v1.md`](docs/specs/2026-10-15-opsnap-v1.md) 为准，贡献规则见 [`AGENTS.md`](AGENTS.md)。

## 运行

- 主密钥只从环境变量 `OPSNAP_SECRET_KEY` 读取，用于加密存储的凭据。缺失时程序拒绝启动并以非零码退出。主密钥丢失后已存储的凭据无法解密，请妥善保存。
- 配置文件默认读取 `./configs/config.yaml`，可用 `-config <路径>` 指定。数据目录（`dataDir`）默认是 `/data`，SQLite 数据库路径在 `db.dsn` 中配置，两者需保持一致。
- HTTP 默认监听 `0.0.0.0:8080`。管理面板在 `/`，API 前缀是 `/api/v1`，OpenAPI 文档在 `/api/v1/openapi.json`。

## Docker 部署

```bash
docker run -d --name opsnap -p 8080:8080 \
  -e OPSNAP_SECRET_KEY='<足够长的随机串，自行保管>' \
  -v opsnap-data:/data \
  -v /mnt/nas/opsnap:/nas/opsnap \
  ghcr.io/opskat/opsnap:0.1.0
```

- 发布的镜像只有 linux/amd64，版本见 [`CHANGELOG.md`](CHANGELOG.md)。也可以自己构建：`docker build -t opsnap .`。
- 镜像基于 Debian bookworm，内含 opsnap（内嵌面板）、restic、rclone、rsync、openssh-client、`mysqldump`（MySQL 8.4 官方客户端）、`mariadb-dump`、`pg_dump`（PGDG 最新大版本）、`mongodump`（mongodb-database-tools）和 `redis-cli`（7.x）。
- 进程以 uid 10001 的 `opsnap` 用户运行。`/data` 存放 SQLite、文件镜像和 restic 缓存；挂载的 NAS 目录（本地目录仓库）需要对 uid 10001 可写。
- 配置文件在 `/opt/opsnap/configs/config.yaml`（数据目录 `/data`、监听 `0.0.0.0:8080`），可用 `-config` 指定其他路径。

## 开发

```bash
make build   # 构建 bin/opsnap（内嵌 web/dist，前端未构建时内嵌占位页）
make test    # go test ./...
make lint    # golangci-lint run
make gen     # cago gen：生成 controller/service 与 docs/ 下的 OpenAPI 文档
```

后端基于 [cago](https://github.com/cago-frame/cago)，目录分层见 `AGENTS.md`。

## 集成测试

```bash
make it        # 经 opsctl 在资产 local-docker（id 4）上构建镜像、启动 compose 项目 opsnap-it、运行全部场景并清理
make it-vet    # 只在本机编译检查集成测试代码
```

- 场景：MySQL、PostgreSQL、Redis、MongoDB、文件各走一遍「备份 → 快照 → 经下载接口取回 → 恢复到空白实例后与源数据逐项比对」；SOCKS5 → SSH 跳板两跳代理链；导出中途失败不留快照；本地仓库缺少 `.opsnap-repo` 时拒绝写入；主机指纹变化时拒绝连接；SFTP 仓库。
- 代码在 `test/integration/`（`integration` 构建标签，`make test` 不会运行），compose 文件是 `test/integration/compose.yaml`，远端步骤见 `scripts/it-remote.sh`。
- 只操作 compose 项目 `opsnap-it` 的容器、网络、卷与 `opsnap-it/*` 镜像，所有网络都是 internal，不向宿主机发布端口；一次性密码在运行时生成，数据全部虚构。
- 分步调试：`scripts/it.sh build`、`up`、`test`、`rekey`、`test-rekeyed`、`down`；`OPSNAP_IT_SOURCE=head` 只用 HEAD 提交的代码。
