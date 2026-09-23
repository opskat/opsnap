# 开发规范

## 命令

所有命令都从仓库根目录的 `Makefile` 进入；文档、CI 使用同一套命令。

```bash
make install        # 安装前端、e2e 依赖和 Playwright 浏览器
make dev-server     # 启动后端，监听 127.0.0.1:8210（首次运行会从 configs/config.example.yaml 复制出 config.yaml）
make dev-web        # 启动前端开发服务器，/api 转发到 127.0.0.1:8210
make build          # 构建前端并内嵌进 bin/opsnap
make generate       # go generate ./...（重新生成 mock）
make lint           # golangci-lint + ESLint + Prettier + i18n 键检查 + e2e 类型检查
make lint-fix       # 自动修复格式与可修复的 lint 问题
make test           # Go 测试 + 前端 Vitest（含守护测试）
make test-cover     # Go 覆盖率
make e2e            # 构建后运行 Playwright 冒烟测试
make verify         # lint + test + e2e，提交前的完整验证
```

只跑一部分：

```bash
go test -run TestSystemHealth ./internal/controller/system_ctr/
pnpm -C frontend exec vitest run src/pages/OverviewPage.test.tsx
pnpm -C e2e exec playwright test -g "主题"   # 需要先 make build
```

包管理：前端与 e2e 用 pnpm 10，版本由各自 `package.json` 的 `packageManager` 字段锁定；锁文件必须提交，CI 使用 `--frozen-lockfile`。

## 目录结构

```text
cmd/opsnap/            入口：配置、注册 repository、启动 cago 组件
configs/               config.example.yaml（提交）；config.yaml（本地，不提交）
internal/
  api/                 请求/响应定义（mux.Meta）与 router.go
  controller/          控制器，只做参数转发，<name>_ctr/
  service/             业务逻辑，接口 + 单例获取函数，<name>_svc/
  repository/          数据访问，接口 + Register/获取函数，<name>_repo/，mock 在 mock/
  web/                 内嵌前端（dist/ 为构建产物，仓库中只保留 .gitkeep）
  archtest/            分层守护测试
migrations/            元数据库迁移（只追加）
frontend/              React 前端（src/ 下 @ 别名指向 src）
e2e/                   Playwright 冒烟测试与 scratch 验证
deploy/test/           docker.local 测试服务的 compose 定义
scripts/               仓库级脚本（test-env.sh）
docs/                  开发文档与需求规格
```

- 路径别名：前端 `@/*` → `frontend/src/*`（`tsconfig.json` 与 `vite.config.ts` 同步配置）
- Go：`gofmt` + `goimports`，本项目包单独成组放在第三方包之后（`.golangci.yml` 的 `local-prefixes`）；代码注释与提交信息用中文
- 前端：Prettier（行宽 120、双引号、`trailingComma: es5`，见 `frontend/.prettierrc`）；组件文件用 PascalCase，工具模块用 camelCase
- 测试位置：Go 测试与被测文件同目录 `*_test.go`；前端测试与被测文件同目录 `*.test.ts(x)`，守护测试在 `frontend/src/__tests__/`

## 守护规则

