# 01 — 产品与详细功能设计

## 1. 产品定位

Open Review Platform 是面向组织和研发团队的 AI 代码审查控制平面。它通过 GitHub App、GitLab 应用或 API 接收变更，组织规则与上下文，调用可替换的审查引擎，并把结构化结果安全、幂等地发布回代码托管平台。

产品不销售“一个会评论的机器人”，而是提供完整的企业审查闭环：

```text
连接代码平台 → 接收事件/指令 → 快速回执 → 异步审查
→ 人机协作处理发现 → 规则反馈与版本演进 → 用量/质量/审计闭环
```

### 1.1 阶段一：可靠执行与交互闭环

- GitHub/GitLab App 安装、仓库同步、webhook 正常化。
- PR/MR 自动触发和评论/`@openreview` 手动触发。
- 快速确认评论、异步状态更新、最终摘要与 inline findings。
- 持久任务、消息队列、幂等、重试、取消、替代和 DLQ。
- 任务列表、实时任务详情、基础组织/成员/角色、审计和运行健康。

### 1.2 阶段二：企业规则与 SaaS 管理闭环

- 规则集、版本、继承、绑定、审批、影响预览、灰度、回滚。
- 多租户隔离、配额、用量与成本归因、套餐能力门禁。
- 组织/团队/仓库管理、企业 SSO 接入、数据保留、密钥引用。
- 质量反馈、误报标记、规则命中趋势和治理审计。

### 1.3 非目标

- 不 fork 或修改 OpenCodeReview 核心来承载租户、计费或 UI。
- 不在阶段一/二构建通用项目管理、完整 IDE 或代码托管平台。
- 不保存或展示模型私有 chain-of-thought；只展示可验证的阶段、工具事件、输入摘要和结果。
- 不用用户个人 PAT 作为 SaaS 的默认凭据模型。
- 不把队列长度、消费者内存状态当作业务任务最终状态。

## 2. 用户与关键任务

| 角色 | 主要目标 | 关键权限 |
| --- | --- | --- |
| 组织所有者 | 创建组织、订阅、SSO、安全与全局治理 | 全局配置、成员与账单、删除/导出 |
| 平台管理员 | 连接 provider、排障、配置配额和运行策略 | 集成、任务干预、审计查看 |
| 安全/规则管理员 | 定义 mandatory 规则、审批版本、查看安全趋势 | 规则发布、例外审批、阻断策略 |
| 团队/仓库管理员 | 为范围绑定规则、配置触发与分支策略 | 仓库级配置、规则绑定 |
| 开发者/审查者 | 请求审查、理解 finding、修复或反馈误报 | 执行、取消自己的任务、反馈 |
| 审计/只读用户 | 查看历史、规则快照、行为和导出 | 只读、受控导出 |
| 财务/运营 | 查看额度、用量、成本与预测 | 用量和账单只读 |

## 3. 核心对象

- **Organization/Tenant**：安全、计费和数据隔离边界。
- **Team**：成员与策略分组，可选；仓库可以属于一个或多个逻辑团队。
- **Provider Installation**：某一 GitHub/GitLab 实例和安装身份。
- **Repository**：同步后的仓库镜像记录，不保存完整源码。
- **Review Request**：一次用户意图，例如“审查 PR #8421”；可以生成多个 attempt。
- **Review Run**：不可变输入上的一次实际执行。
- **Stage/Agent Run**：run 中的准备、分析、agent、发布等子阶段。
- **Finding**：规范化发现，具有稳定 fingerprint 和发布状态。
- **Rule Set/Version/Binding/Snapshot**：规则的定义、不可变版本、作用域绑定和单次运行快照。
- **Interaction**：评论、`@` 指令、按钮操作及其回执/更新记录。

## 4. 功能地图

### 4.1 组织、身份与权限

