# 05 — 数据、API 与事件契约

## 1. 数据设计原则

- UUID/ULID 内部主键；provider 外部 ID 保留为字符串，避免溢出和不同实例冲突。
- 业务表必须有 `tenant_id`、`created_at`、必要时 `updated_at`/`revision`。
- 状态采用受控枚举/约束，不接受任意字符串。
- access token、private key、LLM key 不写业务数据库，只保存 `credential_ref`。
- JSONB 用于版本化 payload/快照，不替代可索引的核心关系字段。
- append-only 事件/审计与当前态分开；当前态用于查询，事件用于恢复与解释。

## 2. 领域表

### 2.1 身份与组织

```text
tenants(id, slug, name, status, region, plan_id, settings, created_at)
users(id, oidc_issuer, oidc_subject, display_name, email_hash, status)
memberships(tenant_id, user_id, role, status, created_at, updated_at)
teams(id, tenant_id, name, slug)
team_members(team_id, user_id, role)
service_accounts(id, tenant_id, name, status, expires_at)
api_keys(id, tenant_id, service_account_id, key_hash, scopes, expires_at)
```

### 2.2 Provider 与仓库

```text
provider_instances(id, provider, base_url, api_url, region)
provider_installations(id, tenant_id, provider_instance_id, external_id,
  credential_ref, repository_scope, status, permissions, last_synced_at)
repositories(id, tenant_id, installation_id, external_id, full_name,
  default_branch, visibility, settings, status)
webhook_deliveries(id, tenant_id?, provider_instance_id, installation_id?,
  delivery_id, event_name, payload_ref, payload_sha, status, received_at)
normalized_events(id, tenant_id, delivery_id, event_type, schema_version,
  subject_type, subject_external_id, payload, created_at)
```

唯一约束：`(provider_instance_id, delivery_id)`；repository 使用 `(installation_id, external_id)`。

### 2.3 任务与执行

```text
review_requests(id, tenant_id, repository_id, provider_review_number,
  trigger_kind, trigger_actor_id, desired_mode, state, latest_run_id, created_at)
review_runs(id, tenant_id, request_id, parent_run_id, base_sha, head_sha,
  state, revision, attempt, rule_snapshot_id, execution_plan_id,
  cancel_requested_at, superseded_by, started_at, finished_at, error_code)
run_stages(id, tenant_id, run_id, stage_type, state, revision, attempt,
  lease_owner, lease_expires_at, heartbeat_at, input_ref, output_ref,
  started_at, finished_at, error_code)
execution_plans(id, tenant_id, run_id, schema_version, engine_name,
  engine_version, engine_digest, model_route, data_region, payload, sha256)
task_events(id BIGSERIAL, tenant_id, run_id, run_revision, event_type,
  schema_version, public_payload, created_at)
review_findings(id, tenant_id, run_id, rule_key, agent_key, fingerprint,
  severity, category, path, start_line, end_line, evidence, body,
  suggestion, confidence, publication_state)
publication_receipts(id, tenant_id, run_id, provider, kind, idempotency_key,
  external_id, external_url, payload_sha, state, updated_at)
```

### 2.4 交互、规则、计量和可靠性

```text
interactions(id, tenant_id, repository_id, provider_comment_id,
  actor_external_id, command, command_args, state, status_publication_id)
rule_sets(...)
rule_versions(...)
rule_bindings(...)
rule_snapshots(...)
usage_ledger(id, tenant_id, run_id, meter, quantity, unit, cost_amount,
  currency, provider, model, idempotency_key, occurred_at)
quota_reservations(id, tenant_id, run_id, meter, reserved, settled, state)
audit_events(id BIGSERIAL, tenant_id, actor_type, actor_id, action,
  resource_type, resource_id, request_id, before_hash, after_hash,
  metadata, outcome, created_at)
outbox_events(...)
inbox_claims(...)
```

## 3. API 约定

Base path：`/v1`。JSON 使用 `snake_case`。时间为 UTC RFC 3339。金额为字符串 decimal + currency。列表采用稳定 cursor，不使用易漂移的 page number。

