# Agent 剩余闭环：恢复、CI 修复与逐项验证

日期：2026-10-01。本轮延续 [前次交付闭环](28-agent-delivery-workflow-closure.md)，范围为私有化部署，不包含购买和订阅。

## 本轮补齐的行为

| 原缺口 | 当前行为 |
| --- | --- |
| 人工退回只记录状态 | owner/admin 的 changes_requested 生成绑定决策版本和提交的内部修复候选；无需另发 provider 评论。Draft 尚未获得新鲜观察时等待观察，随后准备候选。 |
| 远端 CI 失败无法进入修复 | 完整、当前 SHA 的独立 CI 中，明确的代码或测试失败及有界诊断驱动修复；基础设施、未知、取消、超时、待完成和诊断缺失均显示原因并停止自动编码。 |
| 临时故障覆盖验收决定 | 人工决定单独保存。相同 SHA 的 provider、审查与 CI 证据恢复后，恢复原 accepted/changes_requested；不同 SHA 仍 superseded，不继承接受。 |
| CI 观察失败后无法明确恢复 | 新增 owner/admin 的 Refresh independent CI 和 retry-checks API，仅重新排队 provider 只读观察。旧版本、重复活跃请求、安装撤销及权限不足被拒绝。 |
| 长需求被规划预览截断 | 冻结完整来源正文，加入批准摘要和哈希。修订计划不能删除或替换原需求和仓库证据。 |
| 计划只靠需求正文 | 在来源已验证的完整 SHA 上只读取得仓库清单和最多三个相关文本片段，过滤凭据路径、符号链接及已识别凭据；明确标记截断、遗漏和部分清单，不推断已检查整个仓库。 |
| 通用测试通过等同需求完成 | 可启用 require_criterion_evidence，要求固定独立验证器逐条返回批准条件的通过结果和证据。退出码 0、缺项、替换条件或编码 Agent 的自述都不足以通过。 |

内部修复复用原 Draft、分支、路径限制、完整原始需求和累计执行预算。审查、CI、人工退回使用各自的稳定来源键去重；一个分支存在活跃反馈任务时停止准备第二个任务。即使 provider 人工反馈次数设置为 0，内部 workflow 修复仍按自己的深度和分支预算工作。

修复候选必须重新核对 provider 来源、生成新计划并获得新的 owner/admin 批准。它不会自动取得新的写权限。原批准内的本地验证失败仍可按冻结预算自动修复和再验证。

## 逐项独立验证契约

新仓库策略的 Console 可选择 **Require independent evidence for every acceptance criterion**。历史策略默认为 false；策略及条件在任务准入时冻结，编辑仓库策略不会扩大旧任务权限。

固定验证命令仍由部署所有者配置，禁止由仓库或模型选择命令。验证器从 stdin 接收：

```json
{"acceptance_criteria":["Retry twice"]}
```

执行具体断言后，验证器在 stdout 输出每项一行：

```text
OPENREVIEW_CRITERION_RESULT {"criterion":"Retry twice","status":"passed","evidence":"TestRetryLimits passed"}
```

criterion 必须逐字匹配批准的条件；status 为 passed 或 failed，evidence 是具体验证证据。重复、额外字段、尾随 JSON、超限和无效内容被拒绝。严格策略要求恰好覆盖所有批准条件。单项 failed 或严格策略缺少通过证据，会进入有界修复；固定 profile 配置 criterion_report_required=true 而根本没有报告时，判定验证器不可用并停止。

结果随独立验证 profile 哈希、完整输出哈希、补丁哈希和精确 SHA 保存到持久回执，并通过签名事件传回控制面。回执恢复、重复回调、发布 checkpoint 和最终完成必须保持一致；最终人工接受再次读取同一提交的证据。Console 展示逐项结果，最终需求验收仍由 owner/admin 提交证据和决定。

计划保存和 adapter 提交接口使用有界 1 MiB JSON 请求上限，容纳完整正文、仓库证据和 JSON 转义；字段与规范化摘要仍按各自的较小上限验证。其他控制面接口保留原来的请求上限。

部署所有者需要为每个仓库配置真正执行需求断言的可信包装命令。仅打印通过标记不能证明产品行为，平台不会自动推断通用测试覆盖了需求。

