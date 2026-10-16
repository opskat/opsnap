# 开发规范

## 命令

```bash
make build           # 构建前端并嵌入，产出 bin/opsnap（首次需要 pnpm 联网安装依赖）
make build-go        # 只构建 Go 二进制；web/dist 没有前端产物时内嵌占位页
make run             # go run ./cmd/app -config ./configs/config.yaml；需先设置 OPSNAP_SECRET_KEY，并把配置里的 dataDir、db.dsn 改到可写目录
make gen             # 用与 go.mod 一致的 cago 版本生成 docs/ 下的 OpenAPI 文档；改了 internal/api 的请求或响应就要跑
go test ./internal/service/server_svc/...   # 单个包
make test            # go test ./...，包含 internal/pkg/archtest 的架构门禁
make lint            # golangci-lint run（.golangci.yml）
make frontend-test   # 前端：typecheck + ESLint/Prettier + vitest
make check           # 提交前跑这一条：make test + make lint + make frontend-test
```

前端开发：`cd frontend && pnpm dev`，`/api` 转发到 `OPSNAP_API`（默认 `http://127.0.0.1:8080`）。格式化用 `pnpm format`，自动修 lint 用 `pnpm lint:fix`。

## 目录

严格遵循 cago 的目录与分层（[`../AGENTS.md`](../AGENTS.md)）。

```text
cmd/app/main.go            入口：读主密钥、加载配置、注册仓储、启动组件
configs/config.yaml        cago 配置（只读这一个文件；OPSNAP_SECRET_KEY 由 main 自行读取）
internal/api/<域>/         请求/响应结构体（mux.Meta 声明路由）；router.go 绑定路由与鉴权中间件
internal/controller/<域>_ctr/   只做校验和转发
internal/service/<域>_svc/      业务逻辑：接口 + 单例访问器
internal/repository/<域>_repo/  数据访问；mockgen 生成 mock_<域>_repo/
internal/model/entity/<域>_entity/  GORM 实体与简单业务规则
internal/pkg/              无业务状态的库：secret、cmdbuild、executor、retention、sshconn、code（错误码）、archtest（架构门禁）
migrations/                gormigrate 迁移，文件名 YYYYMMDDNNNN_<名称>.go，在 migrations.go 的列表里登记
web/                       内嵌前端：web/dist 是 make build 复制进来的产物（git 只跟踪 .keep），缺产物时回退 web/placeholder
frontend/                  React + shadcn/ui；src/components/ui 由 shadcn CLI 生成，@ 指向 src/
docs/                      spec、开发文档，以及 make gen 生成的 docs.go / swagger.*
```

## 新增一个业务模块

照着 `server` 模块（`internal/{api/server,controller/server_ctr,service/server_svc,repository/server_repo,model/entity/server_entity}`）写：

1. 在 `migrations/` 新建迁移文件写原生 DDL，并登记到 `migrations.go` 的列表末尾；已发布的迁移只追加，不修改。
2. 实体放在 `internal/model/entity/<域>_entity/`，仓储接口加上 `//go:generate mockgen -source <文件>.go -destination mock_<域>_repo/<文件>.go -package mock_<域>_repo` 并执行 `go generate`。
3. 在 `cmd/app/main.go` 注册仓储实现；在 `internal/api/router.go` 把 controller 绑定到 `auth_svc.Middleware()` 保护的分组。
4. 对外错误在 `internal/pkg/code` 增加错误码和中文文案，用 `i18n.NewError(ctx, code.X)` 返回。
5. POST/PUT 请求体字段要有 `json` 标签（cago 按 json 解码请求体）。
6. 跑 `make gen` 更新 OpenAPI，再跑 `make check`。

## 机器强制的规则

| 规则 | 正确写法 | 门禁与豁免 |
|---|---|---|
| controller 不直接访问仓储或数据库 | controller → `internal/service` | `internal/pkg/archtest` 的 `TestImportBans`；`*_test.go` 豁免（测试要注入 mock 仓储） |
| repository 不依赖 service/controller，service 不依赖 controller，model 不依赖三者 | 依赖只能自上而下 | 同上 |
| 不用裸 `go` 语句 | `gogo.Go(func() error { … })`，不把请求 ctx 传进去 | `TestNoBareGoStatements`；豁免测试文件和 `<父包>/<父包>test` 测试辅助包（如 `sshconn/sshconntest`） |
| 迁移不用 `AutoMigrate` | gormigrate + 原生 DDL | `TestNoAutoMigrate` |
| Go 静态检查与格式 | gofmt、goimports、gosec 等 | `.golangci.yml`，`make lint` |
| React Hooks 规则、未使用变量、Prettier 格式 | effect 依赖写全；不要在 effect 里同步 setState，改在渲染期间对比上一次的值 | `frontend/eslint.config.js`，`pnpm lint` |

守卫测试读取真实配置，正反两个方向都有断言：Go 侧在 `internal/pkg/archtest/checker_test.go`，前端在 `frontend/src/test/eslint-guard.test.ts`。以上门禁于 2026-10-15 启用，启用时存量违规为 0，没有豁免清单。

AGENTS.md 里的其余原则（凭据不进日志和参数、失败的导出不能记为成功的快照、TDD 等）仍然只靠评审把关，其中凭据不进参数由 `internal/pkg/cmdbuild` 和 `internal/pkg/rsh`（rsync 命令）的逐个构建器测试覆盖。

## 提交

提交信息用中文，前缀沿用 `feat:` / `fix:` / `docs:` / `chore:`；按路径显式 `git add`，不用 `git add -A`。

## 门禁范围

远程仓库是 `github.com/opskat/opsnap`。`.github/workflows/ci.yml` 在 PR 和推送到 `main` 时跑与 `make check` 相同的检查（Go 测试与架构门禁、golangci-lint、前端 typecheck/lint/vitest）；仓库没有开启分支保护，CI 失败不会阻止合并。集成测试（`make it`）依赖 opsctl 与集成测试主机，只在本地运行。

## 发版

1. 把 `Makefile` 的 `VERSION` 与 `Dockerfile` 的 `ARG VERSION` 改成要发布的版本，合并到 `main`。
2. 在 `main` 上打带注释的标签，标签说明就是这次的更新说明（含已知问题），然后推送：`git tag -a v<版本> -F <说明文件> && git push origin v<版本>`。
3. `.github/workflows/release.yml` 先跑同样的检查，再构建 linux/amd64 镜像推到 `ghcr.io/opskat/opsnap`（标签 `<版本>`、`<主>.<次>`、`latest`），最后用标签说明创建 GitHub Release。

## 相关

[`../AGENTS.md`](../AGENTS.md) · [`testing.md`](testing.md) · [`specs/2026-10-15-opsnap-v1.md`](specs/2026-10-15-opsnap-v1.md)