### 3.1 通用请求头

```http
Authorization: Bearer <oidc-or-service-token>
Idempotency-Key: <uuid>          # mutation 推荐/部分端点必需
X-Request-ID: <uuid>             # 可选，服务端缺失时生成
If-Match: "<revision>"           # 更新规则/配置时必需
```

响应包含：

```http
X-Request-ID: ...
ETag: "<revision>"
```

### 3.2 错误信封

```json
{
  "error": {
    "code": "rule_approval_required",
    "message": "This rule version needs two security approvals.",
    "request_id": "req_...",
    "retryable": false,
    "details": {
      "required": 2,
      "current": 1
    }
  }
}
```

错误 code 稳定，message 可本地化。敏感内部错误仅进入日志，不返回堆栈。

## 4. 任务 API

### 4.1 创建任务

```http
POST /v1/organizations/{org}/review-tasks
Idempotency-Key: 7bb...
Content-Type: application/json

{
  "repository_id": "repo_...",
  "review_number": 8421,
  "mode": "deep",
  "rule_set_ids": ["rset_..."],
  "source": "console"
}
```

```http
HTTP/1.1 202 Accepted
Location: /v1/organizations/acme/review-tasks/task_1842

{
  "data": {
    "id": "task_1842",
    "state": "queued",
    "revision": 1,
    "created_at": "2026-09-17T08:00:00Z",
    "links": {
      "self": "/v1/organizations/acme/review-tasks/task_1842",
      "events": "/v1/organizations/acme/review-tasks/task_1842/events"
    }
  }
}
```

### 4.2 查询与列表

```text
GET /organizations/{org}/review-tasks/{id}
GET /organizations/{org}/review-tasks?state=running&repository_id=...&cursor=...
GET /organizations/{org}/review-tasks/{id}/findings?severity=critical
GET /organizations/{org}/review-tasks/{id}/events
```

Task resource 必须返回：request/run IDs、state、revision、stage、progress、head SHA、mode、rule snapshot、engine、timestamps、error（安全摘要）、usage estimate、permissions/actions。

### 4.3 取消与重试

```http
POST /organizations/{org}/review-tasks/{id}:cancel
If-Match: "7"

{"reason":"Superseded by local verification"}
```

返回 `202` 和 `state: cancel_requested`。不承诺立即停止。

```http
POST /organizations/{org}/review-tasks/{id}:retry
Idempotency-Key: ...

{"from_stage":"publishing","reason":"Provider access restored"}
```

仅允许服务端列出的 `allowed_retry_stages`；普通用户不能跳过失败的审查阶段直接发布。

## 5. SSE 契约

```http
GET /v1/organizations/{org}/review-tasks/{id}/events
Accept: text/event-stream
Last-Event-ID: 48192
```

```text
id: 48193
event: task.stage_changed.v1
data: {"task_id":"task_1842","revision":8,"from":"preparing","to":"reviewing","at":"..."}

id: 48194
event: task.progress.v1
data: {"task_id":"task_1842","revision":8,"stage":"reviewing","percent":68,"summary":"Analyzing 24 of 37 files"}
```

要求：

- `id` 是租户作用域内可恢复的单调事件序号。
- 客户端重连发送 Last-Event-ID；服务端从 `task_events` 补发。
- 心跳 comment 每 15 秒；代理不得缓冲。
- UI 收到 revision 小于当前值的事件必须丢弃。
- progress 允许被合并/跳过；state transition 不允许丢失。

## 6. 规则 API

```text
POST   /organizations/{org}/rule-sets
GET    /organizations/{org}/rule-sets
GET    /organizations/{org}/rule-sets/{id}
POST   /organizations/{org}/rule-sets/{id}/versions
PATCH  /organizations/{org}/rule-sets/{id}/versions/{version}
POST   /organizations/{org}/rule-sets/{id}/versions/{version}:validate
POST   /organizations/{org}/rule-sets/{id}/versions/{version}:preview-impact
POST   /organizations/{org}/rule-sets/{id}/versions/{version}:request-approval
POST   /organizations/{org}/rule-sets/{id}/versions/{version}:approve
POST   /organizations/{org}/rule-sets/{id}/versions/{version}:publish
POST   /organizations/{org}/rule-bindings
PATCH  /organizations/{org}/rule-bindings/{id}
GET    /organizations/{org}/rule-snapshots/{id}
```

