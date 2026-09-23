# 测试

## 先判断要测什么

从改动的契约出发，只为适用的行为补用例：边界值、非法输入与失败、状态与生命周期、顺序与并发、兼容性与安全、进程或外部边界、可机械判定的源码约定。都不适用时，一个有代表性的正常路径就够了。

写测试前先说清楚：

1. 可观察的契约是什么
2. 触发的输入、状态或操作顺序
3. 可观察的结果
4. 这个测试能拒绝哪种看似合理的错误实现

## 选择测试边界

选能观察到真实契约的最窄边界：

| 契约 | 边界 | 本项目的形式 |
|---|---|---|
| 解析、映射、校验、状态逻辑 | 单元 | Go 包内测试；前端 `*.test.ts` |
| 接口的输入输出与错误 | 控制器 | `muxtest.NewTestMux()` + mock repository（见 `internal/controller/system_ctr/system_test.go`） |
| 渲染状态、交互、无障碍 | 组件 | Vitest + Testing Library（见 `frontend/src/pages/OverviewPage.test.tsx`） |
| 真实进程、内嵌前端、构建产物 | e2e | Playwright 冒烟（`e2e/tests/`） |
| 需要真实数据库、存储或服务器 | 真实环境验证 | [`verification.md`](verification.md) |

## 有意识地覆盖行为空间

先写一个正常路径，再为每个适用的分支补一个用例：

- 边界值，以及空、缺省、重复的输入
- 依赖出错、拒绝、超时、取消，以及出错后状态保持不变
- 重复调用、过期的异步结果、清理
- 仅当契约承诺时，才覆盖乱序或重叠操作

不要沿着同一个分支堆砌普通样例。修 bug 的回归测试要足够贴近真实故障：把原因恢复回去，测试就应该变红。

## 断言、mock 与夹具

- 断言返回、渲染、持久化或发出的结果；只有当“调用某个协作者”本身就是契约时，才断言调用
- Go：repository 用 `go.uber.org/mock` 生成的 mock（`internal/repository/*/mock/`），在 `setupXxxTest` 中用 `RegisterXxx` 注册；测试组织用 GoConvey（`convey.Convey` 嵌套场景）+ testify 断言。需要数据库时用 cago 的 `testutils.Database(t)`（sqlmock）
- 前端：接口通过 `vi.stubGlobal("fetch", ...)` 模拟 HTTP 响应，因为所有请求都经过 `request()`；断言页面上显示的内容，而不是 mock 返回了什么
- 夹具保持最小，并且能区分对错

## TDD 的例外

只有两种：

- 确实不改变行为的重构、类型调整、删除、重命名、依赖升级，按比例验证即可
- 确实无法自动化，用人工验证并保留证据

文件类型或任务名称都不构成例外。

## 什么测试不该写

不写同义反复的测试、重复覆盖、只是把 props 渲染出来的测试、测试 mock 或框架本身的测试、名不副实的测试，以及更适合用 lint 规则表达的“源码文本断言”。

保留那些虽然很薄、但唯一覆盖某个分支、映射、无障碍推导、完整性约束或历史回归的测试。

## 范围与清理

只清理本次改动涉及的、或被本次新增守护规则直接取代的无效测试；其他的报告出来，另开改动处理。删除前先看生产路径，并确认同一契约在其他测试里有覆盖。

测试变红或变慢时先归类：生产代码回归、契约已过期、共享状态或时序导致的不稳定、误把真实 I/O 放进了单元测试、确实冗余。不要用重试或加长超时掩盖不稳定。

## 运行

```bash
go test -run TestSystemHealth ./internal/controller/system_ctr/   # 单个 Go 测试
pnpm -C frontend exec vitest run src/lib/api.test.ts             # 单个前端测试文件
make test                                                        # 全部单元测试（含守护测试）
make test-cover                                                  # Go 覆盖率
make e2e                                                         # 冒烟 e2e
make lint                                                        # 静态检查与类型检查
```

- Go：GoConvey + testify + go.uber.org/mock；cago 的 `muxtest`、`testutils`
- 前端：Vitest（happy-dom 环境，`globals: true`）+ Testing Library + jest-dom 断言，配置在 `frontend/vite.config.ts` 的 `test` 段
- e2e：Playwright，见 [`../e2e/README.md`](../e2e/README.md)

## 相关文档

[`../AGENTS.md`](../AGENTS.md) · [`verification.md`](verification.md) · [`../e2e/README.md`](../e2e/README.md)
