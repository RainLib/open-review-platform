# 06 — 前端体验与视觉设计规范

## 1. 设计目标

界面应被理解为“AI 审查工作台”，不是传统 admin 模板。它服务三个核心问题：

1. 现在发生了什么？
2. 哪里需要我介入？
3. 我做出的规则与操作会产生什么影响？

视觉要求：前沿但克制、美观但不牺牲信息、圆角但不幼态、统一但不把每个页面做成同一种卡片网格。

### 1.1 明确避免

- 首页“四个 KPI 卡片 + 大表格”的通用后台结构。
- 每个模块都重复“筛选栏 + CRUD 表格 + 右侧抽屉”。
- 大面积高饱和渐变、霓虹赛博风、过度玻璃拟态和无意义 3D。
- 为了“AI 感”展示不可验证的 chain-of-thought。
- 用颜色作为唯一状态表达；用动画掩盖慢响应。

## 2. 设计语言：Quiet Intelligence

### 2.1 视觉性格

- **Quiet**：暖灰瓷白画布、充足留白、低噪声边界。
- **Intelligent**：用状态流、上下文和建议动作表达智能，而非机器人插画。
- **Crafted**：非对称构图、内容驱动尺寸、细致排版与动效。
- **Trustworthy**：权限、来源、版本、费用和系统状态可见。

### 2.2 Token 初稿

```text
color.canvas          #F6F5F2
color.surface         #FFFFFF / 88% with controlled blur
color.ink             #11142B
color.muted           #667085
color.rail            #17172F
color.primary         #4F46E5
color.primaryAccent   #6D5CFF
color.success         #1DAA78
color.warning         #E59A22
color.danger          #E55B5B
color.info            #3978F6

radius.control        12px
radius.card           22px
radius.hero           28px
radius.pill           999px

space base            4px
content max           1600px
focus ring            2px primary + 2px offset
```

实际实现需使用语义 token，不在组件中硬编码 hex。

### 2.3 排版

- 推荐字体：Inter/Geist 类可变 sans；代码与 ID 使用等宽字体。
- 页面标题 36–48px，紧凑行高；任务标题 26–32px；正文 14–16px。
- 数值采用 tabular numbers。
- 长仓库名、branch、commit、rule key 可复制且有 tooltip，不用不可恢复省略。

### 2.4 圆角与层级

- 主工作面 22–28px；嵌套容器减少 4–8px，形成自然层级。
- 不允许三层以上卡片嵌套。
- 阴影只区分“画布上浮层”和“普通 surface”，不为每张卡制造浮动。
- 表格放在独立列表页，使用柔和行分隔和 sticky header；不用每行再套卡片。

## 3. 信息架构

主导航保持小而稳定：

```text
Home
Reviews
Tasks
Rules
Repos
Connect
────────
Usage
Audit
Settings
```

在窄屏桌面端，Usage/Audit/Settings 收入底部账户菜单。命令面板支持键盘跳转和全局动作。

### 3.1 路由

```text
/:org/home
/:org/reviews
/:org/reviews/:provider/:repository/:number
/:org/tasks
/:org/tasks/:taskId
/:org/rules
/:org/rules/:ruleSetId
/:org/rules/:ruleSetId/versions/:version
/:org/rules/approvals
/:org/rules/test-lab
/:org/repos
/:org/repos/:repositoryId
/:org/connect
/:org/connect/:installationId
/:org/usage
/:org/audit
/:org/settings/{general,members,roles,sso,security,data}
```

## 4. 核心设计稿

### 4.1 Home — AI 审查工作台

![AI 审查工作台](assets/dashboard-overview.png)

信息顺序：

1. 人性化状态摘要：“系统健康，三件事需要处理”。
2. Review pulse：接收、理解、审查、发布的实时流，不是装饰图表。
3. Needs your attention：高风险 finding、规则审批、连接异常，直接给下一动作。
4. In motion：运行中、排队、完成任务的内容卡，按任务需要决定尺寸。
5. Queue/provider/budget 健康胶囊，保持轻量。

行为：