| 规则 | 正确写法 | 门禁与豁免 |
|---|---|---|
| controller 不直接访问 repository | 经对应 service 调用 | `internal/archtest`；豁免：`*_test.go`（需要注册 mock repository） |
| service、repository 不反向依赖上层 | controller → service → repository | `internal/archtest` |
| `internal/api` 只放请求/响应定义 | 路由注册集中在 `internal/api/router.go` | `internal/archtest`；豁免：`internal/api/router.go` |
| `internal/` 不用标准库 `log` | cago 的 `logger.Ctx(ctx)` | `internal/archtest`（`log/slog` 不受影响） |
| 不写 Tailwind 调色板类名 | 语义 token，见 [`design.md`](design.md) | ESLint `no-restricted-syntax`，作用于 `frontend/src/**`；豁免：测试文件 |
| JSX 中不写死中文 | `t("key")`，键写入 `frontend/src/i18n/locales/*.json` | ESLint `i18next/no-literal-string`（仅检查含汉字的文本与可见属性）；豁免：测试文件 |
| 各语言文件键一致，字面量 `t("a.b")` 的键必须存在 | 同时修改 `zh-CN.json` 与 `en.json` | `frontend/scripts/check-i18n.mjs`（`pnpm lint` 的一部分） |
| 不直接调用 `fetch` | `frontend/src/lib/api.ts` 的 `request()` | ESLint `no-restricted-globals`；豁免：`src/lib/api.ts` 与测试文件 |
| React 19 写法 | `ref` 作 prop、`<Context value>`、`use(Context)` | ESLint `react-x/no-forward-ref`、`no-context-provider`、`no-use-context` |
| 通用 Go 检查 | — | golangci-lint v2（`.golangci.yml`，沿用 opskat 的规则集） |

守护测试用真实配置验证每条规则“违规被报告、合规与豁免不被误报”：
- Go 分层：`internal/archtest/archtest_test.go`
- ESLint：`frontend/src/__tests__/eslint-harness.test.ts`
- i18n 键检查：`frontend/scripts/check-i18n.test.mjs`

以上规则自 2026-09-23 起生效，目前没有存量豁免。

**shadcn 组件**：用 `pnpm dlx shadcn@latest add <组件>` 添加后，检查生成文件中 `cn` 的导入路径是否为 `@/lib/utils`，并确认 `package.json` 没有被加入名为 `cn` 的无关依赖——本项目初始化时 CLI 两处都出过错。

## 国际化

- 界面文案放在 `frontend/src/i18n/locales/zh-CN.json` 与 `en.json`，两份文件的键必须一致
- 组件中用 `useTranslation()` 的 `t("分组.键")`；不要用 `t(key, { defaultValue })`，缺键时应由检查脚本发现
- 语言切换用 `frontend/src/i18n/index.ts` 的 `changeLanguage()`，会同步写入 `localStorage`（`opsnap-lang`）和 `<html lang>`

## 接口请求

前端只通过 `request<T>(path)` 调用后端：它拼接 `/api/v1` 前缀、带上 `Accept-Language`，解析 cago 的 `{ code, msg, data }` 响应；`code !== 0`、HTTP 失败或响应不是 JSON 时抛出 `ApiError`（含 `code`、`status`）。按领域在 `frontend/src/lib/` 下封装具体接口，例如 `system.ts` 的 `getHealth()`。

## 日志

后端使用 cago 的 `logger.Ctx(ctx)`，错误通过 `zap.Error(err)` 附带，例如 `internal/service/system_svc/system.go`。日志中不得输出凭据、密钥和数据源密码。

## 提交与 PR

- 不在 `main` 上直接开发；从 `main` 拉分支，推送后开 PR
- 提交信息用中文，格式 `<类型>: <描述>`，类型如 `feat`、`fix`、`docs`、`test`、`refactor`、`chore`
- 提交前运行 `make verify`；没有 pre-commit 钩子

PR 描述需包含：做了什么、为什么；运行过的命令与结果；界面改动附截图或运行时证据；涉及元数据库结构时说明影响范围与回滚方式。

## CI

`.github/workflows/ci.yml` 在 PR 与推送到 `main` 时运行，命令与本地一致：

- `go`：golangci-lint v2.12.2 + `go test ./...`
- `frontend`：`pnpm lint` + `pnpm test`（前端），`pnpm lint`（e2e）
- `e2e`：`make e2e`

**这些检查目前还不会阻止合并**：需要仓库管理员在 GitHub 上为 `main` 开启分支保护，并把上述三个 job 设为必需检查。开启之前，它们只作为提示。

## 相关文档

[`../AGENTS.md`](../AGENTS.md) · [`architecture.md`](architecture.md) · [`testing.md`](testing.md) · [`verification.md`](verification.md)