- 使用 Casdoor 作为 OIDC 身份提供者；平台只保存稳定 subject 与必要资料。
- 支持组织创建、邀请、成员停用、角色赋予、SCIM/企业目录作为后续扩展点。
- RBAC 基础角色：`owner`、`admin`、`rule_admin`、`reviewer`、`viewer`、`billing_viewer`。
- 高风险动作要求细粒度权限与二次确认：规则发布、安装解绑、任务强制取消、数据导出、密钥轮换。
- API key/service account 限定租户、仓库、动作和到期时间；只显示一次明文。

验收：任何跨租户 ID 枚举均返回资源不存在或无权访问；所有高风险动作写入审计日志。

### 4.2 GitHub/GitLab 连接

- 引导用户安装 GitHub App 或授权 GitLab 应用，展示最小权限说明。
- 同步仓库、默认分支、可见性和安装状态；支持 GitHub Enterprise/GitLab Self-Managed 的 base URL。
- 每个 installation 只存 `credential_ref`，令牌由凭据代理按需换取并短期缓存。
- 显示 webhook 最后成功时间、失败原因、权限缺失和续期入口。
- 删除安装时先停止新任务，再撤销 provider 授权，最后执行保留策略。

验收：重复 provider delivery 不产生第二个业务事件；未知或停用 installation 不进入队列。

### 4.3 触发策略

支持：

- PR/MR opened、reopened、synchronize/update、ready_for_review。
- 标签、目标分支、作者、草稿状态、文件路径和变更规模过滤。
- 手动按钮/API 触发。
- 评论命令或 mention 触发。
- 计划性回归审查作为后续扩展，不进入阶段一默认范围。

每个仓库可配置 `off | manual | automatic`，以及 `standard | deep | security` 默认模式。

### 4.4 评论与 `@openreview` 交互

统一命令语法：

```text
@openreview review [--mode standard|deep|security] [--rule <rule-set>]
@openreview status [<task-id>]
@openreview cancel [<task-id>]
@openreview retry [<task-id>]
@openreview explain <finding-id>
@openreview help
```

处理规则：

1. 验证 provider 签名、installation、actor 权限和仓库范围。
2. 解析命令；无效命令立即回复帮助，不创建任务。
3. 将 interaction、review request、初始 run 与 outbox 同事务提交。
4. 提交成功后，回执消费者发布或更新一条“已接收”评论。
5. 执行过程中最多按阶段或固定时间窗口合并更新，避免刷屏。
6. 完成、失败、取消或 superseded 后更新同一条状态评论，并发布独立 review/inline findings；失败文案只指向 task detail 的安全摘要，不暴露 provider、token 或原始运行错误。

回执目标：平台接收成功后 P95 5 秒内出现；高峰时回执队列不与审查执行队列竞争。

### 4.5 审查任务生命周期

用户可见状态：`Queued`、`Preparing`、`Reviewing`、`Publishing`、`Completed`、`Failed`、`Cancelled`、`Superseded`、`Needs attention`。

能力：

- 查询任务当前阶段、持续时间、attempt、规则快照、引擎版本和成本估算。
- SSE 实时订阅；断线后通过 `Last-Event-ID` 补发。
- 在可取消阶段发出 cancel；worker 在阶段边界与工具调用前检查。
- 对瞬时错误自动重试；用户可对可重试终态手动重试。
- 同一 PR 新 commit 到来时，未发布旧任务默认标记 `superseded`；正在发布时依靠 head SHA 防护避免陈旧结果覆盖。
- 管理员可重新入队 DLQ 项，但必须指定原因并产生审计事件。

### 4.6 审查执行与 findings

- 固定 base/head SHA，并在 checkout 后二次验证 head。
- 解析变更、过滤二进制/生成文件/超限文件，记录跳过原因。
- 按规则快照生成可信 OCR 规则文件，调用固定版本 OCR。
- 将 findings 规范化为严重级别、类别、位置、证据、建议、置信度和 fingerprint。
- 发布前执行去重、范围校验、head SHA 校验、最大评论数和敏感信息清理。
- 超过 inline 上限的 findings 进入摘要，不静默丢弃。
- 用户可标记 `resolved`、`useful`、`false_positive`、`won't_fix`，反馈关联规则版本。

