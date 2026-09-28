// Package agentdecision owns the deterministic admission classifier used
// before a coding Agent can be planned or approved. It is intentionally pure:
// provider content is evidence, not executable instruction, and no model
// output can override its result.
package agentdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

const ClassifierVersion = "deterministic-v3"

// Classify returns a conservative decision over one frozen Issue snapshot.
// Every non-rejected result still requires a bounded plan and an explicit
// owner/admin approval; this classifier is not an automation switch.
func Classify(task domain.AgentTask, title, body string, labels []string) domain.AgentTaskClassification {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	evidence := strings.Join([]string{string(task.Provider), task.APIBaseURL, task.Repository, task.OriginKind, task.OriginRevision, task.SourceBaseRef, task.SourceBaseSHA, title, body, strings.Join(labels, "\n")}, "\n")
	digest := sha256.Sum256([]byte(evidence))
	result := domain.AgentTaskClassification{
		TaskID: task.ID, TaskRevision: task.Revision, SourceRevision: task.OriginRevision,
		Decision: "requires_human", RiskLevel: "low", Confidence: 70,
		NextAction:     "capture_source",
		SnapshotSHA256: hex.EncodeToString(digest[:]), ClassifierVersion: ClassifierVersion,
		Evaluation: []domain.AgentTaskEvaluation{
			{Stage: "judge", Outcome: "passed", Summary: "No deterministic prompt-injection or secret-exfiltration pattern was found.", Signals: []string{"untrusted_issue_snapshot"}},
			{Stage: "evaluate", Outcome: "passed", Summary: "The task is eligible only for a bounded, human-approved plan.", Signals: []string{"manual_approval_required"}},
			{Stage: "verify", Outcome: "passed", Summary: "Provider, repository and immutable Issue revision are present; source commit capture is still required.", Signals: []string{"provider_identity", "repository_scope", "issue_revision"}},
		},
		Reasons: []string{"The repository is in manual Agent mode; a bounded plan and owner/admin approval are still required."},
	}
	if task.SourceState == "ready" {
		result.NextAction = "await_plan_approval"
		result.Evaluation[2] = domain.AgentTaskEvaluation{Stage: "verify", Outcome: "passed", Summary: "Provider Issue revision and immutable repository source commit were read and frozen before planning.", Signals: []string{"provider_issue_revision", "source_commit_sha"}}
	}
	content := strings.ToLower(title + "\n" + body + "\n" + strings.Join(labels, "\n"))
	if containsAny(content, []string{
		"ignore previous instructions", "ignore all previous instructions", "system prompt",
		"reveal your prompt", "exfiltrate", "upload secrets", "print environment variables",
		"curl | sh", "curl|sh", "wget | sh", "base64 /etc", "忽略之前的指令",
		"泄露密钥", "上传密钥", "系统提示词",
	}) {
		result.Decision = "rejected"
		result.RiskLevel = "critical"
		result.Confidence = 99
		result.NextAction = "reject"
		result.Evaluation[0] = domain.AgentTaskEvaluation{Stage: "judge", Outcome: "blocked", Summary: "The untrusted Issue snapshot matched a prompt-injection or secret-exfiltration policy.", Signals: []string{"prompt_injection_or_secret_exfiltration"}}
		result.Evaluation[1] = domain.AgentTaskEvaluation{Stage: "evaluate", Outcome: "not_evaluated", Summary: "Risk and implementation suitability are not evaluated after a hard safety rejection."}
		result.Evaluation[2] = domain.AgentTaskEvaluation{Stage: "verify", Outcome: "not_evaluated", Summary: "Source capture is forbidden for this rejected task revision."}
		result.Reasons = []string{
			"The Issue snapshot contains text matching a prompt-injection or secret-exfiltration pattern.",
			"No plan, sandbox task, branch, or provider write can be created from this revision. A trusted owner must replace the Issue content and submit a new revision.",
		}
		return result
	}
	if title == "" || len(body) < 80 {
		result.Decision = "needs_context"
		result.RiskLevel = "unknown"
		result.Confidence = 95
		result.NextAction = "request_context"
		result.Evaluation[1] = domain.AgentTaskEvaluation{Stage: "evaluate", Outcome: "blocked", Summary: "The Issue does not contain enough observable context for a bounded implementation plan.", Signals: []string{"missing_problem_context"}}
		result.Evaluation[2] = domain.AgentTaskEvaluation{Stage: "verify", Outcome: "not_evaluated", Summary: "Source capture cannot make an underspecified request safe to plan."}
		result.Reasons = []string{"The Issue snapshot does not contain enough problem context to produce a safe implementation plan.", "Add observed behavior, expected behavior, and acceptance criteria, then request the task again on the new Issue revision."}
		return result
	}
	if containsAny(content, []string{"password", "secret", "credential", "token", "payment", "billing", "production", "deploy", "migration", "terraform", "kubernetes", ".github/workflows", ".gitlab-ci", "安全", "密钥", "凭证", "支付", "生产", "部署", "迁移"}) {
		result.RiskLevel = "high"
		result.Confidence = 90
		result.NextAction = "await_plan_approval"
		result.Evaluation[1] = domain.AgentTaskEvaluation{Stage: "evaluate", Outcome: "requires_human", Summary: "Sensitive or operational scope requires an independently approved plan.", Signals: []string{"sensitive_or_operational_scope"}}
		result.Reasons = []string{"The Issue snapshot references a sensitive or operational area.", "This task requires an owner-approved plan and must remain outside automatic execution."}
		return result
	}
	if !containsAny(content, []string{"acceptance criteria", "acceptance", "expected behavior", "expected result", "reproduce", "验收", "预期", "复现", "完成条件"}) {
		result.RiskLevel = "medium"
		result.Confidence = 85
		result.NextAction = "await_plan_approval"
		result.Evaluation[1] = domain.AgentTaskEvaluation{Stage: "evaluate", Outcome: "requires_human", Summary: "Acceptance criteria must be added to the bounded plan before approval.", Signals: []string{"missing_acceptance_criteria"}}
		result.Reasons = []string{"The Issue snapshot does not state verifiable acceptance criteria.", "A reviewer must add acceptance criteria to the plan before approval."}
	}
	return result
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
