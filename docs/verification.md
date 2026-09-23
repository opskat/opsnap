# 真实环境验证

## 什么时候需要

提交的测试已经能完整观察改动逻辑时，只跑对应的测试即可。需要真实的界面、进程、数据库、存储或服务器才能确认时，或者要复现只在运行时出现的 bug 时，按本文执行。它不替代 TDD。

## 测试环境

测试服务部署在 docker.local（opsctl 资产 `local-docker`，192.168.8.141），compose 项目名 `opsnap-test`，定义在 [`../deploy/test/compose.yaml`](../deploy/test/compose.yaml)。

| 服务 | 地址（从开发机访问） | 说明 |
|---|---|---|
| MySQL 8.0 | `192.168.8.141:13306`，用户 `root` | 已开启 binlog、ROW 格式、GTID |
| PostgreSQL 16 | `192.168.8.141:15432`，用户 `postgres` | `wal_level=logical`，10 个复制槽 |
| MinIO（S3） | API `192.168.8.141:19000`，控制台 `:19001`，用户 `opsnap` | 固定版本 `RELEASE.2025-04-22T22-12-26Z` |

三个服务使用同一个测试密码，保存在本地 `e2e/.env` 的 `OPSNAP_TEST_PASSWORD`（不提交），首次执行 `make test-env-up` 时自动生成。

```bash
make test-env-up       # 部署或更新（经 opsctl 复制 compose 与 .env 到 /opt/opsnap-test，并等待健康检查通过）
make test-env-status   # 查看容器状态
make test-env-down     # 停止容器，保留数据
scripts/test-env.sh destroy   # 删除容器、数据卷和远端目录
```

注意事项：

- **那台机器上还跑着其他项目的容器**（包括占用 3306、5432、6379 的 MySQL、PostgreSQL、Redis）。只操作 `opsnap-test` 项目，不碰其他容器
- 机器内存约 7.8 GiB，按需启动服务，不要一次性常驻所有引擎版本
- 镜像通过 `katch.ggnb.top/` 代理拉取（写法为 `<代理>/docker.io/...`、`<代理>/quay.io/...`），换机器时设置环境变量 `OPSNAP_TEST_REGISTRY_MIRROR`，设为空则直连
- MongoDB、Redis、Kafka 和带 LVM 的 SSH 测试目标（特权容器 + loop 设备）尚未加入，到对应开发轮次再补进 compose

## 验证流程

1. 先跑 `make lint` 和相关的测试；风险较高或需要门禁时再跑全量 `make verify`
2. 构建并启动被验证的目标：`make build` 后运行 `bin/opsnap -c <配置文件>`，或用 `make dev-server` 启动开发实例。只启动目标本身；真实依赖通过 `e2e/.env` 访问。`.env` 里缺少某个服务的配置时，说出服务名和缺失的变量并询问用户，不要自行启动替代品或改用 mock
3. 选最省事、又能观察到契约的方式，把产生的所有东西放在不提交的 `e2e/scratch/<场景>/` 下：

   | 如何到达并观察目标 | 需要写什么 |
   |---|---|
   | 现有命令或入口就够，且不依赖、不改写本机状态 | 不写，直接驱动并读取独立证据 |
   | 需要特定的启动方式、隔离的状态或真实环境配置，只观察一次 | 写一个启动到目标为止的脚本，之后手动驱动 |
   | 需要重放操作序列，或时序、并发本身就是契约 | 写完整的 scratch 脚本（`pnpm -C e2e scratch`） |

   复用 [`../e2e/README.md`](../e2e/README.md) 的隔离方式和独立证据，但不复用它的夹具。每种方式都至少要有一项观察来自被驱动界面以外的路径：元数据库中的数据、结构化日志、只读接口（如 `/api/v1/system/health`）或输出文件，并在产生它的运行结束前复制进场景目录。
4. 运行前，从 [`references/verification-report-template.md`](references/verification-report-template.md) 复制出 `report.md`，边运行边补充证据
5. 记录如何驱动目标、各步骤的退出码、决定结论的运行时观察、未覆盖的部分，以及用户自行复现的最短步骤

```bash
pnpm -C e2e scratch                      # 运行 e2e/scratch/ 下的全部脚本
pnpm -C e2e scratch -g "<场景名>"         # 只运行一个场景
```

按 spec 验收时，场景名使用 spec 的 slug，spec 中每条需求对应结论表的一行，结论只能是“成立”“不成立”“未观察到”。

复现 bug 时，先说明复现脚本断言的是预期行为（修复前为红）还是当前的错误行为（修复前为绿），然后转成一个提交的、会失败的测试；只有满足 [`testing.md`](testing.md#tdd-的例外) 中人工验证的例外时才可以不写。

不要放宽断言、跳过失败的步骤，或把红的说成绿的。后台或运行时效果要用具体的日志、指标或数据变化来证明，“没有报错”不是证据。有破坏性或外部副作用的操作、以及用 mock 替代真实依赖之前，都要先取得授权；替代时，结论表要写明用什么替代了什么、没有覆盖什么。

## 维护

确认文中命令都存在；`e2e/playwright.config.ts` 排除了 `scratch/`，`e2e/playwright.scratch.config.ts` 只指向它；`.gitignore` 覆盖 `e2e/scratch/`、`e2e/.env`。路径或测试框架变化后按 [`documentation.md`](documentation.md) 核对。
