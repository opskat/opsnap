# E2E

## 1. 两条路线

| | 冒烟测试 | 本地真实环境验证 |
|---|---|---|
| 路径 | `e2e/tests/`（提交） | `e2e/scratch/`（不提交） |
| 命令 | `make e2e` | `pnpm -C e2e scratch` |
| 范围 | 稳定的核心回归流程 | 针对某次改动或某个 bug 的一次性验证 |
| 外部系统 | 不连接任何外部系统 | 经授权可访问 docker.local 测试服务 |
| 产出 | CI 结论 | `scratch/<场景>/report.md` 及证据 |

scratch 脚本提升为冒烟用例需要单独决定。冒烟范围：应用身份与启动、主导航、一条带独立持久化证据的核心增删改查流程、一条关键的完整性流程。目前还没有业务数据，后两类待对应功能落地后补充。

## 2. 测试框架

```text
make e2e → make build（生成 bin/opsnap）→ pnpm -C e2e test
  → global-setup.ts：临时目录 + 专用端口启动真实的 bin/opsnap
  → Playwright（Chromium）驱动页面与接口
  → 断言 + 独立证据（/api/v1/system/health 接口）
```

| 资源 | 隔离方式 |
|---|---|
| 配置与元数据库 | 每次运行在系统临时目录下新建 `opsnap-e2e-*`，teardown 时删除 |
| 端口 | 专用端口 18291（`e2e/ports.ts`），避开开发实例的 8210；启动前若端口已被占用直接失败 |
| 应用身份 | 就绪检查要求健康检查返回 `code: 0` 且 `database: ok`，端口被其他程序占用时不会误判 |
| 浏览器状态 | 每个用例使用独立的浏览器上下文（主题、语言的 localStorage 互不影响） |

## 3. 冒烟命令与覆盖

```bash
make install   # 首次：安装依赖与 Chromium
make e2e
```

当前用例（`e2e/tests/smoke.spec.ts`）：健康检查、未知接口 404、首页显示的版本号与接口一致、前端路由可直接访问并刷新、深色主题刷新后保持、中英文切换。

## 4. 协议 mock

目前冒烟测试不依赖任何外部系统，因此没有 mock。需要时放在 `e2e/fixtures/`，作为无依赖的独立进程启动并暴露就绪检查，端口通过环境变量传入，只实现用到的协议响应。

## 5. 进程编排与清理

`e2e/global-setup.ts` 生成临时配置、启动 `bin/opsnap`、等待就绪，并返回 teardown 函数：先发 SIGTERM，5 秒后仍未退出则 SIGKILL，最后删除临时目录。失败时 Playwright 在 `e2e/test-results/` 保留 trace 和截图，CI 会把它们和 `playwright-report/` 上传为构件。

## 6. 编写 scratch 脚本

验证过程中编写和观察到的所有内容都放在 `e2e/scratch/<场景>/`；选择哪种方式（不写脚本、只写启动脚本、写完整脚本），以及结论与证据的要求，由 [`../docs/verification.md`](../docs/verification.md) 负责。用 `pnpm -C e2e scratch` 运行。

主配置 `playwright.config.ts` 用 `testIgnore: ["**/scratch/**"]` 排除 scratch；`playwright.scratch.config.ts` 只指向 `./scratch` 并去掉 globalSetup，被验证实例的地址来自 `OPSNAP_BASE_URL`。这种机械隔离保证 CI 不会收集到本地脚本。

### 真实环境

把 [`.env.example`](.env.example) 复制为 `e2e/.env`（不提交）。只有 scratch 配置会读取它，冒烟测试和应用本身不读；环境变量优先于文件。访问真实环境前取得授权，使用隔离的测试数据，结束后清理。测试服务的部署见 [`../docs/verification.md`](../docs/verification.md#测试环境)。

`.env` 没有配置某个服务时，要去问，而不是自己安排：自行启动依赖或替换成 mock，得出的结论描述的是一个没人选择过的环境。说出服务名和缺失的变量，然后询问用户。

## 7. 排查失败

```bash
pnpm -C e2e exec playwright show-report          # 查看 HTML 报告
pnpm -C e2e exec playwright show-trace e2e/test-results/<用例>/trace.zip
```

被测进程的日志直接输出在 Playwright 的终端里。

## 相关文档

[`../docs/verification.md`](../docs/verification.md) · [`../docs/testing.md`](../docs/testing.md) · [`../AGENTS.md`](../AGENTS.md)
