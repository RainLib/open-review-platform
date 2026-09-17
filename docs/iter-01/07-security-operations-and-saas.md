# 07 — 安全、运营与 SaaS 管理设计

## 1. 信任边界

```text
Internet
  ├─ Browser user (OIDC)
  ├─ Git provider webhook (signature)
  └─ Git provider API (installation identity)

Platform trusted zone
  ├─ Control/webhook services
  ├─ PostgreSQL/RabbitMQ/Object storage
  ├─ Credential broker/KMS
  └─ Observability

Untrusted execution zone
  └─ Ephemeral repository workspace + OCR/agent process
```

待审代码、PR 规则文件、仓库脚本、构建配置和模型输出全部视为不可信输入。

## 2. 身份与会话

- Casdoor 提供 OIDC；Web 使用 Authorization Code + PKCE。
- API 验证 issuer、audience、signature、expiry、nonce/session；不只 decode JWT。
- 用户与 `(issuer, subject)` 绑定，不以 email 作为稳定身份主键。
- Web session 使用 Secure、HttpOnly、SameSite cookie；mutation 防 CSRF。
- 企业 SSO/MFA/会话时长由 Casdoor/组织策略控制；平台保存必要 policy mirror。
- service account 使用 scoped key 或 OAuth client credential，key 只存强哈希。

## 3. 授权

- RBAC 提供角色基线，ABAC 限定 tenant/team/repository/rule scope。
- API 每次从 token subject 解析 membership，不信任前端提交 role/tenant。
- provider 评论命令以 provider actor 映射 membership；未映射用户只能执行租户策略允许的有限动作。
- 高风险动作支持 approval 或 step-up authentication。
- worker 使用 workload identity，只具备其 queue 和资源所需最小权限。

## 4. Provider 凭据

- GitHub 使用 GitHub App 私钥换取短期 installation token。
- GitLab 优先 OAuth/application/bot 身份，按部署能力选择可轮换的最小权限方案。
- 业务库仅保存 `credential_ref`；broker 从 KMS/Vault 获取并按 tenant/purpose 校验。
- token 缓存有明确 TTL，日志/trace/message/错误不得包含 token。
- 权限清单版本化；新增 scope 必须重新授权，不静默扩大。
- 提供轮换、撤销、last used 和异常使用审计。

## 5. 不可信代码执行

- runner 无特权、只读 rootfs、无 Docker socket、无宿主目录、最小 Linux capability。
- 每个 run 独立 workspace 和进程/容器，CPU/内存/pid/磁盘/时间限制。
- 默认不执行仓库脚本，不安装依赖，不运行 tests；需要工具执行时建立 allowlisted sandbox profile。
- egress 默认只允许 provider、配置的 LLM 和必需基础设施；阻止访问 cloud metadata/private network。
- checkout 禁用 submodule/LFS 自动执行，除非显式安全配置；防 path traversal 和 symlink escape。
- 日志和模型输入前做 secret scanning/redaction；输出发布前再次清理。

## 6. 规则与 prompt 安全

- 控制面发布规则为可信来源；PR head 内规则默认不参与执行。
- repo 规则仅允许来自 base/default branch 并经过 schema/字段/大小验证。
- 模型提示明确将源码视为数据，禁止遵循源码注释中的系统指令。
- tool allowlist 和结构化参数校验在模型之外执行。
- 任何“忽略规则、读取密钥、访问网络”的模型输出都不能自动改变权限。
- private reasoning 不保存；保存高层 stage、tool receipts、finding evidence 和成本。

## 7. 数据分类与保留

| 数据 | 默认分类 | 默认保留建议 |
| --- | --- | --- |
| 身份/成员 | 机密 | 账户期 + 法规要求 |
| provider credential | 高敏感 | secret manager，轮换/撤销 |
| raw webhook | 机密 | 7–30 天，可关闭正文保留 |
| 源码 workspace | 高敏感 | run 结束后立即清理 |
| diff/input artifact | 高敏感 | 默认不持久化或短期加密 |
| findings | 机密 | 90 天或租户策略 |
| audit events | 合规记录 | 1 年或租户策略 |
| usage/cost | 商业机密 | 账期 + 财务要求 |
| operational logs | 内部/机密 | 14–30 天，脱敏 |

支持按 tenant 配置 region、保留、导出和删除；合法保留（legal hold）优先但必须审计。

## 8. 多租户安全

