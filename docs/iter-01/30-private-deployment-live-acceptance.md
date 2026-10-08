# 私有化部署核心链路实机验收

验收日期：2026-10-07 至 2026-10-08（Asia/Shanghai）。范围为当前 GitHub 部署、规则治理和 Agent 需求交付；购买、订阅和收费不属于本次范围。

最新结论（2026-10-08）：新的 Issue #27 → Draft #28 已完成真实计划审批、验证失败后修复、初次交付、人工要求修改、反馈修订、准确 head 再审与独立 CI，以及不重复编码的发布确认恢复；共享执行额度为 2/5。**最终需求状态仍为 awaiting_acceptance，Draft 未合并**。核心界面国际化与部署已有记录，修复版未保存输入的浏览器复验等待重新登录。当前待验收边界见文末及 [国际化记录](32-console-workflow-i18n-validation.md)。

下表及后续追加章节保留逐轮历史：旧 Issue #20 / Draft #26 的五次累计执行为一次成功、四次待处理，额度耗尽记录未修改。收据中的 main SHA、候选镜像、未提交标记均是采集时的快照；源码后续提交不会改写原失败或自动接受需求。公开归档选取见 [证据索引](../../outputs/README.md)，其余本地原始资料保留在验收环境。

## 首轮验收结果（Issue #20 / Draft #26）

| 链路 | 实机结果 | 证据与边界 |
| --- | --- | --- |
| HTTPS 入口和真实登录 | 通过 | 现有 Cloudflare 隧道正常转发；真实 Casdoor OIDC 登录进入 `rainlib-open-review`。保留 OIDC 鉴权。 |
| 服务升级 | 通过 | 数据库从 000113 升至 000116；控制 API、compact workers、Agent runner/adapter/broker 和 Console 已更新。 |
| 备份恢复和恢复后升级 | 通过 | 升级前备份实际恢复到独立数据库，再迁移到 000115；30 个工作区、45 条成员记录、6 个 Agent 任务和 91 个审查记录保持一致，恢复无错误、无无效约束。 |
| 真实 Issue 分析和自动计划 | 通过 | Issue #20 经真实模型分析、固定源 SHA、Jev 分类，生成包含三项验收条件的计划；判定为 high risk；工作区启用自审批后，原作者批准准确 revision 1。 |
| 独立逐项验证器 | 通过 | 固定、无网络镜像先识别基线缺陷；实际 Agent 修复后产生签名回执，三项冻结条件均有独立 passing evidence，与最终 patch/head 绑定。 |
| 工作区可配置自审批 | 通过 | 两个开关默认 false；只有 Owner 可以更新，revision 冲突拒绝。当前工作区显式开启，设置、Agent 计划批准、规则投票均保留 actor、self_approval 和设置 revision 审计。 |
| Agent 编码、验证失败、预算内修复、唯一 Draft | 通过 | attempt ac0f36f0 实际 coding 退出 0 → 固定 verifier 退出 1 → 自动 coding repair 退出 0 → verifier 退出 0，签名回执保留，发布 Draft PR #26。只修改批准的两个文件，6822 diff bytes。 |
| 规则预览和回放入队 | 通过 | 实际 Console 创建规则、提交审批、预览、创建回放和读取持久收据；修复了预览 UUID、空数组及 Outbox SQL 类型问题。 |
| 真实违规与修复规则回放 | 通过 | 相同规则快照、固化模型路由和单文件范围下，违规 SHA 得到 1 条 Critical，修复 SHA 得到 0 条发现。 |
| 回放与 GitHub 发布隔离 | 通过 | 两次回放前后逐项比较评论、检查和 commit status 的 ID/状态，均未改变。 |
| 自动再审和独立提交检查 | 通过 | PR #21 真实 Webhook 再审；现有 SSH 权限在验收分支安装只读 GitHub Actions，实际断言失败注解已被服务采集为 independent/code。修复后规则门禁及 Actions 均通过。工作流仅限验收分支，不代表 Agent 分支已自动接入 CI。 |
| 规则批准、发布、绑定及违规门禁阻断 | 通过，显式启用作者自审批 | v1 正式发布；绑定 d17f1a40 仅覆盖仓库 main 目标的样例文件。9727bb99 产生 1 条规则 Critical 并阻断；19d551e9 再审 0 发现，门禁 Success。Shadow/Canary/回滚尚未实机验收。 |
| Agent 的 CI 诊断修复、人工退回和逐项需求接受 | 部分通过 | Draft #26 准确 head 的独立 Go 检查有 100 个测试/子测试 pass 事件，自动再审 0 findings、门禁成功。Owner 会话实际提交 changes_requested，自动生成子任务 f983f70e，继承三个条件，准确新计划已获批准。子任务两次执行均在 coding_executor 失败；第二次安全诊断明确上游 HTTP 403。累计 5/5 次后新计划及审批入口被阻止，原 Draft 保留。反馈修订、最终接受及 Agent 的 CI 诊断修复尚未实机验收通过。 |

