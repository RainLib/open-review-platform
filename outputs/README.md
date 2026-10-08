# Selected acceptance evidence

These dated records cover the maintainers' own `RainLib/open-review-platform` acceptance repository. They are historical observations, not provider credentials, deployment configuration, or a claim that all deployments are accepted. Commit IDs and `uncommitted_changes` flags describe the capture time; subsequent source delivery does not rewrite them.

| Record | What it supports |
| --- | --- |
| [Initial private-deployment summary](acceptance-20261007/acceptance-summary.json) | First review, rule, and Agent evaluation boundaries. |
| [Execution reliability](acceptance-20261007/execution-reliability-summary.json) | Readiness, bounded failures, deployment identities, and retained history. |
| [Isolated feedback verification](acceptance-20261007/agent-feedback-isolated-verified.json) | Coding and independent verification without formal provider publication. |
| [Model route/retry continuation](acceptance-20261007/model-route-and-retry-continuation-summary.json) | Diagnostic limits and preserved exhausted historical budget. |
| [New formal initial delivery](acceptance-20261007/new-formal-initial-delivery.json) | Plan approval, fail/repair/pass sequence, and first Draft delivery. |
| [New formal feedback and acceptance](acceptance-20261007/new-formal-acceptance-summary.json) | Same-Draft feedback, exact-head review/CI, read-only confirmation recovery, and pending human acceptance. |
| [Awaiting acceptance screenshot](acceptance-20261007/new-formal-awaiting-acceptance.png) | Real Console state at capture; no final acceptance was submitted. |
| [Historical execution budget screenshot](acceptance-20261007/execution-budget-final.png) | Five historical attempts: one successful delivery and four needing attention. |
| [Core workflow i18n](i18n-20261008/validation-summary.json) | Source/build checks, initial browser results, and pending deployed regression recheck. |
| [Agent Chinese UI](i18n-20261008/agent-zh-CN.png) / [Approval settings Chinese UI](i18n-20261008/approval-settings-zh-CN.png) | Initial real-session interface rendering with governed evidence preserved. |

Only these selected records are archived in Git. Other local screenshots, raw build/test logs, private overrides, model payloads, authentication material, and backups remain outside this archive. The complete interpretation and open acceptance gates are in [the live acceptance report](../docs/iter-01/30-private-deployment-live-acceptance.md) and [the i18n report](../docs/iter-01/32-console-workflow-i18n-validation.md).
