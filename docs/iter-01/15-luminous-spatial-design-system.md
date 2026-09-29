# 15 — Luminous Spatial 设计系统契约

> 状态：`APPROVED_BY_OWNER`。2026-09-18 Owner 明确要求按设计稿 1:1 开始实现完整核心流程；支付与订阅最后处理。实现仍须逐页保留真实数据、权限、响应式与可访问性证据。

## 1. 目标

Luminous Spatial 是 Open Review 的独立视觉语言。它借鉴现代桌面应用的空间层次、克制材质和操作节奏，但不复制 Apple、Kodus 或其他产品的页面、图标和品牌资产。

目标不是“更像消费级应用”，而是在不牺牲企业代码审查密度的情况下，让用户更快完成四件事：

1. 看清当前审查状态；
2. 找到需要处理的证据；
3. 判断规则、风险和影响范围；
4. 执行可审计的处置动作。

## 2. 设计 token

### 2.1 颜色

| Token | Light | 用途 |
| --- | --- | --- |
| `--canvas` | `#F3F4FA` | 页面外层画布 |
| `--canvas-tint` | `#EEEFFC` | 顶部环境色，不作为内容背景 |
| `--surface` | `#FCFCFE` | 表格、编辑器、正文工作面 |
| `--surface-raised` | `rgba(255,255,255,.82)` | inspector、popover、toolbar |
| `--surface-selected` | `#F0ECFF` | 选中行与当前 scope |
| `--surface-muted` | `#F6F6FA` | 次级信息、代码 header |
| `--line` | `rgba(35,31,55,.10)` | 默认 hairline |
| `--line-strong` | `rgba(35,31,55,.18)` | 控件边界、分栏 |
| `--text` | `#171525` | 标题和正文 |
| `--text-secondary` | `#5F5A72` | 描述、元数据 |
| `--text-tertiary` | `#8B869A` | 占位、不可用说明 |
| `--accent` | `#6E49F6` | 主操作、焦点、链接 |
| `--accent-hover` | `#5D3BDD` | 主操作 hover |
| `--accent-soft` | `#EAE4FF` | 低强调选中背景 |
| `--critical` | `#E5484D` | Critical / blocked |
| `--warning` | `#D88700` | High / waiting / degraded |
| `--success` | `#1F9D6A` | verified / passed / healthy |
| `--info` | `#5F66C9` | 中性运行信息，不能表示成功 |
| `--critical-text` | `#C83239` | Light surface 上的 critical 正文/链接 |
| `--warning-text` | `#A95E00` | Light surface 上的 warning 正文/图标 |
| `--success-text` | `#167A52` | Light surface 上的 success 正文/链接 |

Dark 主题使用相同的语义 token 名，但每个值独立校准：

| Token | Dark | 用途 |
| --- | --- | --- |
| `--canvas` | `#0E1016` | 页面外层画布，不使用纯黑 |
| `--canvas-tint` | `#111727` | 顶部环境色 |
| `--surface` | `#151821` | 表格、编辑器、正文工作面 |
| `--surface-raised` | `rgba(25,30,43,.88)` | inspector、popover、toolbar |
| `--surface-selected` | `#262344` | 选中行与当前 scope |
| `--surface-muted` | `#1B1F2A` | 次级信息、代码 header |
| `--line` | `rgba(220,224,245,.10)` | 默认 hairline |
| `--line-strong` | `rgba(220,224,245,.18)` | 控件边界、分栏 |
| `--text` | `#F2F1F7` | 标题和正文，不使用纯白 |
| `--text-secondary` | `#B8B5C8` | 描述、元数据 |
| `--text-tertiary` | `#858397` | 占位、不可用说明 |
| `--accent` | `#8B6CFF` | 主操作、焦点、链接 |
| `--accent-hover` | `#9B81FF` | 主操作 hover |
| `--accent-soft` | `#282447` | 低强调选中背景 |
| `--critical` | `#FF6268` | Critical / blocked |
| `--warning` | `#F3A81F` | High / waiting / degraded |
| `--success` | `#45D69B` | verified / passed / healthy |
| `--info` | `#9298F4` | 中性运行信息，不能表示成功 |

约束：

- severity 不能复用品牌紫；
- 任意状态必须同时有文字或图标，不能只靠颜色；
- 半透明表面下必须有确定的 fallback 背景；
- 正文对比度至少 4.5:1，大字和非文本控件至少 3:1。
- Light 的 `--critical`、`--warning`、`--success` 是视觉基础色，不得直接作为普通正文色；surface 上的文字分别使用 `--critical-text`、`--warning-text`、`--success-text`。`--warning` 对 Light surface 仅 2.77:1，也不能作为无背景的独立 UI 图标/边界。
- Dark 不能由 CSS `filter`、统一反色或单一颜色映射生成；代码、图表、focus、selection 和 semantic states 必须逐项验收。