- attention 项点击进入上下文，不直接在首页执行不可逆操作。
- running task 显示真实阶段和可解释进度；未知进度使用阶段状态，不伪造百分比。
- `Start review` 打开 command sheet：仓库、PR/MR、模式、规则和预算预览。

### 4.2 Task — 实时执行画布

![实时任务执行画布](assets/review-run-detail.png)

信息顺序：

- 顶部显示 LIVE/终态、任务标题、触发者、commit、时长和可用动作。
- Reasoning canvas 展示高层执行阶段与当前活动；标题下明确“不展示私有推理”。
- Signals 展示可验证事件：规则解析、工具结果、finding 生成、发布等待。
- Context/Rules/Usage inspector 允许切换，不把全部细节堆在同一屏。
- 底部 Ask 区仅对已产生证据提问；运行未完成时避免暗示最终结论。

取消交互：

1. 点击 Cancel run。
2. 弹出简短确认，说明哪些已发布内容不会被撤回。
3. 成功后状态变为 `Cancel requested`，禁止重复点击。
4. SSE 收到 `cancelled` 后显示终态；若已不可取消，解释原因并刷新 actions。

### 4.3 Rules — 企业策略工作室

![企业策略工作室](assets/enterprise-rules.png)

信息顺序：

- Gallery 按目的、风险与结果展示规则集，而非只列名称和时间。
- Policy composition 直观展示组织基线、团队 overlay、仓库 additions。
- Version constellation 让历史可见，但不喧宾夺主。
- Impact dock 在发布动作附近展示样本、critical 增量、误报风险、置信度和成本。
- Published 页面不显示“保存”，只允许 `Create draft`。

规则编辑器需同时提供：表单视图（大多数用户）、结构化 JSON/DSL 视图（专家）、diff 视图（审批人）。

## 5. 其他页面详细规格

### 5.1 Reviews

目标：以 PR/MR 为中心聚合多次 run、findings 和协作状态。

- 列表按 attention、open、resolved、archived 分组。
- 主要信息：仓库/编号/标题、最新 head、最新 run、critical/major 数、作者、更新时间。
- 支持批量重新运行/分配 reviewer；批量动作先预览影响。
- 详情页包含 Overview、Findings、Runs、Conversation、Policy snapshot。

### 5.2 Tasks 列表

这是适合使用表格的运营页面：

- 列：task、repository/review、trigger、mode、state/stage、age、duration、cost、owner。
- saved views：Running、Failed、Needs attention、Expensive、Mine。
- 筛选条件同步 URL；可分享视图。
- 行展开只显示近期事件，不在表格中嵌入完整任务详情。
- 批量 retry/cancel 仅管理员可见，必须确认并填写理由。

### 5.3 Repository

- Overview：触发模式、默认规则、健康、最近审查。
- Review settings：事件、分支/path、draft 行为、mode、超限策略。
- Policy：effective rules 和来源，提供“为什么生效”。
- Connection：权限、webhook、last delivery、test connection。
- Data：保留和数据地域，只显示租户允许值。

### 5.4 Connect

- provider 卡片用于首次选择；安装后转为连接健康列表。
- 安装向导：选择 provider → 权限解释 → 外部授权 → 仓库选择 → webhook 验证 → 首次同步 → 测试审查。
- 每一步可恢复；外部授权回来后用 state 恢复，不依赖浏览器内存。
- 明确展示平台“能读什么、能写什么、何时获取短期 token”。

### 5.5 Usage

- 顶部先展示预算状态和预测，不用虚荣指标。
- 按 repository/mode/model/rule set 分解成本与运行量。
- 支持时间范围、导出、异常任务定位。
- 额度变化显示生效时间和可能影响的 admission 行为。

### 5.6 Audit

- append-only 事件流；筛选 actor/action/resource/outcome/date。
- before/after 只展示安全摘要或结构化 diff，secret 永不出现。
- 导出是异步任务，包含范围预览、理由和下载审计。

### 5.7 Settings

- General、Members、Roles、SSO、Security、Data retention。
- destructive zone 与日常设置视觉分离；高风险动作输入组织名确认。
- 设置保存采用 revision/ETag；冲突时展示 server/local diff，不覆盖他人修改。