### 4.7 企业规则

- 支持系统基线、组织、团队、仓库、分支、路径和单次运行层级。
- 规则有 draft、in_review、approved、published、deprecated、retired 生命周期。
- published 版本不可变；修改必定创建新版本。
- mandatory 规则不可被下层删除，只能通过有期限、有审批人的 exception 缩小范围。
- 发布前可运行语法校验、样本回放、影响预览、成本预测和误报抽样。
- 每次 run 记录解析后的完整快照与 SHA，确保审计和可重放。

详细设计见 [04-enterprise-rules-design.md](04-enterprise-rules-design.md)。

### 4.8 工作台与运营视图

首页优先展示：

- 当前审查流是否健康；
- 需要人处理的高价值事项；
- 正在运行/排队/近期完成的任务；
- provider、队列和预算的轻量状态。

不把首页设计为传统四 KPI + 大表格。需要批量操作时，进入专用列表页。

### 4.9 用量、额度和计费接口

- 按 tenant/repository/run/provider/model/rule-set 归因 token、OCR 时间、队列时间、存储和 provider API 调用。
- 支持软额度（告警）、硬额度（拒绝或降级）、并发数、单次最大变更和月度预算。
- admission 阶段在入队执行前检查额度并保留预算；终态结算实际用量。
- 计费适配器与执行解耦，支持 SaaS 订阅或私有部署关闭计费但保留计量。

### 4.10 审计、通知与数据治理

- 审计事件记录 actor、action、resource、before/after 摘要、request ID、IP、时间和结果。
- 通知策略支持 provider 评论、应用内通知、email/webhook 扩展；默认避免重复通知。
- raw webhook、checkout 临时目录、finding、运行日志、审计分别配置保留期。
- 用户可导出规则/审计/用量；源码默认不持久化，必要证据需显式策略授权。

## 5. 权限矩阵（阶段一/二）

| 动作 | Owner | Admin | Rule Admin | Reviewer | Viewer | Billing Viewer |
| --- | --- | --- | --- | --- | --- | --- |
| 管理组织/SSO | ✓ | 部分 | — | — | — | — |
| 管理 provider 安装 | ✓ | ✓ | — | — | — | — |
| 运行审查 | ✓ | ✓ | ✓ | ✓ | — | — |
| 取消自己的审查 | ✓ | ✓ | ✓ | ✓ | — | — |
| 强制取消任意任务 | ✓ | ✓ | — | — | — | — |
| 创建规则草稿 | ✓ | ✓ | ✓ | — | — | — |
| 发布 mandatory 规则 | ✓ | 按策略 | ✓ + 审批 | — | — | — |
| 查看代码 findings | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| 查看用量/账单 | ✓ | ✓ | — | — | — | ✓ |
| 导出审计 | ✓ | 按策略 | — | — | 受限 | — |

## 6. 产品级验收标准

- provider 重放同一 delivery 100 次，仅产生一个归一化事件和一个 review request。
- webhook 入口在持久化失败时不得返回成功；成功返回后，任务可在断电恢复后继续。
- comment 命令成功接收后 P95 5 秒内产生回执，审查执行拥塞不影响该目标。
- 任务状态只允许按定义状态机迁移，所有迁移带 revision 并可审计。
- 新 commit 到来后，旧 head 的 finding 不会发布到新 diff。
- 任意 published 规则版本内容不可原地修改；run 可还原当时实际生效规则。
- provider 写入重试不会重复创建摘要、inline comment 或状态评论。
- 任何用户界面均覆盖 loading、empty、partial、error、permission denied、stale 和 offline/reconnect 状态。
