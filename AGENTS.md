# AGENTS.md

`CLAUDE.md` 引入本文件；面向 AI 与贡献者的规则只写在这里。

## 改动前先读

| 什么时候 | 读 | 它负责 |
|---|---|---|
| 改动任何行为、接口或界面之前 | [`docs/specs/2026-10-15-opsnap-v1.md`](docs/specs/2026-10-15-opsnap-v1.md) | v1 的全部需求、失败处理、视觉系统和测试决策 |
| 运行、构建、新增模块、提交之前 | [`docs/develop.md`](docs/develop.md) | 命令、目录、新增模块步骤、机器强制的规则与门禁范围 |
| 写或改测试之前 | [`docs/testing.md`](docs/testing.md) | 测试边界、替身与夹具、集成测试约束、运行命令 |
| 需要看真实界面、进程或接口，或复现运行时问题时 | [`docs/verification.md`](docs/verification.md) | 本机与 opsnap-it 两种目标的启动方式、证据位置、判定规则 |

## 项目概况

opsnap：自托管的备份编排服务。它从线上服务器拉取数据库和文件，用 restic 加密后写入群晖或飞牛 NAS，并提供 Web 管理面板和 HTTP API。

- 后端：Go + [cago](https://github.com/cago-frame/cago) 框架，SQLite 存储
- 前端：React + shadcn/ui + Tailwind CSS v4，测试用 vitest，代码放在 `frontend/`
- 产物：单个 Docker 镜像，内含 opsnap 二进制（内嵌前端）以及 restic、rclone、rsync、openssh 和各数据库导出工具
- 当前状态：v1 已合并到 `main`，推送 `v*` 带注释标签后由 `.github/workflows/release.yml` 发布镜像与 Release

## 工程原则

标注 `review-only` 的条目只靠评审把关；其余条目的可判定部分由 `make check` 强制（门禁见 [`docs/develop.md`](docs/develop.md#机器强制的规则)）。GitHub Actions（`.github/workflows/ci.yml`）在 PR 和推送到 `main` 时跑同样的检查；仓库没有开启分支保护，CI 失败不会阻止合并。

- **严格遵循 cago 的目录与分层。**（分层依赖方向由 `internal/pkg/archtest` 强制；目录与职责划分 `review-only`） 入口是 `cmd/app/main.go`，配置在 `configs/`，业务代码按 `internal/{api,controller,service,repository,model/entity,task,pkg/code}` 分层，迁移放在 `migrations/`。controller 只做校验和转发，业务逻辑放在 service，数据访问只在 repository，简单的业务规则放在 entity 方法里。不要另起一套目录或分层。
- **按 cago 的方式处理错误、迁移和并发。**（禁止裸 `go` 语句、禁止 `AutoMigrate` 由 `internal/pkg/archtest` 强制；错误码用法 `review-only`） 对外的错误一律通过 `internal/pkg/code` 里的错误码和 `i18n.NewError` 返回。迁移用 gormigrate 编写原生 DDL 并在迁移注册表里登记，不用 `AutoMigrate`。启动 goroutine 用 `gogo.Go`，不要把请求的 ctx 传进去。
- **先写失败的测试，再改行为。**（`review-only`） 新行为、修复 bug、改变接口都从一个能复现问题的失败测试开始。修 bug 前必须先复现并找到根因，做不到就停下来报告。
- **凭据不进日志，也不进命令行参数。**（`review-only`；`internal/pkg/cmdbuild` 的测试逐个断言构建器不把凭据放进参数） 服务器密码、私钥、数据库密码、restic 密钥只能经环境变量或标准输入传给子进程。存储时用主密钥 `OPSNAP_SECRET_KEY` 加密；API 密钥只保存哈希；恢复密钥不保存。
- **任何失败或不完整的导出都不能记为成功的快照。**（`review-only`） 这是 v1 的硬性不变量，见 spec 开头。
- **前端代码只放在 `frontend/` 下。**（`review-only`；前端的 Hooks 规则与格式由 `pnpm lint` 强制） shadcn 组件用 shadcn CLI 生成在 `frontend/src/components/ui`；颜色、字体等 token 以 spec「视觉系统」一节为准。
- **集成测试只在 compose 项目 `opsnap-it` 里操作。**（`review-only`） 集成测试主机（opsctl 资产，见 `scripts/it.sh`）上还有其他在用的数据库等容器。测试只启动、连接、清理 compose 项目 `opsnap-it` 自己的容器、网络和卷，不向宿主机发布默认端口，不碰任何其他容器、镜像或卷。
- **只改与任务相关的代码和测试。**（`review-only`） 发现无关的问题，报告出来，不要顺手改掉。
- **代码注释和提交信息用中文。**（`review-only`）

## 架构

> 以 spec 为准；下图是规划中的结构（目录职责见 [`docs/develop.md`](docs/develop.md#目录)），整体落地后会补充单独的架构文档。

```text
浏览器 / API 客户端
  └─ HTTP /api/v1（gin，经 cago mux）── 内嵌 SPA（frontend/ 构建产物）
       └─ controller → service → repository → SQLite
            └─ 运行引擎 / 调度器（internal/task）
                 ├─ SSH 代理链 → 线上服务器（rsync、SSH 隧道 → 数据库）
                 └─ restic → NAS 上的仓库（本地目录 / SFTP / S3 / REST / WebDAV）
```