所有修改版本的操作需要 `If-Match`；发布端点要求 `Idempotency-Key`。published version 的 PATCH 返回 `409 immutable_version`。

## 7. 集成与仓库 API

```text
GET    /organizations/{org}/provider-installations
POST   /organizations/{org}/provider-installations/{id}:sync
DELETE /organizations/{org}/provider-installations/{id}
GET    /organizations/{org}/repositories
GET    /organizations/{org}/repositories/{id}
PATCH  /organizations/{org}/repositories/{id}/review-settings
POST   /organizations/{org}/repositories/{id}:test-connection
```

安装 OAuth/App 回调使用一次性 state、PKCE（适用时）和短期 session，不接收前端提交的长期 token。

## 8. 用量与审计 API

```text
GET /organizations/{org}/usage/summary?from=...&to=...&group_by=repository
GET /organizations/{org}/usage/runs?cursor=...
GET /organizations/{org}/quotas
PATCH /organizations/{org}/quotas/{meter}
GET /organizations/{org}/audit-events?action=rule.publish&cursor=...
POST /organizations/{org}/audit-exports
```

导出为异步任务；完成后返回短期 signed URL，并审计下载行为。

## 9. 内部事件信封

```json
{
  "event_id": "evt_01J...",
  "event_type": "review.run.requested",
  "schema_version": 1,
  "occurred_at": "2026-09-17T08:00:00Z",
  "tenant_id": "ten_...",
  "aggregate": {
    "type": "review_run",
    "id": "run_...",
    "revision": 1
  },
  "correlation_id": "req_...",
  "causation_id": "delivery_...",
  "traceparent": "00-...",
  "payload": {
    "request_id": "rr_..."
  }
}
```

事件兼容规则：

- event type 语义变化时发布新版本，不复用旧名称。
- 同版本只允许新增可选字段；消费者忽略未知字段。
- 删除/改义/必填字段变化需要 schema version + migration window。
- payload 不包含 secret 或大对象，使用 `*_ref`。

## 10. 关键内部事件

| 事件 | 生产者 | 主要消费者 |
| --- | --- | --- |
| `interaction.received.v1` | webhook edge | ack worker, planner |
| `review.run.requested.v1` | task service | planner |
| `review.run.admitted.v1` | planner | workspace worker |
| `review.workspace.ready.v1` | workspace worker | review worker |
| `review.analysis.completed.v1` | review worker | normalizer |
| `review.findings.ready.v1` | normalizer | publisher |
| `review.publication.requested.v1` | orchestrator | provider publisher |
| `review.run.terminal.v1` | orchestrator/publisher | ack, usage, notification |
| `rule.version.published.v1` | rule service | cache invalidator, audit |
| `usage.recorded.v1` | workers | usage aggregator |

## 11. Webhook API

```text
POST /v1/webhooks/github
POST /v1/webhooks/gitlab
```

行为：

- 限制 body（默认 2 MiB，可按 provider 评估）。
- 先验签再解析业务 JSON。
- 通过 installation + base URL 找租户；不信任 payload 内的 tenant ID。
- unknown event 返回 `202 ignored`，避免 provider 重试风暴；验签失败返回 `401/403`。
- 数据库不可用返回 `503`，促使 provider 安全重试；不得先返回 2xx 再内存异步保存。

## 12. API 可演进性

- OpenAPI 是对外契约源；SDK 由规范生成。
- response 中的 `actions` 告诉 UI 当前用户可执行动作，减少复制状态机，但不取代服务端授权。
- beta 字段使用明确命名空间或 capability；不通过默默改变默认值发布破坏性变化。
- provider adapter 的差异不泄漏到通用 Task API；需要时放入 `provider_details` 可选对象。
