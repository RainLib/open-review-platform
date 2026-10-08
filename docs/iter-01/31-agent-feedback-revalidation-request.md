# 新一轮 Agent 需求证据验证请求（用户已授权创建）

目的：在重试终止优化已部署、真实仓库隔离编码与固定验证器通过后，进行新的正式需求交付验证。旧 Issue #20、反馈任务及 Draft #26 保留，原 5/5 冻结预算不修改。用户已明确授权创建本轮需求。实际产物为 Issue #27，任务 1a1a7c80-867d-4b91-a175-35873804bb07，源 main / b50da27acbf01289e3990fdd863d9743babc7ce6。自动计划 revision 1、SHA 7c2a9fad76ef0ac89b470a4920aae85a7d0bb36b9f66f3c6525546b562a0af73 已通过 Owner 会话准确审批，首次正式执行成功交付 Draft #28。

## 新需求及源

从仓库当前 main 的准确 SHA `b50da27acbf01289e3990fdd863d9743babc7ce6` 创建独立验收请求。交付 `CriteriaVerified` 的严格一一对应检查，以及等数量畸形输入回归：未知状态、空白/未修剪/超长条件、空/短/NUL/超长证据均需实际触达字段校验。

验收条件沿用已固定验证器的三项语义：

1. Reject duplicate approved criteria and unapproved evidence so verification is a one-to-one mapping.
2. Reject missing, failed, malformed or duplicate verification results.
3. Keep valid complete passing evidence accepted.

## 可审批的执行范围

仅修改 `internal/domain/agent_workflow.go` 与 `internal/domain/agent_workflow_acceptance_test.go`；固定 Claude CLI 沙箱、现有 `aliyun/glm-5.3` 路由、原始模型预算、部署自有固定验证器。Agent 仅编辑源文件，可信 adapter 校验 Git 元数据、路径、secret/diff、独立三项验证后才提交独立分支并创建 Draft。需要新请求准确源验证、计划摘要 SHA 和 Owner 审批，不复用旧任务批准。

## 正式验收证据

需记录实际 attempt 回执、三个条件的独立证据、准确 Draft head 的自动再审及独立域测试。然后提交真实需求补充意见，生成有界反馈子任务、重新审批、修订同一 Draft 并在新 head 再审。最终需求接受由用户确认；不自动合并到 main。

现有隔离证明是诊断材料，不作为此新请求的执行回执。若模型再度拒绝，有限重试后保留精确原因并停止，不增加预算或无限创建新任务。

## 首次正式交付

真实 GitHub App Webhook 接收 Issue #27，Issue 分析 revision 2 完成并确认上下文充分。过程中发现历史模型路由 JSON 缺少新增 `max_concurrent_runs` 字段：入队路径使用兼容解码，但两个读取路径直接反序列化后丢失运行时默认值，造成模型不可用。已统一使用 `domain.DecodeModelRoute`，保留原始冻结 JSON 与哈希；隔离 PostgreSQL 上历史路由回读与编辑 supersession 两项集成测试通过，随后部署 issue-triager 并确认真实分析恢复。未调整模型、密钥或路由配置。

执行 attempt `5cbfc8d1-ff27-47d9-a906-4b445def7ee7` 成功。Docker 守护进程记录的隔离子容器顺序为初次编码退出 0、独立验证退出 1、自动修复编码退出 0、再次独立验证退出 0；中间测试原始输出未长期保留，因此证据证明失败后修复再验证的顺序，不宣称保留了首次失败的完整断言日志。整个过程仅消耗 1/5 共享任务执行次数。

