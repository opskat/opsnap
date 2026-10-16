# opsnap

自托管的备份编排服务：从线上服务器拉取数据库和文件，用 restic 加密后写入群晖或飞牛 NAS，并提供 Web 管理面板和 HTTP API。

## 功能

- **数据源**：MySQL/MariaDB、PostgreSQL、Redis、MongoDB、文件。数据库导出经管道直接写入 restic，不落盘；文件先经 rsync 增量同步到镜像目录，再生成快照。失败或不完整的导出不会记为成功的快照。
- **连接**：数据库可直连，或经服务器建 SSH 隧道。服务器支持密码、私钥、私钥 + 口令认证，可配置代理链（SSH 跳板 / SOCKS5 / HTTP CONNECT），逐跳测试连接并核对主机指纹。
- **存储位置**：本地目录（NFS/SMB 挂载）、SFTP、S3 兼容、restic REST Server、WebDAV（经 rclone）。本地目录靠标记文件 `.opsnap-repo` 判断 NAS 是否已挂载，未挂载时拒绝写入。
- **密钥**：每个存储位置有一把 opsnap 日常使用的密钥（加密保存）和一把可选的恢复密钥（只显示一次，opsnap 不保存），可轮换。
- **调度**：每小时 / 每 6 小时 / 每天 / 每周 / cron；保留规则、超时、停止运行；每周自动清理空间与抽样校验；「太久没有成功」告警。
- **通知**：通用 Webhook（失败、恢复正常、太久没有成功、每次成功），发送失败自动重试。
- **快照**：在面板里浏览快照，勾选文件流式下载为 `.tar.gz`，或下载数据库导出文件。opsnap 不会把数据写回线上。
- **单点登录**：可绑定 Gitea 等 OIDC 提供方上的一个账号登录面板，并可关闭密码登录。
- **HTTP API**：面板用的就是对外 API，配合 API 密钥和 OpenAPI 文档，可交给脚本或 AI 助手巡检和操作。

## 快速开始

```bash
docker run -d --name opsnap -p 8080:8080 \
  -e OPSNAP_SECRET_KEY='<足够长的随机串，自行保管>' \
  -v opsnap-data:/data \
  -v /mnt/nas/opsnap:/nas/opsnap \
  ghcr.io/opskat/opsnap:latest
```

1. 主密钥可以用 `openssl rand -hex 32` 生成，**务必另行保存**：丢失后已存储的凭据无法解密。
2. 打开 `http://<主机>:8080`，首次访问会进入 `/setup` 创建管理员账号。
3. 依次添加服务器、存储位置，再创建备份任务。添加存储位置时保留「同时生成一把恢复密钥」，并把恢复密钥存进密码管理器。

第二个 `-v` 只在使用「本地目录」类型的存储位置时需要：把宿主机上挂载好的 NAS 共享目录映射进容器，该目录需要对 uid 10001 可写。

## 部署说明

- **主密钥**：只从环境变量 `OPSNAP_SECRET_KEY` 读取，用于加密存储的凭据。缺失或与已存凭据不匹配时拒绝启动。可在「设置」里轮换，轮换后要把环境变量改成新值。
- **数据目录**：`/data` 存放 SQLite、文件镜像和 restic 缓存，请挂载为持久卷。
- **配置文件**：镜像内为 `/opt/opsnap/configs/config.yaml`，可用 `-config <路径>` 指定其他文件。修改 `dataDir` 时要同步修改 `db.dsn` 里的数据库路径。
- **端口与接口**：默认监听 `8080`。面板在 `/`，API 前缀 `/api/v1`，OpenAPI 文档在 `/api/v1/openapi.json`，健康检查 `/health`。
- **恢复密码登录**：关闭了密码登录、而单点登录的提供方又不可用时，给容器加上环境变量 `OPSNAP_PASSWORD_LOGIN=1` 重启，就能用用户名密码登录（不改动设置里保存的开关）。恢复访问后去掉这个变量再重启。
- **镜像**：只发布 linux/amd64，以 uid 10001 的 `opsnap` 用户运行，内含 restic、rclone、rsync、openssh-client、`mysqldump`（MySQL 8.4）、`mariadb-dump`、`pg_dump`（PGDG 最新大版本）、`mongodump` 和 `redis-cli`（7.x）。也可以自己构建：`docker build -t opsnap .`。

## 单点登录（OIDC）

可以把 opsnap 唯一的管理员绑定到 Gitea（或 Authentik、Keycloak 等任何 OIDC 提供方）上的一个账号，之后用这个账号登录面板。以 Gitea 为例：

1. 在 opsnap「设置 → 单点登录」填写面板对外地址（浏览器打开 opsnap 用的地址），页面会显示回调地址 `<面板对外地址>/api/v1/auth/oidc/callback`。
2. 在 Gitea「设置 → 应用」创建 OAuth2 应用，重定向 URI 填上面的回调地址，记下 Client ID 和 Client Secret。
3. 回到 opsnap 填写 Issuer 地址（Gitea 的 ROOT_URL，例如 `https://git.example.com/`，注意结尾的 `/`）、Client ID、Client Secret，点击「保存并检查」。
4. 点击「绑定 Gitea 账号」，在 Gitea 授权后回到设置页。之后登录页会出现「用 Gitea 登录」，只有绑定的这个账号能登录。
5. 需要的话关闭「允许用户名密码登录」，登录页就只剩单点登录；提供方不可用时按上面「恢复密码登录」处理。

Client Secret 用主密钥加密保存。修改 Issuer 或 Client ID、解除绑定、停用单点登录都会解除绑定并重新打开密码登录。API 密钥不受单点登录影响。

## 脱离 opsnap 恢复

NAS 上的仓库是标准的 restic 仓库。在存储位置的「密钥」对话框里下载恢复包，里面有仓库地址、访问方式和 restic 恢复命令（不含任何密钥）；在任意能访问 NAS 的机器上装好 restic，按恢复包操作，提示输入密码时粘贴恢复密钥即可。

## HTTP API

在「设置 → API 密钥」创建密钥（前缀 `osk_`，只显示一次），请求时带上 `Authorization: Bearer <key>`；接口清单见 `/api/v1/openapi.json`。

```bash
curl -H "Authorization: Bearer osk_..." http://<主机>:8080/api/v1/overview
```

API 密钥不区分权限，拥有与管理员相同的能力，包括修改配置和下载快照明文。

## 开发

后端是 Go + [cago](https://github.com/cago-frame/cago) + SQLite，前端是 `frontend/` 下的 React + shadcn/ui + Tailwind CSS v4。

```bash
make build           # 构建前端并内嵌，产出 bin/opsnap
make run             # 本地运行；需先设置 OPSNAP_SECRET_KEY，并把配置里的 dataDir、db.dsn 改到可写目录
make gen             # 改了 internal/api 的请求或响应后，重新生成 OpenAPI 文档
make check           # 提交前跑：Go 测试（含架构门禁）+ golangci-lint + 前端 typecheck/lint/vitest
cd frontend && pnpm dev   # 前端开发服务器，/api 转发到 OPSNAP_API（默认 http://127.0.0.1:8080）
```

贡献规则见 [`AGENTS.md`](AGENTS.md)，开发、测试与运行时验证分别见 [`docs/develop.md`](docs/develop.md)、[`docs/testing.md`](docs/testing.md)、[`docs/verification.md`](docs/verification.md)。