## 部署恢复与本轮代码修复

部署前 Docker 磁盘已满，PostgreSQL、RabbitMQ 和 Agent 无法正常启动。仅回收超过 168 小时的构建缓存，释放 10.52 GB，保留数据库卷和业务数据。Console 的本轮临时打包恢复了 pnpm 符号链接，解决启动重启导致的 502；现有 Cloudflare 路由无需新增。

本轮修复了五处实际问题：

1. 规则预览使用 `candidate:` 前缀拼接 UUID，异常解析器拒绝该身份。现在保留真实版本 UUID，并让预览和回放一致地替换该规则集的旧版本。
2. 空规则差异序列化为 `null`，Console 对其调用 `.length` 时崩溃。现在返回三个数组，空差异为 `[]`。
3. 规则回放 Outbox 将同一 SQL 参数同时当作 UUID 和 text，PostgreSQL 报 42P08。现在使用显式 UUID/text 转换，回归覆盖真实创建、快照和入队。
4. Agent 分类阶段的 `signals` 可为空，页面对其调用 `.length`/`.join` 时崩溃。现在展示层统一空数组，不改变 pending 状态或模型建议的权限边界，也不修改输入证据。
5. Console 将八类配置快照显示为 `8/7`，并误判为不完整。现在按八个不同的治理配置项计算覆盖，重复项不能补足缺失项。

验证：15 项聚焦 Go/数据库/规则测试通过；29 项相关前端回归测试通过；相关 ESLint、生产构建及构建中的 TypeScript 检查通过。数据库用例使用隔离 PostgreSQL，不修改业务数据。这些聚焦结果不代表所有功能或生产拓扑已完成验收。

基线 main 为 `b50da27acbf01289e3990fdd863d9743babc7ce6`。控制 API 和 Console 使用本轮工作区修复构建的候选镜像；本轮源码和验收报告尚未提交推送。

## 可核对的真实产物

