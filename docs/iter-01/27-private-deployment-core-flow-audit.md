# 私有化部署核心流程与 Agent 闭环核验

核验日期：2026 年 10 月 1 日，Asia/Shanghai。核验对象为当前 main 源码、正在运行的本地 Docker 部署、部署数据库和 GitHub 实际产物。购买、订阅、收款、多租户商业运营不作为验收要求。执行预算、仓库权限、任务超时和审计仍属于核心运行约束。

结论：基础 PR 审查已具备真实流程证据；规则治理的主要代码已实现，但实际工作区尚未使用自定义规则验证效果；Agent 已完成一次真实的人工计划和审批后执行、创建 Draft、审查的链路。当前不能认定 Agent 能自主完成业务任务并持续修复到需求验收通过。

本次只做代码审计、只读运行核验与隔离测试，没有修改运行策略、批准任务、调用付费模型、向 provider 写入或合并代码。旧的完成矩阵和 Agent 设计文档含有“未部署 adapter、没有真实 Draft”的历史结论；以下实时证据更新了该判断。

## 核心流程核验结果

| 流程 | 当前证据 | 判断与缺口 |
| --- | --- | --- |
| Webhook 和命令准入 | Provider 校验、幂等、安装和范围验证、冻结来源和策略、outbox/inbox 均有实现及测试。实际工作区保留真实 GitHub Review。 | 已有成功路径；目标部署的拒绝准入与故障演练需单独验收。 |
| PR 审查和回写 | 实际工作区保留 30 个 Review job，核验时没有 queued/running。Agent PR 的相同提交有 3 个 completed Review run；GitHub 当前 Check 为 success。 | 基础流程已跑通。Review completed 和无 AI finding 不能证明业务逻辑正确。 |
| 规则版本和治理 | 创建、审批、发布、绑定、不可变快照、例外到期、Test Lab、Shadow/Canary 和回滚代码及集成测试存在。 | 主要实现已具备，但实际工作区无自定义规则集或绑定，最新 5 个 Review 的规则来源数均为 0。 |
| Agent 来源与审批 | 来源 commit 冻结、Jev 判定、人工计划、按计划哈希批准、执行租约、取消和晚到回调防护存在。当前另有 Issue #19 等待计划批准。 | 属于受审批约束的执行流程。计划输入仍由人工提供。 |
| Agent 编码和 Draft 发布 | 真实 Issue #15 的第 4 个计划成功执行，Claude 修改 1 个文件、291 diff 字节，生成 Draft PR #16。 | 真实执行与发布链路已证明；任务只是新增 5 行 Markdown，尚无业务功能实现的验收证据。 |
| Agent 独立验证 | 编码后可以运行部署方配置的固定验证命令，并持久化 profile/output 哈希。 | 当前验证配置为空，成功 attempt 的验证哈希也为空。独立验证未参与这次真实任务。 |
| Draft 自动审查 | 实际仓库的覆盖策略已设置 review_drafts、automatic_review 和 rereview_on_push 为 true；PR #16 相同 SHA 有真实 Review。 | Draft 审查路径有真实结果。各次 Review 的自动触发或手动命令应按 trigger 进一步区分。 |
| 反馈修改和再审 | revise 创建子任务，重新读取 Draft head，冻结同一分支及预算，再经计划和批准。源代码及 provider fixture 覆盖正反路径。 | 当前反馈策略和历史父任务上限均为 0，反馈记录数为 0，真实反馈循环没有被接受。 |
| 任务需求验收 | adapter 返回 Draft 后 task 置为 completed；详情只读关联 exact SHA 的 Review。 | completed 表示 Draft 交付，尚无等待 CI、验收标准逐项判定、修复迭代、人工验收通过的独立生命周期。 |
| 故障恢复 | 来源/Jev 的暂时错误有有界重试；adapter 有磁盘回执、终态 callback 重投、发布检查点和重启后只读核对。 | 不能说“没有恢复”。但编码失败、超时、不能核实发布时进入 needs_attention，不自动重新编码；真实完整故障恢复演练缺失。 |

## 真实 Agent 证据的范围

