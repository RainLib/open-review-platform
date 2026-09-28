# 25 — Agent 编码凭据代理

Agent 编码是独立于代码审核的写入边界。Jev 凭据只负责 Issue 分类；审核 GitHub App、GitLab OAuth 或 OCR 模型密钥均不能自动成为编码凭据。仅在仓库启用 Agent 策略、Issue 通过准入与计划审批、适配器领取精确 attempt 并消费一次性 start claim 后，私有代理才允许发行写入凭据。

## 发行路径

`adapter → HMAC broker → PostgreSQL live grant → exact coding identity → adapter`。代理再次检查 attempt、job、lease、安装、仓库、provider、API origin、计划摘要与发行次数；token 不进入任务提交、RabbitMQ、审计或沙箱子容器。适配器只在克隆、推送和 Draft PR/MR API 调用期间持有返回的 token。

- GitHub：独立 Coding App，显式映射审核安装到 Coding App 安装，按单仓库请求安装 token。未配置 App 或缺少仓库写权限时拒绝启动或发行。
- GitLab：独立、部署管理的仓库编码凭据映射，代理按安装 UUID、provider、API base 和仓库精确读取。映射每次发行时重新读取以支持撤销与轮换；它不是审核 OAuth token，也**不是每任务临时生成的 token**。必须使用对应仓库的 GitLab 项目访问令牌，不能用个人或群组令牌代替。
- GitLab 映射在 broker 启动时逐条检查 provider 与 API/clone 同源关系；混入 GitHub 条目、跨域克隆或未显式允许的 HTTP 会使 broker 拒绝启动。发行时仍重新读取并检查同一关系，避免启动后的文件轮换绕过校验。
- GitLab 每次发行前还用该令牌只读查询精确项目、`/user` 与 `/personal_access_tokens/self`：项目路径必须与授权仓库一致，账户名必须匹配该项目 ID 的 `project_<id>_bot_*`，token 元数据必须属于同一 bot、处于 active 且未 revoked，并同时含 `api` 与 `write_repository`。GitLab [项目访问令牌文档](https://docs.gitlab.com/user/project/settings/project_access_tokens/)说明这种 bot 只属于项目；[令牌范围文档](https://docs.gitlab.com/security/tokens/access_token_scopes/)区分 API 写入与 Git-over-HTTP 推送。查询拒绝重定向，不在日志或响应中暴露 token；身份、范围、状态不符，或者自建版本不支持只读 token-self 端点，均拒绝发行。这个预检仍不能证明 bot 的实际项目角色、分支保护、真实 push/MR 能力或到期轮换，仍需 provider 端验收。
- 未配置的 provider、过期/取消的任务、错误安装或仓库、失配的克隆 origin 一律拒绝。自建 GitLab 的 HTTP 只允许在 `ENVIRONMENT=development` 且显式设置 `AGENT_CODING_GITLAB_ALLOW_HTTP=true`。

## GitLab 私有映射

将以下结构保存在代理主机的 0600 私有文件中，属主与 `AGENT_CREDENTIAL_BROKER_UID` 一致；不要提交到 Git、挂载进审核 worker 或 Agent 沙箱：

```json
{
  "version": 1,
  "entries": [{
    "installation_id": "00000000-0000-4000-8000-000000000000",
    "provider": "gitlab",
    "api_base_url": "https://gitlab.example/api/v4",
    "repository": "team/project",
    "clone_base_url": "https://gitlab.example",
    "token": "<separately-provisioned-repository-coding-token>"
  }]
}
```

配置 `AGENT_CODING_GITLAB_CREDENTIALS_HOST_PATH` 后，Compose 只把该文件挂载到私有 broker；GitHub-only 时该入口为空。GitHub 与 GitLab 可独立配置或同时配置。适配器使用 `AGENT_ADAPTER_CREDENTIAL_BROKER_URL` 和共享的 `AGENT_CREDENTIAL_BROKER_SECRET`，后者与 runner/adapter 回调签名密钥必须不同。生产代理要求 TLS，适配器应部署在单独的受控执行主机。

当前源码验证覆盖精确授权、失效租约、改写安装/仓库/API、跨域克隆地址、映射轮换、项目 bot 身份拒绝个人/群组/其他项目令牌，以及 token-self 缺失、撤销、停用或缺少任一写入范围的拒绝；签名 broker 与项目身份校验还通过隔离 PostgreSQL 联通测试。此前的 Compose 条件挂载及镜像构建证据早于本次权限预检改动，不能视为新版镜像已部署。真实 GitLab 编码 token 的实际推送权限、到期轮换、Draft MR 创建/更新和运行态代理部署尚未验收；在这些证据出现前，不打开自动编码策略。
一个仅含合成映射的 GitLab-only 代理容器已在本地连接控制库并对未签名请求返回 401；该容器和测试映射已删除。它不代表真实凭据发行或真实 Draft MR 验收。
