# 测试

各接缝要验证什么，以 spec 的「Testing decisions」一节为准（[`specs/2026-10-15-opsnap-v1.md`](specs/2026-10-15-opsnap-v1.md#testing-decisions)）；本文件只管怎么写、写在哪、怎么跑。

## 写之前先说清

1. 被测的可观察契约；
2. 触发它的输入、状态或顺序；
3. 可观察的结果；
4. 这个测试能挡住哪种看似合理的错误实现。

先写一个正常路径，再按适用的分支各补一个：边界值与空输入、依赖出错或超时或取消后状态不变、重复调用、并发与顺序（只在契约承诺时）、凭据与权限。同一分支不堆样例。修 bug 的回归测试要在恢复根因时变红。

## 选择边界

| 契约 | 边界 | 本仓库的写法 |
|---|---|---|
| 纯映射、校验、命令构建、保留规则 | 单元 | 表驱动；如 `internal/pkg/cmdbuild`、`internal/pkg/retention` |
| HTTP 接口的校验、状态码、鉴权 | controller | `muxtest.NewTestMux()` + mockgen 仓储，见各 `*_ctr/*_test.go` 的 `setupXxxTest` |
| 业务规则、持久化、加密落库 | service / repository | 内存 SQLite：`gorm.Open(sqlite.Open("file::memory:"))`、`SetMaxOpenConns(1)`、`migrations.RunMigrations`，见 `internal/repository/admin_repo/admin_test.go` |
| 子进程、超时、进程组 | executor | 真实子进程测试在 `internal/pkg/executor`；上层用 `executor.NewFake().On(...)` 替身 |
| SSH 代理链、端口转发、指纹 | 进程内服务 | `internal/pkg/sshconn/sshconntest`：`StartSSH`、`StartSOCKS5`、`StartHTTPConnect`、`StartEcho`、`StartBlackhole` |
| 时间相关（锁定、调度、告警） | 注入时钟 | 服务暴露可替换的 now，不在测试里 sleep 等真实时间 |
| 前端渲染与交互 | 组件 | vitest + happy-dom + Testing Library；`src/test/render.tsx` 的 `renderPage`、`src/test/api-mock.ts` 的 `mockApi`/`ok`/`fail` |
| 真实工具与真实数据库全链路 | 集成 | 只在 compose 项目 `opsnap-it` 里，规则见下 |

## 断言与替身

断言返回值、响应、落库内容或渲染结果；只有当调用本身就是契约时（例如「导出失败时没有调用 restic backup」）才断言调用。替身只放在外部或昂贵的边界上：仓储（mockgen）、执行器（`executor.Fake`）、HTTP 对端（`httptest.Server`）、SSH 与代理（`sshconntest`）。

凭据相关的测试用带标记的假值（如 `SECRET-…`），断言它不出现在参数、日志和 API 响应里。

## TDD 的例外

只有两种：确实不改变行为的重构、改名、删除、依赖升级，按比例验证即可；以及确实无法自动化的部分（spec 列出的群晖/飞牛真机、视觉效果、异机恢复），在收尾阶段人工验证并保留证据。

## 集成测试

经 `scripts/it.sh` 在集成测试主机（opsctl 资产，由 `OPSNAP_IT_ASSET` 指定）上的 compose 项目 `opsnap-it` 里运行：独立网络，不向宿主机发布默认端口，只连接本项目启动的容器，清理只针对本项目的容器、网络和卷，数据全部虚构。宿主机上可能还有其他在用的容器，不能碰。

## 怎么跑

```bash
go test ./internal/pkg/sshconn/...      # 单个包
go test -race ./internal/pkg/executor/  # 涉及并发时加 -race
make test                               # Go 全量（含架构门禁）
cd frontend && pnpm test src/pages/login.test.tsx   # 单个前端测试
make frontend-test                      # 前端 typecheck + lint + vitest
make check                              # 提交前全量
```

Node 26 自带的 `localStorage` 会干扰 happy-dom，`vite.config.ts` 里的 vitest 已加 `--no-experimental-webstorage`。

## 相关

[`../AGENTS.md`](../AGENTS.md) · [`develop.md`](develop.md)