[Issue #15](https://github.com/RainLib/open-review-platform/issues/15) 对应的 [Draft PR #16](https://github.com/RainLib/open-review-platform/pull/16) 在本次 GitHub 读取中仍为 open、draft，未合并。提交为 `8083c764190a9215d37053bd67100161564e927e`；`Open Review / Analysis` 和 `go` 两个 Check 均为 completed/success。

PR 只新增 `docs/agent-smoke-20260927.md`，共 5 行。其正文明确说 adapter 只完成路径、变更预算、空白和基本 secret-pattern 检查，没有证明 build、tests、SAST、性能、UI 或迁移。外部 Go Check 的通过与 adapter 独立验证命令是两类证据。GitHub 当前 main 保护要求 `Open Review / Analysis`，`enforce_admins=false`；这证明已有必需审查配置，但管理员仍有绕过边界，且 Go Check 未在该 contexts 列表中。

数据库中同一 Issue 的前 3 个 attempt 均为 needs_attention，第 4 个为 succeeded；保留了 4 次计划批准。这证明失败可被人工重新计划和批准后恢复，不证明 Agent 自动诊断、修复和重试。当前只有 1 个 completed Agent task、1 个 publication checkpoint、0 个反馈循环。

正在运行的 adapter 使用 Docker 每任务沙箱，`AGENT_ADAPTER_ALLOW_UNSANDBOXED_DEVELOPMENT=false`；runner 心跳报告 adapter 已配置且可达，凭据 broker 和来源 worker 也存活。部署仍为 development 模式，私网 broker 使用 HTTP。本次没有进行运行镜像和源码逐二进制一致性核验、实际编码隔离攻击测试或目标部署的 TLS 验收，不能把单元测试扩大为这些结论。

## 应优先补齐的闭环

### 1 Agent 自动生成可审核计划

当前 Console 的 objective、scope、verification、risks、unknowns 都是人工填写后提交。Jev 仅提供有界准入选择，代码明确不把模型判定文字用作计划或授权。

若目标是“Agent 自我完成任务”，应增加读取固定来源、提取 Issue 验收标准、生成结构化计划的 worker。计划必须保留来源 SHA、允许路径、验证方法和未知项，仍沿用现有审批及内容哈希。计划生成自动化不要求自动批准或自动合并。

### 2 把验证结果接入有界修复

先为一个实际仓库配置固定镜像和验证命令，证明通过路径及失败阻止 push/Draft 的路径。再实现验证失败后的证据反馈、同一范围内的有限修复、复验和预算耗尽状态。

当前固定验证命令只会在失败时结束 pipeline；模型 CLI 在一次执行中可能自行检查和修复，但平台没有保证“测试失败后继续修复直到通过”。需要将命令结果、Review finding 和 CI 结果分别作为带 exact SHA 的输入，不能只相信 Agent 自述完成。

修复循环还应有跨计划、执行和反馈的累计预算。当前 `max_attempts` 在 claim 时约束同一计划执行记录的 lease reclaim；重新创建并批准新计划可以生成新的 attempt，从 1 重新计数。真实任务配置为 1 仍保留 4 个获批计划和执行记录，符合这段代码的语义。它不能被解释为整个 Issue 最多执行 1 次；自动化前应明确并实现任务总次数、总时间或总模型用量上限。

### 3 完成真实反馈修改和再审

当前 `max_feedback_cycles=0`，修改仓库策略后也不能扩大已接收父任务的冻结预算。应在新任务准入前明确设置非零上限，用新任务验证：首次 Draft → 获授权的 revise → 新计划和批准 → 同一 Draft 的新提交 → 新 SHA 的 Review/CI。

现有 revise 能力属于人工反馈入口，并非 Review 或 CI 失败自动触发修复。若需要自动修复，应单独实现受原计划、路径、次数和时间限制的触发策略。

### 4 区分 Draft 交付与需求完成

现在 adapter 成功就令任务 completed，不等待后续 Review 和验收。建议增加独立的交付与验收状态，例如 Draft 已交付、检查中、等待修复、等待人工验收、验收通过、人工关闭。具体命名可沿现有领域模型确定。

每项验收标准应关联证据及其提交 SHA；PR 关闭、合并、CI 失败、Review 阻断或新提交应更新对应状态。人工合并可以保留，闭环并不要求 Agent 自动合并。

### 5 在实际工作区验证规则效果

当前实际工作区没有自定义规则。全库 10 个 published 版本及相关绑定来自测试工作区；全库 Test Lab 和 Rollout 记录均为 0。不能用这些记录宣称实际规则治理已接受。

最低验收应包含一组可稳定触发的违规样例及修复样例：创建规则 → 独立批准 → 发布和精确仓库绑定 → Test Lab → 实际违规 Review → provider 阻断 → 修复后新 SHA 通过。随后修改规则并重试旧 run，证明旧 run 仍使用原快照；再验证例外到期及绑定回滚。

规则编译器有确定性合并和 mandatory 防覆盖语义，但 OCR adapter 把实际规则内容转换为 AI prompt。最终严重级别门禁是对保存后的 finding 作确定性判断，不能保证 AI 一定识别违规。测试退出状态、secret scanner、许可或覆盖率等硬要求，需要独立工具及 provider 必需检查承接。

设计文档中的组织/团队多层作用域、按规则风险强制审批组合也不等于全部已实现。当前主要绑定范围为 tenant/repository，审批人数由请求提供并默认为 1。个人私有部署可以先使用现有范围和审批，复杂组织治理不必成为当前阻塞项。

## 运行恢复与证据质量

RabbitMQ 普通执行队列核验时无 ready/unacknowledged 消息，Review 死信队列有 76 条。数据库全局有 11 个 queued 和 4 个 running job，但主要属于历史集成测试工作区；实际 `rainlib-open-review` 工作区无待执行 Review。这些数据需要按来源核对，不能直接认定真实任务卡死，也不应不加判断地重新投递历史消息。

上线接受前需清晰区分测试数据和真实数据，并对一项真正任务演练 worker 中断、callback 丢失、provider 接受写入后断连、取消与晚到回调。验收要证明同一 attempt/branch/Draft 身份不重复、恢复后能给操作者明确处理结论。

单节点私有化部署无需为了本次闭环先实现商业账单或多副本 SaaS 调度。持久化备份恢复、运行告警和真实任务的超时/失败可见性仍应列入运维验收。

## 本次测试结果

| 检查 | 本次结果 | 证据边界 |
| --- | --- | --- |
| go test ./... | 1012 个 test/subtest pass，115 个 skip，无失败 | 未设置 DB、AMQP 和付费模型等专用验收环境的测试会 skip。 |
| 新 PostgreSQL 16 完整迁移 | 通过，迁移至 000113 | 使用专门的一次性测试容器，无生产数据。 |
| store、agentcredentials、governance、issuepublisher、terminal 集成套件 | 252 个 test/subtest pass，1 个 fail，无 skip | Provider Check 测试领取了其他测试留下的 completed run，存在套件隔离缺陷，不能称整套绿色。 |
| 失败的 Provider Check 用例 | 在另一全新迁移数据库独立运行通过 | 支持测试残留干扰的判断，不将原套件失败隐藏或计作整套通过。 |
| 前端与脚本 Node 测试 | 115/115 通过 | Node 22 需要 experimental-strip-types；首次缺少该参数的加载失败是测试命令问题。 |
| Web typecheck、lint、production build | 均通过 | 未在本次重新登录浏览器完成 UI 到 provider 的操作链。 |
| go vet ./... | 通过 | 静态检查。 |
| rules、agentdecision、agentadapter、agent-task-runner race 测试 | 通过 | CLI、Linux 和专门沙箱用例仍遵守其 opt-in/平台条件。 |
| GitHub Draft 和 exact SHA Check 读取 | 通过 | 只读核对历史真实产物；本次未创建新业务任务。 |

## 建议的下一轮验收

选择一个真实的小型代码缺陷，要求改动代码并新增有意义的回归测试。在新任务准入前配置独立验证命令、非零反馈预算和 Draft 自动审查策略。

验收标准为：Issue 的验收条件进入结构化计划；批准后 Agent 改代码；故意失败一次的测试驱动修复；相同提交的独立验证通过后发布唯一 Draft；一次真实 revise 更新同一 Draft；新提交重新审查；最后人工确认需求通过，并记录任务验收状态。另用违规及修复两个样例验证实际规则和 provider 合并门禁。

达到上述结果，才能把“执行与发布打通”升级为“任务交付闭环已接受”。GitLab 编码、自托管 HTTPS 或完全断网执行，只有部署实际需要时再分别补充验收，不能用 GitHub 文档任务成功替代。

## 主要代码依据

- `internal/store/agent_tasks.go`：计划创建和批准、租约、adapter callback、反馈准入、终态 provider 文案。
- `apps/web/components/console/agent-work-manager.tsx`：人工结构化计划编辑及审批。
- `internal/agentdecision/backend.go`：Jev 有界准入输出，不作为计划或授权。
- `internal/agentadapter/pipeline.go`：编码、可选独立验证、来源和租约复核、变更预算、push 及 Draft。
- `internal/agentadapter/service.go`、`receipts.go`：磁盘回执、重启恢复、检查点核对与 callback 重投。
- `internal/rules/compiler.go`、`ocr.go`：确定性治理编译与 AI prompt 转换。
- `internal/store/rules.go`、`rule_tests.go`、`rule_rollouts.go`：规则治理、实验与灰度。
- `internal/publisher/merge_gate.go`：基于 finding 严重级别的 gate，实际阻断还取决于 provider 保护规则。
- `internal/store/provider_checks_integration_test.go`：本次整套集成测试的隔离问题。