### 2.2 字体

- UI：`Geist Sans, Inter, ui-sans-serif, system-ui, sans-serif`；
- 代码：`Geist Mono, SFMono-Regular, Consolas, monospace`；
- Display：40/44，字重 650，仅用于一级工作区标题；
- H1：32/38，字重 650；H2：24/30，字重 620；H3：18/24，字重 620；
- Body：14/21；Compact：13/18；Meta：12/16；
- 路径、SHA、规则键、金额和 token 数使用等宽字体或 tabular numerals。

### 2.3 空间、圆角与阴影

- spacing：`4, 8, 12, 16, 20, 24, 32, 40, 48, 64`；
- control height：紧凑 `32px`，默认 `38px`，强调 `44px`；
- radius：控件 `10px`，工作面 `14px`，sheet `24px`，pill `999px`；
- Light `shadow-float`：`0 20px 60px rgba(36, 29, 71, .12), 0 2px 10px rgba(36, 29, 71, .06)`；
- Dark `shadow-float`：`0 24px 72px rgba(0, 0, 0, .34), inset 0 1px rgba(255,255,255,.035)`；
- Light `shadow-control`：`0 1px 2px rgba(36, 29, 71, .08)`；
- Dark `shadow-control`：`0 1px 2px rgba(0, 0, 0, .28), inset 0 1px rgba(255,255,255,.025)`；
- 任何页面同一视口内不得出现超过两个明显浮起层级。

### 2.4 材质

`FrostedSurface` 只允许用于：

- global toolbar；
- compact rail；
- inspector / command palette / popover；
- modal 和 temporary sheet。

Light 建议实现：`background: rgba(255,255,255,.78)`；Dark 建议实现：`background: rgba(25,30,43,.88)`；两者均可使用 `backdrop-filter: blur(20px) saturate(125%)`。不支持 blur 时退化为 `--surface`，不能降低可读性。表格、代码、表单和长正文禁止使用半透明背景。

### 2.5 主题选择

- 默认遵循 `prefers-color-scheme`；
- 用户可选择 `System / Light / Dark`，偏好按用户跨 workspace 保存；
- 服务端渲染时使用 cookie/初始脚本避免主题闪烁；
- `color-scheme` 必须同步更新原生表单、滚动条和浏览器控件；
- 切换主题不能重置表格、筛选器、编辑器或 sheet 状态；
- 图表、diff、代码高亮和第三方嵌入必须订阅同一主题状态。

## 3. Shell 与导航

### 3.1 桌面 ≥ 1280px

- global toolbar：`56px`，sticky；
- compact rail：`64px`，只显示图标和 tooltip；
- page canvas：`max-width: 1600px`，两侧 `28–40px`；
- 一级领域：`Review / Policy / Operate`；
- workspace、command search、notification 和 identity 只能出现一次；
- 页面级 tabs 位于标题下方，不与全局领域导航混合。

### 3.2 笔记本 768–1279px

- toolbar 保留，command search 收为图标/快捷键；
- rail 默认仅当前入口可见，完整入口由 command sheet 提供；
- 两栏页面的 inspector 变成右侧 overlay sheet；
- 表格隐藏低优先列，但保留列选择器。

### 3.3 移动 < 768px

- 顶栏仅保留返回/页面标题、workspace 和 overflow；
- 底部 4 个主入口：Review、Issues、Policy、Operate；
- 表格变为语义列表，不做横向缩小版桌面表格；
- inspector、filter builder、rule editor 都使用全屏 sheet；
- 固定底部动作区必须尊重 safe area。

## 4. 核心组件契约

### 4.1 `GlobalToolbar`

属性：workspace、domain、command、notifications、identity。切换 workspace 后必须重新解析权限、连接和 scope；不能只替换 URL slug。

状态：default、search-open、workspace-open、notification-unread、offline、permission-refreshing。

### 4.2 `CompactRail`

最多 6 个持久入口；图标下不常驻文字，hover/focus 显示 tooltip。当前领域使用 `--accent-soft` 表面和 2px accent marker；禁用入口说明原因。

### 4.3 `PageHeader`

顺序固定：breadcrumb → title/description/status → primary action → secondary actions。最多一个 filled primary action；危险动作不能与默认主操作共用样式。

