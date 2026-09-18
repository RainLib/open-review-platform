# 审查评论与证据回执设计

## 1. 目标

Open Review 的评论不是模型聊天记录，而是一次变更的可审计回执。设计吸收 Kodus 的可见开始回执、标签化行内建议、明确完成状态与可复制给 Agent 的修复上下文，同时保持独立的数据模型、命名和实现。

同一个 Review Job 在 PR/MR 中只有一条生命周期评论：开始时创建，完成、失败、取消或被新提交替代时原位更新。行内 Finding 使用稳定指纹去重，GitHub Check 只承担紧凑的合并门禁结论。

## 2. 信息来源与可信等级

| 信息 | 来源 | 可信等级 | 是否可控制门禁 |
| --- | --- | --- | --- |
| Base/Head SHA、文件统计 | Git provider API | 已观测 | 是，作为审查对象 |
| Finding | 固定版本 OCR/模型输出 | 推断 | 经过确定性严重度策略后可控制 |
| 企业规则与阈值 | 控制面不可变规则快照 | 可信配置 | 是 |
| Outcome、验收、灰度、回滚 | PR/MR Change Contract | 作者声明、未验证 | 否 |
| 测试/安全/性能结果 | 未来 Verifier 结构化结果 | 已执行证据 | 可按企业策略控制 |

PR/MR 描述永远是不可信输入。它可以改善报告上下文，但不能降低严重度、改变规则、扩大 Agent 权限或伪造已执行证据。

## 3. Change Contract

平台识别以下 Markdown 标题，中英文标题均可：

```markdown
## Outcome
## Scope
## Risk
## Acceptance mapping
## Invariants
## Verification
## Rollout
## Rollback
## Provenance
```

上下文分为四级：

- `empty`：描述为空，提示补充任务信息。
- `minimal`：有描述，但没有可识别的合同章节。
- `partial`：识别到部分章节，明确列出缺失项。
- `complete`：八个必需章节齐全；内容仍标记为“作者声明、未验证”。

系统不得为了让报告显得完整而生成缺失的验收、灰度或回滚信息。

## 4. 场景与回复

| 场景 | 生命周期评论 | 行内评论 | Check 结论 |
| --- | --- | --- | --- |
| 已接收 | PR 标题、Base/Head、文件统计、上下文质量、下一步 | 无 | `in_progress` |
| 通过且无 Finding | 完整证据报告，明确 `Review passed` | 无 | `success` |
| 有非阻塞 Finding | `passed with findings`，列出风险与建议 | 类别、严重度、建议、Agent 上下文 | `success` |
| 达到门禁阈值 | `Merge blocked`，说明阈值和阻塞数量 | 同上 | `failure` |
| 最终执行失败 | 通用安全错误、重试命令、Commit/Job 追溯 | 无，不暴露半成品 | `failure` |
| 用户取消 | 明确未发布 Finding | 无 | `neutral` |
| 新提交替代 | 明确旧输出已丢弃且不影响门禁 | 无 | `neutral` |

底层 provider 或模型异常不得原样写入公开评论，详细安全错误只保存在任务与审计数据中。

## 5. 组件与抽象

```text
Provider Context Loader
  ├─ GitHub PR + files
  └─ GitLab MR + diffs
          │
          v
Change Contract Parser ──> ReviewContext
                                 │
ReviewResult + MergeGate ────────┤
                                 v
Report Builders
  ├─ StartedReport
  ├─ CompletedReport
  ├─ TerminalReport
  └─ FindingReport
                                 │
                                 v
Markdown Components
  Heading / Paragraph / BadgeRow / BulletList / Table / Details / Divider
                                 │
                                 v
Provider Publisher
  ├─ GitHub issue comment / review comment / Check Run
  └─ GitLab note / discussion
```

Report Builder 不调用 provider API；Provider Loader 不拼接 Markdown；门禁计算只读取结构化 Finding 与可信配置。这样后续可以增加 HTML 管理后台、JSON 审计导出或 Slack 通知，而不复制业务话术。

## 6. 最终证据报告

最终生命周期评论固定包含：

1. `Outcome`：执行结果、Finding 数量、门禁结论。
2. `Scope`：精确 Commit、文件数量与折叠文件表。
3. `Risk`：最高严重度、数据分类、信任边界、静态爆炸半径。
4. `Acceptance mapping`：仅展示作者声明，并明确是否实际执行。
5. `Invariants`：系统已验证不变量与作者声明分开呈现。
6. `Verification`：AI 审查、门禁、测试、安全、性能、UI、迁移分别报告。
7. `Rollout`：声明内容与实际执行状态分开。
8. `Rollback`：Owner、开关、版本和数据恢复；缺失时明确告警。
9. `Provenance`：Base/Head、Job、Provider、OCR 版本与规则快照。

删除 GitHub Actions 后，Build、测试和安全扫描默认必须显示为“未提供”，直到隔离 Runner 接入确定性 Verifier，不能将 AI 审查成功等价为测试通过。

## 7. 约束

- 生命周期 Marker 使用 Review Job ID，重试更新原评论而不是制造评论风暴。
- Finding Marker 使用 Job、路径、行号、类别和正文的稳定摘要。
- 文件表最多展开 20 行，仍保留总文件/增删统计。
- 文件名是可点击链接：GitHub 直达当前 Head 的文件，GitLab 直达 MR Diffs；仅允许 provider 返回的 HTTP(S) 地址。
- 作者声明在重新渲染前进行 HTML 转义和长度限制。
- Provider 元数据读取失败不终止代码审查，报告退化为精确 SHA 与“范围元数据不可用”。
- 新 Head 到达后旧 Run 必须进入 `superseded`，旧 Run 禁止发布 Finding 或通过门禁。