## 6. Command Palette

`⌘K / Ctrl+K`：

- 导航到任务、仓库、规则和 PR/MR。
- 动作：Start review、Create rule set、Connect provider。
- 自然语言只做检索/建议，执行高风险动作仍进入明确确认流程。
- 结果按当前租户和权限过滤；不把无权资源名称泄漏给用户。

## 7. 状态设计

每个页面/组件必须覆盖：

| 状态 | 体验要求 |
| --- | --- |
| loading | 保持布局骨架，超过 400ms 才展示，避免闪烁 |
| empty-first-use | 解释价值，单一主要动作，提供示例 |
| empty-filtered | 显示清除筛选，不重复 onboarding |
| partial | 已有数据可用，单独标记失败区域并允许重试 |
| error | 人类可读原因、request ID、可执行恢复动作 |
| forbidden | 解释所需角色，不假装资源不存在（跨租户 API 除外） |
| stale | 展示旧数据时间和重新连接，不清空页面 |
| reconnecting | SSE 重连提示轻量，恢复后自动补事件 |
| rate-limited | 展示可重试时间，不进行倒计时请求风暴 |
| offline | 保留只读缓存，禁用 mutation 并解释 |

## 8. 组件清单

- `AppRail`, `CommandBar`, `OrgEnvironmentSwitcher`
- `ReviewPulse`, `AttentionFeed`, `TaskTile`
- `RunJourney`, `StageNode`, `SignalStream`, `RunInspector`
- `StatusPill`, `SeverityGlyph`, `ProgressRing`, `BudgetRing`
- `PolicyGallery`, `PolicyLayerStack`, `VersionConstellation`
- `ImpactDock`, `RuleDiff`, `ApprovalStepper`, `ScopeBuilder`
- `DataTable`, `SavedViewBar`, `FilterBuilder`, `BulkActionTray`
- `ConnectionHealth`, `PermissionManifest`, `UsageBreakdown`
- `AuditTimeline`, `AsyncExportPanel`
- `ConfirmAction`, `ErrorState`, `EmptyState`, `PermissionGate`

组件必须接收语义状态，不接受任意颜色字符串来决定业务含义。

## 9. 动效

- 页面/卡片进入：150–220ms，淡入 + 4–8px 位移。
- Review pulse/active stage：低频、低幅呼吸；不持续闪烁。
- 列表重排使用 FLIP 类平滑过渡，但高频事件最多每 500ms 合并一次。
- `prefers-reduced-motion` 下关闭非必要移动与波形动画。
- 完成/失败不用彩纸或震动，采用清晰状态和下一动作。

## 10. 响应式

优先桌面，但不忽略平板：

- ≥1440：主画布 + inspector 同屏。
- 1024–1439：rail 收窄，inspector 为可固定侧面板。
- 768–1023：主导航变顶部/抽屉，inspector 变 bottom sheet。
- <768：阶段一提供任务查看、approve/cancel 等关键响应式能力；复杂规则编辑显示“建议桌面使用”，但不得完全不可访问。

## 11. 无障碍与国际化

- WCAG 2.2 AA；键盘可操作，焦点顺序与视觉顺序一致。
- status/severity 同时有文本和图标；图表有文本摘要/表格替代。
- 目标尺寸至少 44×44px；tooltip 不承载唯一信息。
- 语义 HTML、ARIA live 只播报关键阶段变化，progress 高频变化不轰炸读屏。
- 所有文案进入 i18n；日期、数字、货币和时区使用 locale-aware formatter。
- 中英文长度差异纳入布局，不在按钮中写死宽度。

## 12. 设计审批交付物

在前端编码前必须补齐并批准：

- 本文三张高保真方向稿。
- Home、Task、Rules 三个关键流程的可点击原型。
- Tasks/Reviews/Connect 的中保真流程图。
- component inventory 与 token 文件。
- loading/empty/error/permission/reconnect 状态矩阵。
- 桌面 1440、窄桌面 1024、移动 390 三个断点验证。
- 无障碍初审和文案/i18n 清单。

当前图片批准只代表视觉方向；只有上述项目与产品/API 契约一起批准，才解除实现门禁。