## CI 诊断与来源边界

provider-prober 在独立失败检查上读取 GitHub Check Run 的输出及有界 annotations，或 GitLab 失败 job 的有界 trace。API 调用使用冻结的安装凭据及固定 provider API 源，不跟随日志中的链接或重定向；解析和保存前脱敏安装 token 与已识别凭据。接口依据：[GitHub annotations API](https://docs.github.com/en/rest/checks/runs#list-check-run-annotations)、[GitLab job trace API](https://docs.gitlab.com/api/jobs/#get-a-log-file)。

最多读取 20 条 GitHub annotations、每个 GitLab pipeline 最多五个失败 job，每份诊断最多 12,000 bytes。超限、读取失败、没有可归因日志的 commit status，以及不明确的失败均保留人工诊断入口，不猜测修复内容。故障类别是保守信号分类，所有诊断仍是无权扩展执行范围的不可信数据。

PR/MR 已变为非 Draft 时继续只读审查和验收观察，但不再准备要求 Draft 写权限的自动修复。

## 升级与启用

1. 应用 000115_agent_workflow_recovery.sql，并协调升级 core、source-admitter、provider-prober、runner、adapter 与 Console。迁移已在一次性 PostgreSQL 16 上验证，未应用到实际部署。
2. 现有 workflow 可使用人工退回和 CI 修复；确认安装有效、仓库策略为 Manual、workflow 开启，且审查门禁和独立 CI 已正确配置。
3. 若需要严格逐项证明，为固定验证 profile 配置可信断言包装命令，可设置 criterion_report_required=true；随后在仓库 workflow 勾选逐项证据要求，为新需求创建任务。
4. 普通 go test 等命令不会自动生成逐项报告。开启严格要求前必须先配置验证器，否则任务会按冻结预算修复或停止。
5. 迁移保留旧内部审查绑定和历史验证回执。历史任务没有本轮新增的完整正文、仓库片段和逐项证明，不能反推其具备这些证据；旧逻辑已经丢失的历史决策也不会被迁移猜测重建。

## 本轮验证

| 检查 | 结果与证据范围 |
| --- | --- |
| Go 全套 | 1,059 test/subtest pass；119 skip；无失败。跳过项包含需外部环境的测试，另有以下补充验证。 |
| 隔离 PostgreSQL 集成套件 | store、agentcredentials、governance、issuepublisher、terminal 共 257 test/subtest pass，0 fail、0 skip；每个队列用例使用独立数据库。 |
| 核心包 race 与 DB workflow/repair race | 通过，覆盖规划、来源、provider、adapter、API、domain、恢复及修复事务。 |
| Linux root 专用测试 | 三项通过：子进程 UID/权限与父进程凭据隔离、编码模型 broker、独立验证参数及逐项 stdin/回执。使用一次性 Linux 容器、模拟 CLI/Docker 命令和临时工作区；不是实际模型或真实嵌套 Docker 验证。 |
| 前端 | 115 tests、typecheck、lint、生产 build 通过。包含 retry-checks BFF；未代替已登录页面的真实操作验收。 |
| 静态检查 | go vet ./... 和 git diff --check 通过。 |

数据库用例实际覆盖人工退回候选、反馈次数为零、重复观察去重、新批准、拒绝错误角色/旧版本、CI 的代码/基础设施/待完成/缺失诊断分支、原需求继承、预算停止、验收证据篡改及 accepted 在临时故障后的恢复。HTTP 合约用例覆盖固定 SHA、日志脱敏和重定向拒绝。

本轮没有升级正在运行的服务，没有触发真实 provider 写操作、真实模型编码或真实 Linux 沙箱命令，也未执行生产规则违规/修复样例验收。代码与隔离状态链路已补齐；部署后的完整验收仍须验证：真实规则生效及合并门禁阻断 → Agent 编码 → 验证失败及修复 → 唯一 Draft → 真实 CI 失败修复或人工退回 → 新计划批准 → 更新同一 Draft → 新 SHA 再审及独立 CI 通过 → 逐项需求接受。不得把本地绿色测试替代这一真实链路。