Draft [#28](https://github.com/RainLib/open-review-platform/pull/28) head `a3edf61fa8703dacc9668d0cc10b773c700a93d5`，仅两个批准文件、9320 diff bytes。可信 publication checkpoint 保留三个独立条件的 passed 结果及输出 SHA `331f073e0e031235010e8f3185f6177ea02bd7aca7121b179fcbbd83ab807dc4`。准确 head 的手动独立固定镜像域测试 104 项通过，实际 GitHub 状态 `Acceptance / Go domain tests` 成功；该运行器不是 GitHub Actions。初次自动审查 run 为 `28a555fc-8205-4fc6-a65d-517c56354c07`。

完整机器可读首次交付证据见 [new-formal-initial-delivery.json](../../outputs/acceptance-20261007/new-formal-initial-delivery.json)。最终人类验收和合并尚未发生。

## 正式反馈修订与当前验收状态

首次 head 的审查 `28a555fc-8205-4fc6-a65d-517c56354c07` 完成，0 findings、门禁 success；准确 head 的独立 CI 通过后，Owner 会话实际提交 `changes_requested`，要求补充有效边界与 UTF-8 字节边界测试。这是覆盖范围补强，没有把已通过的行为虚构为缺陷。系统自动创建反馈子任务 `752e7811-060b-4bf0-a240-ae391233b8e0`，继承三项条件和共享分支；自动计划 revision 1、SHA `5717c34c0440427ebd3905b4fd07adca6232c364f0caa942046129aded98b1bf` 经 Owner 准确审批。

反馈 attempt `0729c89e-6239-44a4-898f-5803becf61d8` 在原有编码预算内完成编码和独立三项验证。仅在批准的测试文件增加 175 行，生产函数没有继续变动；同一 Draft #28 更新为 `59d409b71463f1e13c73657167a90f850ca871a1`。验证输出 SHA `1b12b6a2c379bcf1e2591c5538ab66afca0338ae33d7ab1352df9d02d7c6e059`、2791 bytes；三项独立验证全部 passed。

反馈补充测试实际覆盖：3/1000 字节条件、3/2000 字节证据的有效闭区间；多字节 UTF-8 的准确边界与越界；20 项不同条件及重排结果；21 项拒绝。越界条件在批准列表和结果中保持完全相同、数量相等，避免只命中匹配或数量校验。当前 head 的固定无网络 Go 容器取得 **117 个测试及子测试 pass 事件**、退出 0，发布真实 GitHub `Acceptance / Go domain tests` success；不是 Agent 自报，也不是 GitHub Actions。

## 真实发布确认故障及只读恢复

该反馈 attempt 已提交并推送新 head，但 `draft_publication` 的即时确认失败，最初记录 `agent_adapter_execution_failed`。当时没有保留足够具体的 Provider 原因，不能确认是网络、延迟或其他即时故障；当前 Provider 读回成功不能倒推历史原因。

此次故障暴露了一个闭环缺口：已有可信验证 checkpoint 和已更新的自有 Draft，却只能重新消耗编码次数来重试。已增加迁移 `000117_agent_publication_read_recovery.sql` 和有界只读确认恢复：检查已有 feedback Draft 的仓库、目标/来源分支、所有权标记、准确 head、签名 checkpoint、有效批准人与 installation，以及较新计划/attempt/子任务的 fencing；最多三次 Provider GET，不重放编码、提交、推送或 Draft 创建。即时 adapter 路径也会在完成 checkpoint 后先尝试一次只读确认。

隔离数据库实测确认、head 变化、外部 URL、失败条件、新计划、新子任务、批准撤销、installation 撤销、三次失败耗尽和最后一次读取崩溃等 10 个子用例通过；既有完整 Agent 工作流验收数据库回归通过。adapter 与 workflow 竞态测试通过，GitHub/GitLab 只读读取单元回归通过。GitLab 单元模拟不作为 GitLab 实机验收。

迁移 117 已在备份后部署。真实反馈 attempt 经 **一次只读 Provider 确认**恢复成功，任务 revision 7 completed；没有新编码 attempt，共享额度仍为 **2/5**。原失败回调和审计保留，恢复表记录原错误与 `exact_owned_draft_confirmed`，并追加恢复审计。

当前 head 的首次再审 `ce3dbbbe-db13-4eb2-b851-a4e7b773a4fe` 在原 5 分钟上限下自动尝试三次后超时失败，保留原失败记录。通过 Console 正常 `Retry review` 发起一次重审，`578d526d-d47f-4f75-8db6-1157b945ed4d` 在 2026-10-08 12:08:25 UTC 完成，**0 findings、门禁 success**；没有增加超时或编码预算。Provider observation 同时记录当前 head 的 Open Review success 和 independent CI success。

记录采集时 Console 状态为 **awaiting_acceptance**，三项人工验收证据已填写但尚未提交最终接受；冻结策略要求 Owner/Admin 最终逐项验收决定。PR #28 为 OPEN/Draft，未合并。当时运行修复源码和报告尚未提交推送，远端 main 为 `b50da27acbf01289e3990fdd863d9743babc7ce6`；后续源码交付与该 Draft 的人工接受是不同操作。完整机器收据见 [new-formal-acceptance-summary.json](../../outputs/acceptance-20261007/new-formal-acceptance-summary.json)，[页面证据](../../outputs/acceptance-20261007/new-formal-awaiting-acceptance.png)保留采集时状态。
