# 运行时验证

## 什么时候用

已提交的单元、组件测试能完整观察到的逻辑，只跑对应测试即可。要看真实界面、真实进程、真实 HTTP 接口，或者要复现只在运行时出现的问题时，才按本文做一次运行时验证。它不能代替 TDD。

## 证据放在哪

所有一次性的启动脚本、截图、日志和报告都放在被 git 忽略的 `.dev-kit/artifacts/<spec 或问题名>/runtime/` 下。按 spec 验收时，`<名称>` 用 spec 的 slug，例如 `2026-10-15-opsnap-v1`。报告从 [`references/verification-report-template.md`](references/verification-report-template.md) 复制为 `report.md`，边跑边填。

## 两种目标

| 要观察的 | 目标与启动方式 | 观察手段 |
|---|---|---|
| 面板、登录、设置、表单校验、不依赖外部工具的接口 | 本机：`make build`，用临时配置启动 `bin/opsnap`（见下） | 无头 Chrome 截图与交互；`curl` 调接口 |
| 真实备份、快照、下载、代理链、指纹、挂载标记 | local-docker 的 compose 项目 `opsnap-it`：`scripts/it.sh build`、`up`，跑完 `down` | 集成测试或在 runner 容器里调接口；restic 用恢复密钥独立核对 |

本机没有 restic 和各数据库导出工具，涉及真实备份的需求只能在 `opsnap-it` 里观察。

### 本机启动

```bash
make build
RT=.dev-kit/artifacts/<名称>/runtime; mkdir -p "$RT/data"
sed -e "s#/data#$PWD/$RT/data#g" -e 's#0.0.0.0:8080#127.0.0.1:18080#' configs/config.yaml > "$RT/config.yaml"
OPSNAP_SECRET_KEY="$(openssl rand -hex 32)" bin/opsnap -config "$RT/config.yaml" > "$RT/opsnap.log" 2>&1 &
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/   # 200 表示已就绪
```

首次访问时用 `POST /api/v1/auth/setup` 创建管理员，或在面板 `/setup` 页创建。用完结束进程，并删除 `$RT/data`。

### opsnap-it

规则见 [`testing.md`](testing.md#集成测试)：只操作 `opsnap-it` 项目，不发布端口，不碰宿主机上的其他容器、镜像和卷，结束后执行 `scripts/it.sh down`，并确认该项目的容器、卷、网络和镜像都已为 0。经 opsctl 的命令需要审批时，由用户在桌面端批准；被拒绝就停下来。

## 判定

每条 spec 需求给出一个结论：`holds`、`does not hold` 或 `not observed`。

- 每个结论都要有一条来自被驱动界面之外的观察，例如 SQLite 里的记录、restic 用恢复密钥列出的快照、下载文件与源数据的比对、服务日志里的具体行。「没有报错」不算证据。
- 用替身代替真实依赖之前要先得到用户授权，结论行里写明用了什么替身、它覆盖不到什么。
- 不弱化断言，不跳过失败的步骤，不把失败写成通过。需要群晖或飞牛真机、另一台机器独立恢复的需求，在没有真实环境时写 `not observed` 并说明缺什么。

复现 bug 时，先说明复现脚本断言的是期望行为（修好前为红）还是当前的错误行为（修好前为绿），再把它变成一个已提交的失败测试。只有 [`testing.md`](testing.md#tdd-的例外) 允许的情况，才可以只保留人工证据。

## 维护

命令、端口或脚本变化时，同步更新本文和 [`testing.md`](testing.md)，并重新跑一遍本机启动的几条命令确认可用。
