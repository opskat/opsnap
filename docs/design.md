# OpsNap 设计系统

来源：设计稿 `opsnap.pen`（Terminal 暗色风格，深浅两套变量）；代码中的 token 定义在 `frontend/src/styles/globals.css`，主题逻辑在 `frontend/src/lib/theme.tsx`。守护规则见 [`develop.md`](develop.md#守护规则)，实现分层见 [`architecture.md`](architecture.md)。

## 核心约束

- 只使用 `globals.css` 中的语义 token，不写 Tailwind 调色板类名或十六进制颜色。新概念需要同时给出深色和浅色取值，并补进下表
- 每个界面状态都要在深色、浅色主题下各检查一遍
- 先复用 `frontend/src/components/ui/`（shadcn/ui）和 `frontend/src/components/layout/` 中的组件，图标使用 `lucide-react`
- 类名用 `cn()`（`frontend/src/lib/utils.ts`）合并，变体用 `class-variance-authority`；只有 CSS 无法表达的动态值才写内联样式
- 每个自己负责的异步流程都要覆盖加载、出错、成功状态，只替换发生变化的区域，不整页刷新
- 界面中的固定文案都通过 i18n（`t()`）输出；用户数据、运行时内容和日志不翻译
- **绿底放文字的位置用 `primary`（深绿 `#08804F` 配白字，对比度约 5:1）**；品牌薄荷绿 `brand` 只用于标识、强调数字、图表等装饰位置

## 主题与 token

`ThemeProvider` 支持浅色、深色、跟随系统三种模式，选择保存在 `localStorage` 的 `opsnap-theme`；深色时在 `<html>` 上加 `dark` 类。`frontend/index.html` 中有一段内联脚本，在首帧渲染前套用主题，避免深色用户刷新时闪白，其逻辑需与 `theme.tsx` 保持一致。

| Token | 浅色 | 深色 | 用途 |
|---|---|---|---|
| `background` | `#F6F7F7` | `#0B0D0E` | 页面背景 |
| `foreground` | `#0E1113` | `#E8ECEF` | 正文 |
| `card` / `popover` | `#FFFFFF` | `#121619` | 卡片、弹层表面 |
| `sidebar` | `#FFFFFF` | `#0F1214` | 侧栏 |
| `muted` / `secondary` / `accent` | `#F0F2F3` | `#1A2024` | 次级底色、悬停、选中项 |
| `muted-foreground` | `#5E6873` | `#86909A` | 次要文字 |
| `faint-foreground` | `#66707A` | `#7D8790` | 更弱的文字（未选中的导航、分组标题） |
| `border` / `input` | `#E2E6E9` | `#20272C` | 边框、输入框 |
| `ring` | `#1FBF7F` | `#3DDC97` | 焦点环 |
| `primary` / `primary-foreground` | `#08804F` / `#FFFFFF` | `#08804F` / `#FFFFFF` | 实心按钮等绿底文字 |
| `brand` / `brand-foreground` | `#1FBF7F` / `#04140C` | `#3DDC97` / `#04140C` | 品牌标识底色及其上的图标 |
| `brand-text` | `#0B8F5A` | `#3DDC97` | 品牌色文字（强调数字） |
| `success` / `success-soft` | `#087A4B` / `#0E9F6317` | `#3DDC97` / `#3DDC971F` | 成功状态文字、状态标签底色 |
| `running` / `running-soft` | `#1765C2` / `#1F7AE017` | `#5AB0FF` / `#5AB0FF1F` | 运行中 |
| `warning` / `warning-soft` | `#8A5A00` / `#8A5A0017` | `#F5B83D` / `#F5B83D1F` | 可用但有风险 |
| `destructive` / `destructive-soft` | `#C4313A` / `#DC3B4117` | `#FF5C5C` / `#FF5C5C1F` | 失败、危险操作 |
| `pending` / `pending-soft` | `#5E6873` / `#7A848D17` | `#86909A` / `#86909A1F` | 等待中 |
| `chart-bar` | `#5FD3A1` | `#2E8F66` | 图表成功柱 |
| `track` | `#ECEFF1` | `#1E2529` | 进度条轨道 |

- 字体：界面文字 `Geist Variable`，数字、地址、耗时等用 `JetBrains Mono Variable`（`font-mono`）；中文回退到 PingFang SC / Microsoft YaHei。两种字体通过 `@fontsource-variable` 打包进前端，不依赖外部 CDN
- 圆角：`--radius: 0.375rem`（6px）

## 布局

- 整体框架：`AppShell`（`frontend/src/components/layout/AppShell.tsx`），左侧 232px 侧栏，右侧内容区独立滚动
- 侧栏导航项定义在 `frontend/src/components/layout/nav.ts`（主导航 + “系统”分组），底部放主题与语言切换
- 页面标题用 `PageHeader`（标题 + 副标题，底部分隔线）

## 组件与状态

| 需求 | 本项目的做法 |
|---|---|
| 首次加载 | 在内容区域显示“加载中…”文字；替换为内容时不改变外层卡片的位置 |
| 出错 | 在出错区域内显示 `destructive-soft` 底的错误信息，附带“重试”按钮 |
| 状态标签 | `<状态>-soft` 底色 + `<状态>` 文字 + 同色圆点，例如健康检查的“正常 / 不可用” |
| 尚未实现的页面 | `ComingSoonPage`：保留导航完整，对应功能落地时直接替换 |

参考实现：`frontend/src/pages/OverviewPage.tsx`（加载、出错重试、就绪三种状态，区域带 `aria-live="polite"`）。

## 无障碍

- 所有主题下文字对比度达到 WCAG AA：普通文字 ≥ 4.5:1，图标 ≥ 3:1；设计稿中的颜色已按此校验。不只用颜色表达含义，状态同时有文字
- 纯图标按钮必须有 `aria-label`（如主题切换按钮），切换类按钮用 `aria-pressed` 表示当前状态
- 加载、出错、成功的变化通过 `aria-live` 区域播报

## 新增页面

1. 在 `nav.ts` 中添加导航项，在 `App.tsx` 中添加路由
2. 用 `PageHeader` 和现有组件、token 组合页面
3. 实现需要的异步状态
4. 分别在深色、浅色主题和中英文下检查，确认键盘可达、图标有标签
5. 运行 `make lint`、`make test`，涉及真实流程时按 [`verification.md`](verification.md) 验证

## 来源

- token 与主题：`frontend/src/styles/globals.css`、`frontend/src/lib/theme.tsx`、`frontend/index.html`
- 组件：`frontend/src/components/ui/`、`frontend/src/components/layout/`
- 守护规则：[`develop.md`](develop.md)
- 事实核查：[`documentation.md`](documentation.md)
