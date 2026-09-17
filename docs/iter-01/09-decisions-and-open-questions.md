# 09 — 架构决策与开放问题

## 1. 已确认决策

### ADR-001：企业扩展独立仓库

- **决策**：Open Review Platform 独立于 Kodus 和 OpenCodeReview 仓库。
- **理由**：隔离许可证、产品边界、发布节奏和技术栈；OCR 是执行引擎，平台是控制面。
- **影响**：通过 CLI/结构化输出适配 OCR，不复制第三方企业代码。

### ADR-002：Go/Kratos 控制面 + Casdoor

- **决策**：后端使用 Go/Kratos，身份使用 Casdoor OIDC。
- **理由**：适合独立服务、明确的认证边界和企业身份扩展。
- **影响**：平台仍需自行实现租户成员、RBAC/ABAC、审计和会话安全；Casdoor 不替代业务授权。

### ADR-003：PostgreSQL 权威状态 + RabbitMQ 传输

- **决策**：PostgreSQL 是任务/规则/计量真相，RabbitMQ 负责通知和消峰。
- **理由**：可恢复、可审计、可查询，并保留队列隔离与扩缩容。
- **影响**：必须实现 outbox/inbox、幂等和补偿，不宣称 exactly-once。

### ADR-004：先回执再执行

- **决策**：评论/mention 触发后，以高优先级独立 worker 发布回执，长任务异步执行。
- **理由**：让用户知道请求已被接收，并隔离执行高峰。
- **影响**：区分 HTTP transport ACK 与 provider user ACK。

### ADR-005：规则版本不可变

- **决策**：published 规则不可编辑；run 固定完整 snapshot。
- **理由**：可审计、可重放、可解释、可回滚。
- **影响**：规则修改、审批和发布必须以新版本进行。

### ADR-006：不展示私有推理

- **决策**：UI 只展示阶段、工具/规则事件、证据和结论，不展示 chain-of-thought。
- **理由**：安全、稳定、可验证且避免误导。
- **影响**：任务页命名为“Reasoning canvas”时必须明确其为高层执行流；实现可考虑改为 “Review canvas” 以减少歧义。

### ADR-007：前端先设计评审

- **决策**：前端代码只能在高保真方向、关键原型、状态矩阵和 API 契约批准后开始。
- **理由**：避免先做通用后台再反复重构为产品化体验。
- **影响**：本 PR 只提交设计文档和图片。

## 2. P0 开放问题（实现前必须确认）

### Q-001：SaaS 首发身份模型

- GitHub/GitLab actor 未与 Casdoor 用户映射时，是否允许 `review` 命令？
- 建议：允许仓库成员触发低风险 review；cancel/retry/rule override 必须完成账户绑定。
- 决策人：产品 + 安全。

### Q-002：AI finding 是否默认影响 merge gate

- 建议：阶段一仅 advisory；阶段二只有经过影响验证的明确规则可配置 required，确定性政策才能默认 blocking。
- 决策人：产品 + 安全/研发效能。

### Q-003：源码/diff 是否持久化

- 建议：默认不保存完整源码；短期缓存 manifest/diff 必须加密、按租户保留并允许完全关闭。
- 影响：Test Lab 历史回放可能需要按需重新拉取。
- 决策人：安全 + 产品。

### Q-004：GitLab SaaS 与 Self-Managed 凭据标准方案

- 需要确认 OAuth application、project/group access token、bot user 的支持矩阵和最小权限。
- 决策人：集成负责人 + 安全。

### Q-005：RabbitMQ delayed-message 插件依赖

- 选项 A：要求插件，拓扑简单。
- 选项 B：TTL retry queues + DLX，兼容性更高但队列更多。
- 建议：支持能力检测；默认 TTL/DLX，插件作为优化。
- 决策人：SRE/架构。

### Q-006：私有部署 entitlement

- 是否完全关闭套餐门禁，还是使用本地签名 license/静态 entitlement 文件？
- 建议：社区私有部署启用所有开源能力；商业支持/托管能力通过独立合法模块，不依赖或绕过第三方 token。
- 决策人：商业/法律/产品。

## 3. P1 开放问题（阶段一开发中确认）

- 单个 PR/MR 的最大 diff、文件、inline finding 默认上限。
- `standard/deep/security` 的 agent、模型、预算和超时默认值。
- 自动触发对 draft、bot author、fork PR 的默认策略。
- 状态评论更新最短间隔和 provider rate-limit 预算。
- 任务 ID 对外格式（ULID/短 ID）和搜索体验。
- 首发支持的 LLM provider、BYOK 和 region 路由。
- SSE 是否足够，是否需要在超大部署引入独立 pub/sub。

## 4. P2 开放问题（阶段二开发中确认）

- 规则 DSL 是否保持 JSON，或提供更适合 authoring 的 YAML/表单中间模型。
- 历史回放样本的授权、隐私和成本限制。
- rule feedback 的最小样本量和统计置信度展示。
- 组织/team/repo 三层是否足够，是否需要 business unit/folder。
- SCIM、SAML 与 Casdoor 能力边界及企业支持矩阵。
- 专用 tenant worker/database 的套餐和迁移路径。

## 5. 设计评审记录模板

```markdown
## Review YYYY-MM-DD

- Decision: APPROVED | APPROVED_WITH_CHANGES | CHANGES_REQUESTED
- Product owner:
- Design owner:
- Architecture owner:
- Security owner:
- SRE owner:
- Approved commit:

### Required changes
- [ ] ...

### Accepted trade-offs
- ...
```

审批必须引用 commit SHA，避免后续文档变化仍沿用旧批准。