- [Issue #20](https://github.com/RainLib/open-review-platform/issues/20)：真实 Agent 缺陷修复验收需求。
- [Agent 计划](https://review.rainlib.com/rainlib-open-review/agent-work?task=fc7de38a-b94a-4369-b447-a0a1a21ce8e3)：任务 revision 14 completed，计划 revision 3 由原作者批准并执行成功。计划 SHA-256 为 `f2bf92ce8dab8185bc532ec08619201af55c0f75f2665eafe229177235e38e21`；前两次失败审计保留。
- [真实 Agent Draft PR #26](https://github.com/RainLib/open-review-platform/pull/26)：head `4eb4a3c7019203086a75e9d7a95fa448533848b5`，签名 patch `80cc871b58f0e0b56c10b35922edc26416545a7c651de70ca59d357981a95215`，验证 profile `f5adf8cd78add7899aceb427d7c27a589c8f2359e80ec86dae86150b8b1f50f4`，output `7d566d9c76883e3f7a32239b9a4952c74c1c85ca1c60db2a54c622362c47c5fd`；自动再审 `5b9df1ec-8d44-4c42-858e-8955ea660793`。
- [验收反馈子任务](https://review.rainlib.com/rainlib-open-review/agent-work?task=f983f70e-83da-4cb4-ac06-83f60aac7fc1)：实际 changes_requested 反馈要求补强畸形结果测试，避免只命中数量检查；子任务冻结源为原 Draft head，继承原三项条件，自动生成计划 SHA `e78e91e6fe82b66de6327fb007cb3ce5b5d47948f8b4ee73ea25c35e9e48101f`，批准后 attempt `e68e3eb8-a093-4d4f-a88c-15cc524a5559` 约两秒退出，未进入验证。隔离诊断通过后提交第二版明确有界重试，SHA `2607474b7b7abfa5f39a80e5c137626ab700d6b8a74da551ad01ae964b85a36b`，准确审批；attempt `a2813493-5009-4a46-a24f-f41f121f5ce9` 返回 `agent_adapter_model_upstream_failed` / HTTP 403。子任务 revision 10 needs_attention，无交付回执；根任务保持 changes_requested。所有 Console 审批及验收操作使用用户已登录并授权本次验证的 Owner 会话。
- [Draft PR #21](https://github.com/RainLib/open-review-platform/pull/21)：人工创建的规则 fail/pass 验收样例，保持未合并。它不证明 Agent 已完成编码。
- 违规提交：`fc2a13d3b9bdda0b3a43a10a5115e9ae9dd84d4f`；源审查 `18b7cbf2-044c-482d-9ed7-f51eb2463f0c`；规则回放 `48cdaeae-5051-4715-b365-6c3adcde8fec`，1 条 Critical，定位 `internal/domain/acceptance_rules_fixture.go:5` 并引用准确规则版本。
- 修复提交：`1662840ab7255e4d51ea6a64fd03b9a6a5188506`；[源审查](https://review.rainlib.com/rainlib-open-review/reviews/969d9860-467c-4217-ba95-c0492939f1a0)完成，Open Review 门禁成功；规则回放 `b990f707-bef2-4fac-8542-788fa8fd046b`，0 条发现。
- 规则 v1：`bd100e51-5831-47d3-a302-6aa9bd6d21d5`；候选快照 SHA-256：`88528daa0fc7601f5fc49a965d9cbe014407756c78767bb538ecbead46dc908a`；两次回放相同。
- 仓库模型路由 revision 1：`deepseek-v4-flash`、现有 Tokenmix 端点和部署凭据引用。路由 SHA-256 为 `13dbd9c69d70e75d113f5314a8aff9857fa02bc357b848fa1e0a051916d850af`，源审查真实保留；连通探测成功，响应只保留 SHA-256。
- 独立检查上下文 `Acceptance / Go domain tests`：失败和成功均针对准确当前 head，来自固定无网络 Go 镜像；早期为手动独立容器检查；2026-10-08 已在验收分支安装真实 GitHub Actions，失败注解和成功结果都由服务采集。

非秘密收据和截图保存在仓库 `outputs/acceptance-20261007/`；汇总为 `acceptance-summary.json`。原始测试日志、部署 override、可信验证配置和备份保存在本机私有目录 `/Users/houshuai/.local/state/open-review/acceptance-20261007/`。备份 SHA-256 为 `48f861f45faebdd28373fe433f099343d87dac32c910b4ebbfccd53796a19f52`。独立测试容器和恢复演练数据库已清理，备份保留。

## 完成完整验收所需的下一步

1. 工作区设置已按用户授权开启；默认禁止、角色限制、设置版本冲突、撤销、重复投票、Console 和 Issue 评论命令的准确计划批准均已验证。没有伪造第二身份或降低冻结分类风险。
2. 初次编码故障已修复；真实 Agent 验证失败、预算内修复、唯一 Draft 及三个条件的签名回执均已取得。准确 head 的再审已通过，退回、子任务自动计划及新版本自审批均有证据；反馈编码被上游 HTTP 403 阻断。
3. 先恢复真实反馈编码请求的模型访问资格或使用现有已授权路由，再按合法的新请求与审批流程安排后续验收；当前冻结分支的五次执行已用完，不能修改策略扩展它。准确 Agent Draft 已通过手动独立 CI；仍需完成 CI 诊断修复、同一 Draft 新 SHA 再审及最终逐项需求接受。验收分支 Actions 使用现有 SSH 权限；Agent 分支的独立 CI 目前为手动触发。
4. 正式规则批准、发布、绑定和失败到通过已验证；下一步为 Shadow/Canary/回滚。main 要求 `Open Review / Analysis`，但独立检查未列为 required，`enforce_admins=false`；严格强制合并仍未证明。

5. 当前业务队列没有未确认消息；历史 `openreview.review.dlq.v1` 仍有 76 条，本轮未清空或重投。其故障分类和有证据的恢复、生产故障演练、GitLab 及离线部署等拓扑仍需分别验收。

当前可信验证 profile 仅用于 Issue #20 的三项固定条件，不能作为任意任务的通用验收器。继续验收时使用私有目录中的 Compose override；不要在这个专用配置下批准其他任务。原有 Issue #19 保持原状态。

## 2026-10-08 新增实机证据

- 审批 API：`GET/PUT /v1/tenants/{slug}/approval-policy`；Console `Settings → Approvals`；迁移 000116。只有 `rainlib-open-review` 显式开启，默认值及其他工作区保持关闭。
- 聚焦验证：Go API/Store/Domain 单元测试、29 项前端回归、完整 ESLint 和两次生产构建通过；新增设置/规则/Agent 命令完整数据库回归、既有 Agent 需求验收回归，以及此前失败的 `TestProviderChecksExactRunLeaseAndReadModel` 隔离重跑通过。
- 正式违规：head `9727bb992d457b9cc74ba8747690d79288f47693`，run `df86c4b9-cc88-4920-be5b-6fd70d0eb514`，规则 snapshot `da8a6d46-275c-48f4-9704-167fe88ac1de`，Critical 引用精确规则版本，门禁 Failure/1 blocking，独立 Actions Failure/code diagnostics。
- 正式修复：head `19d551e956389aaedad919fb060a1bf7e7d5de91`，run `3dcc38b1-a254-4e11-a4dd-1b4ec4a59132`，0 findings、门禁 Success，独立 Actions run `37708176338` Success；Console 展示了两个来源及准确 SHA。
- 编码依赖探测：当前 `aliyun/glm-5.3` 真实 Messages 请求 HTTP 200；同一固定沙箱镜像的独立写文件探测通过（9 秒）。这些探测不能代替真实 Agent attempt 的完成回执。
- 首次执行故障复现：8 个上游 Messages 请求均为 HTTP 200；第 9 次被本地模型 broker 以预算耗尽拒绝，子进程收到 429 并重试。旧配置每次预留 8192，8 次即达到 65536 的总额度。Claude 单次额度现为 4096，总额度、请求数、输入字节和沙箱隔离保持不变；16 次允许、第 17 次拒绝的回归及整个 adapter 测试通过。错误回调新增仅含可信计数的预算诊断，不传原始输出或凭据。
- 新 adapter 镜像 `open-review-platform-agent-task-adapter:acceptance-20261008-model-budget`，digest `sha256:cbd867898007bebd9e9c83379d7165299b078e45d2bc6cb553e1aa0f7f6c9dc4`。
- 后续诊断显示仅降低单次额度仍可能用完预留预算。现改为完成消息后根据有效累计 usage 结算实际输出；缺失、畸形、中断、错误、超限或倒退的 usage 不释放预留额度。请求数、输入字节及总输出上限保持原值。真实网关流式探测返回完整 message_start/delta/stop 和 13 个输出 token；缺少 usage 的 16 次上限、有效 usage 后 24 次请求上限等回归及整个 adapter 测试通过。
- 第二次正式执行编码退出 0，随后 Git 元数据 guard 拒绝结果；独立诊断也未通过基准提交匹配。新增固定类别错误码 `agent_adapter_git_state_rejected`，区分配置、分支、基准提交，且不传子进程原始输出。编码提示明确禁止 commit、分支切换、Git 配置和 `.git` 变更，所有元数据检查继续生效。
- 完成初次交付时的 adapter 镜像 `open-review-platform-agent-task-adapter:acceptance-20261008-usage-git-guard`，digest `sha256:c4dde35d911db8b142fa7454f1846b8bffeef93ca3bb69b89934e172e31c491c`。诊断及数据库测试使用的自有容器、网络和卷已清理，生产卷未改动。

- 最终反馈故障：两次实际任务编码均快速退出，第二次诊断记录固定模型上游 HTTP 403。新增安全错误码只包含可信 HTTP 状态或传输失败类别，不记录原始响应、提示词或凭据；预算诊断优先，恢复后的 HTTP 200 不误标为上游失败。新增两种协议的回归通过。
- 诊断边界：两版反馈计划在自有隔离网络与临时源快照内分别完成编码，10 次请求均为 HTTP 200；第二版耗时 60.19 秒、计费输出 2897 token。部署网络内相同端点/凭据的最小请求也返回 HTTP 200。端点、模型与凭据逐项相等，但当时尚未定位实际任务的 403 原因；后续真实 shallow clone 加生产 Transport 复现得到 `AccessDenied.Unpurchased`，但账号资格或网关具体路由原因仍未确认；不能由简化诊断成功推断真实反馈交付成功。诊断没有 Provider 写入，输出未用于正式交付。
- 当前 adapter 为 `open-review-platform-agent-task-adapter:acceptance-20261008-upstream-diagnostic`，digest `sha256:d940d4aa675f3694f80e21758f3800baa2a8e1e24e6abe07e84c0f878b1c38f7`。完整 adapter 测试及新增可信状态诊断回归通过；规则/Agent 设置的前述数据库和前端结果保持有效。
- 最终读回：PR #26 为 OPEN/Draft，head 仍为 `4eb4a3c7019203086a75e9d7a95fa448533848b5`；远端 main 仍为 `b50da27acbf01289e3990fdd863d9743babc7ce6`。三项初次交付证据、再审与独立检查保留，反馈修订和最终需求接受没有伪造成功。

## 2026-10-08 执行失败诊断与优化

五次累计执行为主任务三次加反馈子任务两次，共 **一次成功交付、四次 needs_attention**，并非五次全部失败。第一次为模型预留预算耗尽，第二次为 Git 元数据校验拒绝；第三次实际完成验证失败、自动修复、再验证、Draft 和准确 head 再审。第四次的旧回调只保留 coding_executor 失败，不能补写其历史原因；第五次明确为上游 403。

在自有诊断网络内，使用反馈任务同一准确 SHA 的真实 shallow clone、同一固定 Claude CLI/模型/凭据和生产 Transport，再次复现 403，安全错误枚举为 `AccessDenied.Unpurchased`。该枚举表示模型访问资格被拒绝；当前账号资格、路由或下游账户选择仍需排查，不能由错误名称推断用户需要购买应用功能。原始响应、请求与凭据只在私有诊断目录，诊断没有 Provider 写入。

本轮代码与部署优化：

- 新执行先调用签名 `/v1/open-review/readiness`，检查实际沙箱和固定模型完整响应，再领取数据库 attempt。检查失败仅将准确排队 revision 置为 needs_attention，原子记录审计；不创建 attempt、不消耗冻结额度，页面展示执行前阻断。恢复后需要新建并批准计划。重复消息、错误 hash、新批准的计划和正在执行的任务均受数据库 fencing 保护。
- 定期签名 `/health` 保持轻量，不调用模型。执行前成功检查缓存一分钟；401/403 预检或实际执行失败会阻断该 adapter 进程的后续执行检查，防止持续消耗额度。此阻断是进程内状态，修正不可变模型配置并重启后重新检查；不将历史失败伪造为成功。
- 429、500/502/503/504/529 明确 HTTP 失败最多追加两次相同请求，尊重最多五秒 Retry-After，取消即时终止；每次仍计入原有 24 请求、1 MiB 输入和 65536 输出预算。授权、参数、路由拒绝不重试；不重放结果不确定的传输失败或已开始的成功响应流。后续实测发现 CLI 自身重试仍会叠加消耗原有总预算；该问题在下述追加优化中修复。
- 只保留白名单上游错误枚举及可信 HTTP 状态，不回传 Provider 消息、提示词、密钥或子进程输出。
- Console 聚合原任务与所有反馈的共享预算，真实显示 `5/5`、一次成功交付、四次待处理。旧五次回执、Draft #26 和冻结限额保持原样。

验证：四个相关 Go 包单元回归通过；adapter/runner 竞态检查通过；隔离 PostgreSQL 实测预检失败额度为零、审计唯一、重复/旧消息无效、新审批恢复、既有 Agent 需求交付与工作区自审批回归通过；29 项相关前端回归、ESLint、生产构建通过。真实部署签名探测 health 204（8 ms）、readiness 204（1614 ms）、随后 health 204（0 ms），没有 task claim。模型可用性探测不是完整反馈成功证据，真实完整反馈闭环仍未通过。

Console 首次候选包因展开 pnpm 链接而缺少运行依赖，已恢复可用镜像并采用匹配的既有运行依赖重新打包；最终启动、登录页和真实任务详情均完成检查。独立诊断及数据库测试的自有容器、网络和卷清理，生产卷不动。本轮代码仍未提交推送。

最终候选镜像身份和各层验证汇总见 `outputs/acceptance-20261007/execution-reliability-summary.json`；真实 Owner 页面截图为 `execution-budget-final.png`。远端 main 仍为 `b50da27acbf01289e3990fdd863d9743babc7ce6`，PR #26 仍为 OPEN/Draft、head `4eb4a3c7019203086a75e9d7a95fa448533848b5`。


## 2026-10-08 追加：终止叠加重试与真实仓库反馈验证

同一现有网关及凭据的模型目录可读取，但目录不证明所有路由具有执行权限。隔离真实 clone 的 `zhipu/glm-5.3` 诊断遇到持续 429：旧 broker 每个请求追加两次后仍允许 SDK 发起新请求，最终上游调用 16 次，编码约 154 秒，误报预算耗尽。该诊断没有正式 attempt 或 Provider 发布。

追加修复将失败限制在同一 job：明确临时 HTTP 失败最多访问上游三次，仍失败则保持首次终止状态并通知编码进程取消；401/403、非重试状态、不确定的传输失败和成功流读取中断也终止本次编码。认证后的模型请求串行化，SDK 顺序或并发重试均不能越过终止状态，也不会继续预留预算。本地请求、输入或输出预算触顶也结束本次编码，保留预算诊断。编码上下文取消不取消可信父上下文，沙箱清理仍使用独立上下文；CLI 即使退出 0，也不能把终止模型失败当作成功交付。原请求、输入、输出和任务执行限额未增加。

请求对比中，完整 `aliyun/glm-5.3` 请求先返回 403，移除扩展字段及 `aliyun/glm-5.2` 对比请求返回 200。随后**完全相同请求 SHA-256** `4a343af4f237fad0dbfe59ecf1fd7cc2912ab672daac3a5e0cd57e5a5dd9cbf5` 的原始完整请求也返回 200，所以不能认定某个字段是故障原因。当前网关路由/下游资格的间歇性拒绝原因未确认，未更换正式模型、凭据或账户权限。

使用准确反馈源 `4eb4a3c7019203086a75e9d7a95fa448533848b5` 的真实 shallow clone、原批准反馈计划和固定沙箱，再次验证 `aliyun/glm-5.3`：编码及 Git/变更范围检查通过，共 11 次上游请求全部 HTTP 200；同一模型 job 预算下，固定网络隔离验证器的三项验收条件全部通过，证据输出 SHA-256 `55e329883094fc6ae229f7190bca2c57be177c8932415a163355c38c99cfaa28`。诊断首次调用验证器时遗漏条件 stdin，已修正诊断并完整重跑；没有把那次失败计为成功。成功证据见 `outputs/acceptance-20261007/agent-feedback-isolated-verified.json`。

该结果证明隔离编码与独立验证可执行，不是正式发布回执。旧根任务与反馈子任务仍共享 5/5 冻结额度，Draft #26 head 与 main 未变化；没有清空额度、修改历史、推送诊断代码到 Draft 或代替用户最终接受。新的正式验收轮次需要明确的新验收需求及其审批。安全诊断和路径汇总见 `outputs/acceptance-20261007/model-route-and-retry-continuation-summary.json`。

追加优化最终运行镜像 `open-review-platform-agent-task-adapter:acceptance-20261008-reliability-v4`，身份 `sha256:a0ab3fc2b39d076d048eac482b906a1c29dd59ccba37ac446d7757a0a76b3846`。完整 adapter 竞态回归以及 runner 竞态、API/store 单元回归通过；最终签名 health/readiness/health 均为 204，仅检查可用性，没有创建新 attempt。自有诊断容器、子容器、网络、卷及临时源码已清理，私有原始证据保留；本轮代码仍未提交推送。

## 2026-10-08 新的正式需求与反馈交付

用户明确授权“继续 创建新的”后，创建真实 [Issue #27](https://github.com/RainLib/open-review-platform/issues/27)，独立冻结 main / `b50da27acbf01289e3990fdd863d9743babc7ce6`，保留旧 Issue #20 / Draft #26 的全部历史和 5/5 冻结额度。新轮沿用相同的三项固定条件和部署验证器，不扩大验证器适用范围；自动计划由确定性 planner 产生，Jev 分类建议及 Owner 准确计划审批均真实发生。

| 新轮检查点 | 当前证据 |
| --- | --- |
| Issue 分析与自动计划 | 真实 Webhook，Issue 分析 revision 2 上下文充分；root 和反馈的自动计划分别经 Owner 准确审批。 |
| 自动编码、验证与修复 | 初次编码 → 固定独立验证退出 1 → 自动修复 → 固定独立验证退出 0；最终三项条件签名回执通过。中间失败 stdout 未长期保留，顺序由 Docker 子容器事件证明。 |
| 初次交付与再审 | 自有 Draft #28，初次 head `a3edf61f`；准确 head 审查 0 findings、门禁 success，独立 Go 检查 104 个 pass 事件。 |
| 真实需求退回和反馈修订 | Owner 实际 changes_requested；子任务 `752e7811` 经新计划审批，增加有效边界、20 项重排与 UTF-8 测试，更新同一 Draft 到 `59d409b7`，仅增加测试，生产函数未继续变更。 |
| 发布确认恢复 | 编码/验证/推送完成后即时 Draft 确认失败；新增有界只读恢复经一次 Provider GET 确认现有准确 head，原失败和恢复审计同时保留，无新编码、无重复 Provider 写入。 |
| 反馈 head 再审与独立 CI | 原审查三次 5 分钟超时后保留失败；Console 正常重审 `578d526d` 完成，0 findings、门禁 success。固定独立 Go 容器 117 个测试及子测试 pass 事件；服务采集两种检查来源的准确 head success。 |
| 最终需求接受 | Console 为 awaiting_acceptance，逐项证据已准备，等待用户最终决定；Draft 保持 OPEN，未合并。 |

新轮共有两次正式执行，额度 **2/5**。首次任务 `1a1a7c80` 和反馈任务 `752e7811` 均成功交付；故障恢复没有重置预算或伪造原失败回调。首次交付机器收据 `new-formal-initial-delivery.json`，反馈与最终待验收收据 `new-formal-acceptance-summary.json`，详情见 `31-agent-feedback-revalidation-request.md`。Console 的用户填写内容目前仅在页面准备，最终决定尚未提交。

新轮暴露并修复了两个实际缺口：历史冻结模型路由缺少 `max_concurrent_runs` 时，两处读取未使用兼容解码；可信 checkpoint 后已有 Draft 的即时确认失败缺少不重复编码的持久化恢复。前者保持原始快照与哈希；后者用迁移 117、有效批准/installation/新计划与子任务 fencing、最多三次只读确认。隔离 PostgreSQL 的路由兼容与 supersession 回归、10 个发布恢复子用例及既有完整 Agent 工作流验收回归通过；adapter/workflow 竞态通过。迁移 117 于 11:59:36 UTC 实机应用，原业务数据备份保存在本机私有目录。

当前 adapter 镜像 `open-review-platform-agent-task-adapter:acceptance-20261008-reliability-v5`，身份 `sha256:de4d0269b88bb7a2f40ad0f9ae8880a9adfb913345f98116a151af8e31b60fd3`；compact 镜像 `open-review-platform-compact-review:acceptance-20261008-publication-recovery`，身份 `sha256:e9b825289c70d431798b9e76f856e492670e0baa1f128f7cdcca0d2d5aa65269`。新恢复数据库测试容器已清理，业务卷不动。运行修复和报告仍未提交推送；远端 main 保持原 `b50da27a`，Agent 已独立推送 Draft 分支。

本轮补齐了真实反馈修订、同一 Draft 新 SHA 再审与无重复编码的发布确认恢复；最终人类接受仍待决定。Agent 根据真实独立 CI 诊断自动修复、Agent 分支自动 CI、规则 Shadow/Canary/回滚、历史 DLQ 恢复、严格主分支强制合并、GitLab 及离线拓扑仍未由本轮实机证明。即时发布确认故障及偶发模型超时的底层原因仍未确认，正常有界恢复成功不能替代根因证据；不据此宣称整个服务所有功能均已完成验收。