### 4.4 `SegmentedView`

用于 saved view 或同一对象的互斥视图，不用于普通筛选。计数是视图范围内的服务端结果；加载时显示 skeleton，不回退为 0。

### 4.5 `FilterBuilder`

字段、操作符、值三段式；URL 可序列化；支持保存视图。Clear 只在存在非默认条件时出现。权限不足的字段不能通过 URL 绕过前端隐藏。

### 4.6 `DataSurface`

统一支撑 Issues、PR、runs、audit 和 usage ledger：

- header `40px`，row `54–58px`；
- hover 使用 `--surface-muted`，selection 使用 `--surface-selected`；
- 键盘上下移动、Enter 打开、Space 多选、Escape 返回列表；
- path、PR、repository、rule 都必须是真链接；
- 空状态、错误、stale、权限不足必须占据表格工作面，不移除列语义。

### 4.7 `InspectorSheet`

桌面宽 `420–520px`；列表选择改变时不刷新 shell。标题、状态、摘要、证据、上下文、动作按此顺序呈现。关闭后焦点回到触发行。

### 4.8 `EvidenceViewer`

显示 repository、path、revision、line、rule snapshot 和 engine。代码仅是证据，不允许在没有来源时伪造完整文件上下文。支持 provider deep link、copy path、copy prompt；路径必须可点击。

### 4.9 `AsyncAction`

状态：idle、confirming、queued、running、succeeded、failed、superseded。需要异步处理的动作先给确定性回执和 run link，不能让按钮无限 loading。

### 4.10 `StatePanel`

统一处理 loading、empty-healthy、empty-filtered、partial、error、permission、stale、offline。健康空状态可以庆祝；同步失败不能显示为 0。

## 5. 页面布局模式

| Pattern | 页面 | 布局 |
| --- | --- | --- |
| Inbox + inspector | Issues、Work queue、approvals | 列表保持上下文，右侧 sheet 处理 |
| Evidence detail | Issue、PR review、run | 主证据流 + 稳定 context sheet |
| Split editor | Rule composer、messages、prompts | 左目录 + 中编辑 + 右 scope/provenance |
| Operational table | PR、CLI、audit、keys | filter + dense table + detail sheet |
| Settings list | Connections、Notifications、Models、Members | summary table + focused configuration sheet |
| Health canvas | Cockpit、Usage、Insights、Platform health | 少量可行动指标 + 趋势 + 明确下一步 |

## 6. 交互与状态

- 列表选择、tab、filter、sheet 开闭使用 140–200ms；禁用装饰性弹跳；
- reduced-motion 下移除位移，仅保留 opacity；
- optimistic UI 只用于可安全回滚的标签、保存视图等动作；规则发布、exception、密钥撤销必须等待服务端 receipt；
- 新 revision 到达导致 run superseded 时，页面保留旧证据并显示不可复用原因；
- provider 外链必须按 GitHub、GitLab.com 或 self-managed base URL 构造，不能硬编码 github.com；
- secret、token、私钥永不回显，界面只显示 secret reference 和最后验证时间。

## 7. 设计验收

- 三张核心桌面稿采用同一 token、shell 和组件语法；
- 1440、1024、390 三档覆盖所有核心布局模式；
- Light 与 Dark 都必须独立通过视觉、对比度、代码高亮和图表验收；
- 键盘可完成筛选、选行、打开/关闭 inspector、保存规则草稿和触发测试；
- 200% 缩放不丢失操作；
- skeleton 不造成显著 layout shift；
- 所有状态包含 loading、empty、partial、error、permission、stale、offline；
- 设计评审结论写入本文件头部后，才能开始生产组件实现。

## 8. 当前视觉证据

- Issues inbox：`assets/console-v2-issues-inbox-v3-apple.png`；
- Issue detail：`assets/console-v2-issue-detail-v3-apple.png`；
- 六屏系统板：`assets/console-v2-screen-system-v3-apple.png`。
- Dark Issues inbox：`assets/console-v2-issues-inbox-v3-dark.png`；
- Dark Issue detail：`assets/console-v2-issue-detail-v3-dark.png`；
- Dark 六屏系统板：`assets/console-v2-screen-system-v3-dark.png`。
- 页面与切换状态板：见 [16-console-v2-page-state-mockups.md](16-console-v2-page-state-mockups.md)，覆盖页面族、tab 数据语义、onboarding、治理分析和 workspace 恢复状态。
- 响应式、扩展 Dark 与对比度证据：见 [17-responsive-dark-validation.md](17-responsive-dark-validation.md)。
