# AGENTS.md

`CLAUDE.md` 引用本文件；面向 AI 与贡献者的规则只写在这里。

## 改动前先读

| 什么时候 | 读 | 它负责 |
|---|---|---|
| 写代码前 | [`docs/develop.md`](docs/develop.md) | 命令、目录结构、代码风格、守护规则、提交流程 |
| 写测试前 | [`docs/testing.md`](docs/testing.md) | 测试边界、覆盖、mock 与夹具、命令 |
| 做真实环境验证前 | [`docs/verification.md`](docs/verification.md) | docker.local 测试环境、scratch 验证流程与报告 |
| 改界面前 | [`docs/design.md`](docs/design.md) | 设计 token、组件、深浅主题、状态、无障碍 |
| 改分层或新增模块前 | [`docs/architecture.md`](docs/architecture.md) | 分层与依赖方向、子系统、扩展步骤、迁移 |
| 改文档前 | [`docs/documentation.md`](docs/documentation.md) | 文档归属与事实核查 |

索引：[`docs/README.md`](docs/README.md)。需求规格：[`docs/specs/`](docs/specs/)。

## 项目概况

OpsNap —— 自托管的服务器备份与同步 Web 控制台，单个 Go 二进制内嵌前端。

- 后端：Go 1.26，[cago](https://github.com/cago-frame/cago) 框架（Gin + GORM），元数据库 SQLite
- 前端：React 19 + TypeScript + Vite 6 + Tailwind CSS v4 + shadcn/ui（new-york），i18next 中英文
- 包管理：Go modules；前端与 e2e 用 pnpm 10（`packageManager` 字段锁定），CI 使用 `--frozen-lockfile`
- 模块：`github.com/opskat/opsnap`
- 产物：`bin/opsnap`（`make build`）

## 工程原则

- **可观察行为先写测试（TDD）。** 先写接口定义与测试、看到失败，再写实现；边界与例外见 [`docs/testing.md`](docs/testing.md)。review-only
- **修 bug 先复现。** 先写出能稳定失败的测试并找到根因，否则停下来说明。review-only
- **依赖方向只能是 controller → service → repository。** controller 不直接访问 repository，repository 与 service 不反向依赖上层，`internal/api` 只放请求/响应定义。enforced by `go test ./internal/archtest/`（CI `go` job）
- **接口请求只走 `request()`。** 前端所有接口调用经过 `frontend/src/lib/api.ts`，统一 `/api/v1` 前缀、语言头与错误转换。enforced by ESLint `no-restricted-globals`（CI `frontend` job）
- **界面文案走 i18n，颜色走设计 token。** JSX 中不写死中文，不写 Tailwind 调色板类名。enforced by ESLint + `scripts/check-i18n.mjs`（CI `frontend` job）
- **迁移只追加，不修改已发布的迁移；迁移中用确定性 DDL，不用 `AutoMigrate(&entity)`。** review-only
- **凭据不进仓库、日志和报告。** 本地配置 `configs/config.yaml`、测试密码 `e2e/.env` 均已被 `.gitignore` 排除。review-only
- **只改被要求的范围。** 发现无关问题时报告，不顺手改。review-only

## 架构速览

> 详见 [`docs/architecture.md`](docs/architecture.md)。

```text
浏览器 ──> Gin（cago mux）
             ├─ /api/v1/*  ──> controller ──> service ──> repository ──> SQLite（GORM）
             └─ 其他 GET   ──> internal/web（内嵌的前端构建产物，单页应用回退到 index.html）
```

- `cmd/opsnap` —— 入口：加载配置、注册 repository、按顺序启动 cago 组件
- `internal/api` —— 请求/响应定义（`mux.Meta` 声明路由）与 `router.go` 路由注册
- `internal/controller` / `internal/service` / `internal/repository` —— 三层实现
- `internal/web` —— 内嵌前端并提供单页应用
- `migrations` —— 元数据库迁移
- `frontend` —— React 前端，构建输出到 `internal/web/dist`

### 关键约束

- 路由只在 `internal/api/router.go` 注册；`/api/` 下的未知路径返回 404，不回退成页面
- 新增 repository 需在 `cmd/opsnap/main.go` 注册，并用 `go generate` 生成 mock