- tenant context 从认证/installation 映射生成，不接受任意 header 作为生产租户身份。
- 每个 repository、run、rule、usage、audit 查询必须带 tenant predicate。
- 负面测试覆盖跨租户 UUID、cursor、export、SSE、对象引用和 provider ID 冲突。
- 缓存 key、对象 key、MQ routing、metric label 避免租户碰撞。
- enterprise dedicated 模式可提供独立数据库/队列/worker，但使用同一逻辑契约。

## 9. 审计

必审计：

- 登录/敏感认证事件和 API key 生命周期；
- 成员/角色/SSO/安全设置变更；
- provider 安装、权限、轮换、解绑；
- 规则创建、diff、审批、发布、回滚、exception；
- 任务创建、强制取消、重试、DLQ 操作；
- 数据导出、保留策略与删除；
- 额度/套餐/计费相关管理动作。

审计日志 append-only，使用 hash chaining/WORM 导出作为增强选项；任何 actor 都不能修改历史事件。

## 10. 限流、配额与防滥用

### 10.1 入口

- webhook 按 provider instance/installation/IP 分层限流，但避免误伤 provider 固定出口。
- control API 按 user/service account/tenant/route 限流。
- 评论命令按 actor + repository 限频，重复命令幂等。

### 10.2 资源

- tenant active runs、queued runs、deep runs、单次 diff、月 token/cost。
- provider API 请求预算、LLM provider/model 并发和每日上限。
- 单租户异常增长触发 admission 降级，不拖垮全局。

### 10.3 套餐能力

能力门禁使用 server-side entitlements：

```text
feature.enterprise_rules
feature.rule_approvals
feature.sso
feature.audit_export
feature.custom_retention
limit.concurrent_runs
limit.monthly_review_tokens
```

私有部署可加载本地 entitlement 配置；不得通过绕过 Kodus token 获得商业代码。此平台实现的是独立能力，不调用或伪造第三方企业许可证。

## 11. 计量与成本

- 账本 append-only，修正使用 reversal/adjustment，不原地改历史。
- 记录 input/output/cache token、model/provider、OCR/worker 秒、存储、provider API。
- admission 进行预算 reservation；终态 settle，崩溃由 reconciliation job 对账。
- UI 估算标记为 estimated，结算后显示 actual。
- BYOK 与平台 key 分开计量；BYOK 不等于免费，仍可能计 worker/存储。

## 12. SLO 与告警

建议首版目标：

| 指标 | SLO |
| --- | --- |
| Webhook 接收可用性 | 99.95% 月度 |
| Webhook 持久化延迟 | P95 < 500ms（不含 provider 网络） |
| User ACK 延迟 | P95 < 5s，P99 < 15s |
| Control API 查询 | P95 < 400ms |
| SSE 状态可见延迟 | P95 < 2s |
| 已接收任务不丢失 | 99.999% |
| Publisher 重复外部效果 | < 0.01%，目标 0 |

不对整体 review 完成时间设单一 SLO，应按 mode、diff size、provider/model 分桶。

告警基于用户影响：oldest queue age、ack SLA breach、outbox stuck、terminal failure rate、publication ambiguous、lease churn、credential failures、quota reconciliation drift。

## 13. 备份与灾备

- PostgreSQL PITR + 每日全备；定期恢复演练，不只检查备份成功。
- RabbitMQ 消息可由 outbox 重建；队列不是唯一备份。
- 对象存储版本/生命周期按数据分类配置。
- secret manager 有独立备份/恢复和 break-glass 流程。
- 建议目标：控制面 RPO ≤ 5 分钟，RTO ≤ 60 分钟；实际目标由部署级别确认。
- 灾备切换后 publisher 依靠 receipt/marker 防止重复评论。

## 14. 运维控制台

仅内部/SRE 可见：

- queue depth/age、consumer、outbox、DLQ；
- run 事件和安全日志摘要；
- safe retry、requeue、cancel、mark resolved；
- provider credential health（不显示 secret）；
- tenant concurrency/额度和 noisy neighbor；
- feature flag/canary 状态。

所有运维操作要求理由、审计和明确作用范围；不提供“重跑所有失败任务”的无界按钮。

## 15. 合规准备

- 数据流图、subprocessor/LLM provider 清单和数据地域矩阵。
- DPA/删除/导出流程，安全事件响应与通知流程。
- 依赖/SBOM/容器扫描、签名镜像和 provenance。
- 定期权限审查、渗透测试和 tenant isolation 自动化测试。
- 私有部署文档明确客户与平台的共享责任。
