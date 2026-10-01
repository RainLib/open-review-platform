# Agent 交付闭环：实现与验证

日期：2026-10-01。范围为私有化部署中的 Agent 自动计划、验证、修复、再审和需求验收，不包含购买、支付或订阅功能。

代码实现已接通以下流程：

```text
验证 Issue 来源和冻结的源码提交
  → 自动生成结构化计划、冻结验收条件
  → owner/admin 批准具体计划
  → 隔离 Agent 编码
  → 固定独立验证命令
      失败 → 在原计划、路径、截止时间和同一模型预算内修复 → 再验证
  → 保存验证及补丁证据 → 唯一 Draft 交付
  → 读取 provider 当前 head、分支和目标分支 → 对该 SHA 发起普通审查
      阻断 findings → 自动准备绑定审查结果的修复任务
                  → 重新核对同一 Draft → 自动生成新计划
                  → 新计划批准 → 修复、独立验证、更新同一 Draft → 新 SHA 再审
  → 当前 SHA 的启用合并门禁和独立 CI 通过
  → owner/admin 对每条原始需求提交验收证据
  → accepted，保留操作者、提交、证据和审计记录
```

## 交付与验收是不同状态

兼容历史数据，`agent_tasks.state=completed` 仍表示 Draft 已交付。Console 将其显示为 **Draft delivered**。新增 `agent_task_acceptances` 单独记录 `reviewing`、`checks_failed`、`needs_attention`、`awaiting_acceptance`、`accepted`、`changes_requested` 和 `superseded`。

验收属于一个确定的 attempt 和 SHA。provider head 改变、安装撤销、审查失败、CI 证据缺失或过期，都不能推断需求已经通过。提交变化后，旧验收不能被新提交继承。

最终验收检查当前安装、短期 provider head 观察、独立验证回执、完成的同 SHA 审查、启用且通过的合并门禁、完整且新鲜的独立 CI 快照、最新修复任务和乐观版本号。接受必须对每条验收条件提供证据；viewer/reviewer 不能代替 owner/admin 接受。

provider 状态回写通过原有 outbox。验收回执使用每次决策独立的标记，记录具体时间和 SHA；延迟发布的历史决策不会替换 Draft 的当前交付状态。

## 计划和修复约束

- 仓库策略新增 opt-in `workflow`，默认关闭，仅影响新任务。数据库冻结每个任务的策略，反馈任务继承父任务，策略编辑不能扩大已准入任务的预算。
- 默认从经过验证的需求和源码 SHA 生成结构化计划草案，明确标注尚待检查的代码路径。可选的固定 HTTPS 模型可以生成更详细的草案；来源验收条件由系统保留，模型不能删减。默认生成器没有提前检查整个代码仓库，也不声称测试已通过。
- `agent_task_requirements` 冻结原始验收条件。手工修订计划不能删掉这些条件，反馈和自动审查修复任务必须继承全部原条件。
- `max_repair_cycles` 为 0–3；`max_task_attempts` 为 1–5，累计同一 Agent 分支所有计划、反馈和前置租约重试，不能通过新计划重置。达到预算会停止自动执行并给出明确提示。
- 独立验证使用部署所有者固定的、精确匹配仓库的 profile。workflow 开启后必须配置 profile。普通命令失败可驱动修复；超时、Git 元数据或补丁篡改、清理失败、输出超限，以及 Docker 启动失败都停止执行。
- 每次修复重新检查 Git 元数据、路径白名单、秘密扫描、文件数和 diff 大小。初次编码与修复共用一个 job model broker，模型预算不随修复次数重置。
- 完成的阻断审查自动生成一次修复候选，绑定审查 run、当前 SHA、诊断摘要和原 Agent 分支。系统没有伪造 provider 用户评论。新任务仍重新读取 Draft，并需要新的 owner/admin 计划批准。
- 自动审查修复最多经过三层反馈深度，并继续受分支总 attempt 预算约束。存在人工反馈任务、安装/策略关闭、预算耗尽、诊断缺失或过大、硬规则拒绝时，会显示阻断原因。
- 远端 CI 失败、审查服务失败或诊断无法完整获取时，不自动猜测修复内容。可通过现有反馈或审查恢复入口处理。

## 再审与 CI

交付观察器运行于 `provider-prober`，使用独立租约与 heartbeat，只读 provider 元数据。它核对仓库、分支、目标分支和完整 SHA，使用普通 manual review admission 为已批准 workflow 的 Draft 发起审查；不会绕过整体审查关闭、安装撤销或工作区准入条件。webhook 与该观察器竞态时复用已有同提交审查。

