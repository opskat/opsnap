# 架构

> 速览：[`../AGENTS.md`](../AGENTS.md#架构速览)。产品需求见 [`specs/2026-09-23-opsnap-v1.md`](specs/2026-09-23-opsnap-v1.md)。

## 分层与依赖方向

```text
internal/api（请求/响应定义 + router.go）
        │ router.go 绑定
        ▼
internal/controller ──> internal/service ──> internal/repository ──> db.Ctx(ctx)（GORM / SQLite）
```

| 约束 | 仓库中的具体形式 | 执行方式 |
|---|---|---|
| controller 只做转发 | 控制器方法签名 `func (c *X) M(ctx, *api.Req) (*api.Resp, error)`，直接调用 `xxx_svc.X().M(...)` | review-only |
| controller 不导入 `internal/repository` | 测试文件除外（需要注册 mock） | `internal/archtest` |
| service、repository 不反向依赖 | service 不导入 controller；repository 不导入 service、controller | `internal/archtest` |
| `internal/api` 不依赖业务层 | 仅 `internal/api/router.go` 可导入 controller | `internal/archtest` |
| service 通过获取函数取依赖 | `system_repo.System()`；实现在 `cmd/opsnap/main.go` 用 `RegisterSystem(NewSystem())` 注册 | review-only |

## 子系统

### HTTP 与路由

cago 的 `mux.HTTP(api.Router)` 启动 Gin。`internal/api/router.go` 把所有业务接口挂在 `/api/v1` 下，接口路径与方法由请求结构体的 `mux.Meta` 标签声明（如 `internal/api/system/system.go` 的 `HealthRequest`）。cago 自带的 `/health` 只返回 `ok`；带版本号和元数据库状态的健康检查是 `/api/v1/system/health`。

### 内嵌前端

`internal/web` 用 `//go:embed all:dist` 内嵌前端构建产物，通过 `mux.RegisterMiddleware(web.Register)` 注册为 Gin 的 `NoRoute` 处理器：

- 存在的静态文件直接返回
- 其他 GET 请求回退到 `index.html`，交给前端路由
- `/api/` 下的未知路径和非 GET 请求保持 404
- 前端未构建时（`dist/` 中只有 `.gitkeep`）返回提示“请先执行 make build”

### 启动顺序

`cmd/opsnap/main.go` 依次注册：`component.Core()`（日志）→ 创建 SQLite 数据目录 → `component.Database()` → 执行迁移 → HTTP 服务。配置文件默认 `./configs/config.yaml`，可用 `-c` 指定。版本号在构建时注入 `github.com/cago-frame/cago/configs.Version`（`Makefile` 中取 `git describe`）。

## 扩展步骤

### 新增一个接口

1. 在 `internal/api/<领域>/` 定义请求（带 `mux.Meta`）与响应结构体
2. 在 `internal/controller/<领域>_ctr/` 写测试：`setupXxxTest` 注册 mock repository、用 `muxtest.NewTestMux()` 绑定控制器；运行并确认失败
3. 需要数据访问时，在 `internal/repository/<领域>_repo/` 定义接口，加 `//go:generate mockgen ...` 注释，执行 `make generate`
4. 实现 service（`internal/service/<领域>_svc/`）与 controller，让测试通过
5. 在 `internal/api/router.go` 中 `Bind`，新 repository 在 `cmd/opsnap/main.go` 注册
6. 运行 `go test ./...` 与 `make lint`

参考实现：健康检查（`internal/api/system`、`internal/controller/system_ctr`、`internal/service/system_svc`、`internal/repository/system_repo`）。

## 数据与迁移

- 元数据库为 SQLite，路径由配置 `db.dsn` 决定，默认 `./runtime/opsnap.db`（开启 WAL 与 5 秒 busy timeout）；启动时自动创建所在目录
- 迁移写在 `migrations/`：新增一个返回 `*gormigrate.Migration` 的函数，追加到 `RunMigrations` 的参数列表末尾。已发布的迁移不修改；使用确定性的 SQL，不使用 `AutoMigrate(&entity)`，避免实体结构变化影响旧迁移
- 目前还没有任何业务表，`RunMigrations` 在列表为空时直接返回

## 生成产物

| 路径 | 来源 | 重新生成 |
|---|---|---|
| `internal/repository/*/mock/*.go` | 各 repository 接口上的 `//go:generate mockgen` | `make generate` |
| `internal/web/dist/`（除 `.gitkeep` 外不提交） | `frontend/` | `make build-web` |

## 相关文档

[`develop.md`](develop.md) · [`testing.md`](testing.md) · [`../AGENTS.md`](../AGENTS.md)
