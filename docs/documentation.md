# 文档维护

用 `git ls-files '*.md'` 找出已跟踪的文档。核对事实时使用 git 命令；未跟踪的工作区文件不能作为已提交状态的证据。

## 归属

| 文件 | 负责 |
|---|---|
| `AGENTS.md` | 项目事实、阅读路由、工程原则、架构速览 |
| `docs/develop.md` | 命令、目录结构、代码风格、守护规则、提交流程、CI |
| `docs/testing.md` | 测试设计与命令 |
| `docs/verification.md` | 测试环境与真实环境验证流程 |
| `docs/design.md` | 设计系统 |
| `docs/architecture.md` | 分层、子系统、扩展步骤、迁移 |
| `docs/documentation.md` | 本规则 |
| `docs/README.md` | 索引 |
| `e2e/README.md` | e2e 框架与 scratch 脚本写法 |

一条事实只写在它的归属文件里，其他地方链接过去，不要复制。

## docs/specs/ 是记录，不属于本文档集

已完成的需求规格不随代码同步更新。进行中的一轮里，只有需求本身变了才修改正式 spec，并且要重新获得批准后再提交；不能为了迁就实现去改 spec。被新需求取代时，新建一份 spec，并更新旧 spec 的状态。

spec 不进索引和归属表，但其中的链接必须有效，且不能包含凭据或个人信息。

## 事实与规则核查

修改任何说法时逐项核对：

| 说法 | 证据 |
|---|---|
| 文件、路径 | `git ls-files <路径>` |
| 标识符、函数签名 | `git grep` 并直接阅读文件 |
| 数量、列表 | 现场从权威来源重新枚举 |
| lint 作用范围 | 配置文件，包括 overrides 与 ignores |
| 命令 | `Makefile` 或 `package.json` 中存在，并实际运行一次 |

触及的每条规则都要能说清：归属、触发条件、动作、例外或兜底、合规证据、停止条件。把绝对化的措辞当作复查清单：

```bash
git grep -n -E '必须|不得|不要|禁止|所有|一律' -- AGENTS.md 'docs/*.md'
```

## 结构核查

- 新增、重命名、删除文档时，同步更新索引、归属表和 `AGENTS.md` 的路由
- 所有相对链接和锚点都要能解析
- 只存在于分支上的计划事实要标注，不要写成主干现状
- rebase 或解决冲突后，在最终结果上重新核查

```bash
git ls-files '*.md' | while IFS= read -r doc; do
  sed '/^```/,/^```/d' "$doc" | sed -E 's/`[^`]*`//g' \
    | grep -oE '\]\(([^)]+)\)' | sed -E 's/^\]\(|\)$//g' \
    | grep -vE '^(https?:|mailto:|#)' | while IFS= read -r link; do
      target="$(dirname "$doc")/${link%%#*}"
      [ -e "$target" ] || echo "broken $doc -> $link"
  done
done
```

## 删除

在所有已跟踪文件中搜索被删除的标识符，清理链接、索引行、列举、示例、注释、配置、CI 和不再使用的辅助代码：

```bash
git grep -inw '<被删除的标识符>' -- .
```

然后重新运行链接、锚点、规则核查和 `make verify`，只报告实际核查过的范围。
