# 10 — 参考架构经验与独立实现边界

## 1. 目的

本设计吸收成熟 AI 代码审查系统的工程经验，但不复制其专有实现、品牌、文案或企业授权逻辑。所有能力都在 Open Review Platform 中以独立领域模型、Go/Kratos 代码、公开协议和本项目许可证重新实现。

## 2. 从 Kodus 社区代码吸收的经验

### 2.1 可靠入队

Kodus 的 workflow 基础设施展示了 job 与 outbox 的组合，以及 consumer 侧 claim/release 思路：

- [workflow job queue service](https://github.com/kodustech/kodus-ai/blob/main/libs/core/workflow/infrastructure/workflow-job-queue.service.ts)
- [workflow job consumer service](https://github.com/kodustech/kodus-ai/blob/main/libs/core/workflow/infrastructure/workflow-job-consumer.service.ts)

本项目采用的结论：领域状态、审计和 outbox 同事务；消费者用 inbox + lease 幂等执行。区别是本项目明确把 PostgreSQL 定义为业务真相，并建立公开 task revision/SSE 契约。

### 2.2 队列隔离和延迟重试

Kodus 对 quorum/DLX、delayed exchange 和不同 workflow queue 的处理说明，审查执行、重试和维护任务不应共用一个无差别队列：

- [workflow queue arguments](https://github.com/kodustech/kodus-ai/blob/main/libs/core/workflow/infrastructure/workflow-queue-arguments.ts)
- [RabbitMQ topology](https://github.com/kodustech/kodus-ai/blob/main/libs/core/infrastructure/queue/config/rabbitmq-topology.config.ts)

本项目采用的结论：ACK、plan、standard/deep/security execute、provider publish、usage 分队列；DLQ 与 retry tier 明确。区别是物理队列数量保持有限，租户公平主要由 admission/scheduler 完成。

### 2.3 流水线和多 agent 编排

Kodus 将 code review 表达为有顺序的 pipeline，并在审查阶段编排多个 agent：

- [code review pipeline strategy](https://github.com/kodustech/kodus-ai/blob/main/libs/code-review/pipeline/strategy/code-review-pipeline.strategy.ts)
- [review orchestrator](https://github.com/kodustech/kodus-ai/blob/main/libs/code-review/infrastructure/agents/review-orchestrator.service.ts)

本项目采用的结论：外部任务状态保持少量、稳定、可恢复；多 agent 只作为 `reviewing` 内部执行图。区别是每个 agent 输出都先规范化、去重并通过确定性发布门禁，UI 不展示私有推理。

### 2.4 评论/mention 触发与初始状态评论

Kodus 支持 GitHub `issue_comment` 类事件、`@kody` 指令和初始评论阶段。这验证了“先让用户看到平台已经工作，再完成长审查”的产品价值：

- [GitHub webhook controller](https://github.com/kodustech/kodus-ai/blob/main/apps/webhooks/src/controllers/github.controller.ts)
- [GitHub pull request handler](https://github.com/kodustech/kodus-ai/blob/main/libs/platform/infrastructure/webhooks/github/githubPullRequest.handler.ts)
- [initial comment stage](https://github.com/kodustech/kodus-ai/blob/main/libs/code-review/pipeline/stages/initial-comment.stage.ts)

本项目采用的结论：User ACK 是独立高优先级任务，并更新同一条状态评论。区别是 HTTP `202` 只能在数据库事务提交后返回，不把进程内 `setImmediate`/fire-and-forget 当作可靠持久化边界。

## 3. 从 OpenCodeReview 吸收的引擎边界

OCR 是审查引擎，不负责 SaaS 的租户、身份、计费、审计和 provider App 生命周期：

- [OpenCodeReview repository](https://github.com/alibaba/open-code-review)
- [OCR rule skill](https://github.com/alibaba/open-code-review/blob/main/plugins/open-code-review/skills/open-code-review/SKILL.md)
- [OCR GitHub Action](https://github.com/alibaba/open-code-review/blob/main/action.yml)

本项目通过固定版本 CLI/容器、结构化结果、可信 `--rule` 文件和 adapter contract 使用 OCR。focused/critical 计划先在临时 worktree 生成仅包含 selected paths 的 base-rooted commit，再交给 OCR；不把大量 deferred paths 拼成模型侧排除参数。模型上下文耗尽属于确定性终态，不对相同输入自动重试。若未来更换引擎，task、rule snapshot、finding 和 publisher 的外部契约保持不变。

## 4. 明确不照搬

- 不复制 Kodus 企业版、闭源模块、许可证校验或云 token 流程。
- 不把第三方内部类名、队列名、数据库结构当作本项目 API。
- 不要求用户持有 Kodus token；Open Review 使用自己的身份、凭据和 entitlement。
- 不将 OCR 仓库内规则未经验证地直接作为企业可信规则。
- 不把某个模型/agent 框架锁死在公开 API 中。
- 不以“功能看起来相似”替代独立威胁模型、状态机和验收测试。

## 5. 采用/调整/拒绝矩阵

| 模式 | 结论 | 本项目处理 |
| --- | --- | --- |
| Outbox/Inbox | 采用 | PostgreSQL 权威、显式 revision/lease |
| 多队列隔离 | 采用 | ACK/execute/publish/usage 分离 |
| 延迟重试/DLQ | 采用 | 插件可选，TTL/DLX 兼容路径 |
| 线性 pipeline | 调整 | 外部稳定阶段 + 内部可并行图 |
| 多 agent | 调整 | 可选、预算化、结构化合并 |
| 初始评论 | 采用 | 高优先级 user ACK，幂等 upsert |
| fire-and-forget 入站 | 拒绝 | 必须事务持久化后才返回 202 |
| PR head 规则直接生效 | 拒绝 | 可信 snapshot/base branch policy |
| 私有推理展示 | 拒绝 | 只展示可验证 signals |
| 第三方企业 token | 拒绝 | 独立 entitlement 与凭据体系 |

## 6. 法律与品牌边界

- 在发布前记录所有第三方依赖许可证与版本，生成 SBOM 和 notices。
- 产品 UI、域名、机器人名称和文案使用 Open Review 自有品牌。
- 对“兼容 OCR”使用事实性描述，不暗示阿里或 Kodus 背书。
- 新代码由本项目独立提交；若确需移植开源片段，必须单独做许可证审查、保留 notice 并在 PR 中标明来源。