要求至少一个 `origin=independent` 的成功检查；Open Review 自己的成功状态不能满足独立 CI。配置了 `required_checks` 时，所有指定名称必须存在并通过，所有已观察到的独立检查也必须通过。截断、未知来源、错误 SHA 和过期快照都不能接受。

活跃 workflow 的 CI 会在普通审查的 24 小时观察窗口之后继续刷新，避免晚验收任务永久停在过期证据上。

## 启用步骤

1. 升级 core、source-admitter、provider-prober、runner、adapter 和 Console；先应用 `000114_agent_delivery_workflow.sql`。部署组件应使用兼容版本，再开启新策略。
2. 通过现有 `docker-compose.agent-verification.yml` 挂载私有的 0600 验证 profile。镜像必须固定，命令为部署所有者配置的绝对路径 argv，且依赖需支持无网络验证。
3. 在具体仓库的 Agent policy 中选择 Manual，开启 **Task delivery workflow**，配置修复次数、分支累计 attempt 和独立 CI 名称。
4. 确认该仓库合并门禁已启用，provider CI 正确标识为独立来源。
5. 为当前需求创建新任务。历史任务不会被自动扩大权限或改造成新 workflow。

可选规划模型环境变量：`AGENT_PLANNER_API_BASE_URL`、`AGENT_PLANNER_API_KEY`、`AGENT_PLANNER_MODEL`。它们独立于 Jev 准入判断和 coding model broker；全空时使用需求来源生成器。

## 验证结果与证据边界

| 检查 | 结果 | 能证明的内容 |
| --- | --- | --- |
| Go 全套 | 1,049 个 test/subtest pass，117 skip | 代码与默认可运行测试；DB、真实 CLI/model/provider 及平台专用条件仍有边界 |
| 一次性 PostgreSQL 16 迁移 | 迁移至 000114 成功 | 新表、冻结触发器和旧数据默认兼容 |
| store、agentcredentials、governance、issuepublisher、terminal 隔离集成套件 | 255 个 test/subtest pass，0 fail、0 skip | 数据库状态、准入、权限、租约、回执、再审、预算和验收链路 |
| 修复循环 | 通过 | 真实临时 Git 仓库中验证失败后修复、再验证、预算停止、越界和元数据篡改拒绝；编码与验证函数使用受控替代实现 |
| 自动审查修复 | 通过 | 仅创建一次、绑定准确 run/head、拒绝篡改诊断、继承原条件、要求新的批准 |
| provider 与规划模型 HTTP 合约 | 通过 | 测试服务器上的当前 SHA、仓库/目标分支绑定、重定向拒绝、撤销和模型输出校验 |
| 相关 adapter/source/workflow/domain/API race 与 DB workflow race | 通过 | 本地并发与租约路径 |
| 前端 | 115 tests、typecheck、lint、生产 build 通过 | 数据判断、类型、路由与构建；没有代替真实已登录页面验收 |
| `go vet ./...`、`git diff --check` | 通过 | 静态检查及补丁格式 |

前次审计的 Provider Check 用例领取其他测试残留 completed run 的问题，已通过该队列测试专用数据库修复。本轮整套数据库测试通过；前次审计文档保留原结果。

本轮没有升级正在运行的部署，也没有运行真实模型编码、真实 provider 写操作或真实 Linux 沙箱验证命令。逻辑层的集成验证不等于线上需求验收。

部署后仍应使用一个真实代码缺陷做验收：故意失败一次验证并触发修复、交付唯一 Draft、触发一次真实阻断再审、批准自动准备的修复计划、更新同一 Draft、新 SHA 再审与独立 CI 通过、最后逐项记录需求验收。实际规则发布/Rule Test Lab/合并门禁也需使用违规和修复样例单独验证，不能由手工构造的 gate fixture 替代。

## 主要实现位置

- `internal/agentplan/`：需求计划与可选模型输出约束。
- `internal/agentadapter/repair.go`、`pipeline.go`、`verification.go`：固定验证、受限修复和共享模型预算。
- `internal/store/agent_workflow.go`、`agent_review_repair.go`：原始需求、预算、交付观察、修复候选及验收事务。
- `internal/agenttasksource/`：内部审查修复诊断绑定与 Draft 再核对。
- `internal/agentworkflow/`、`cmd/provider-prober/`：provider 只读观察与普通再审准入。
- `internal/api/agent_workflow.go`、Console acceptance BFF 与 `agent-work-manager.tsx`：逐项验收、权限和状态展示。
