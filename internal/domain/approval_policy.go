package domain

import "time"

// WorkspaceApprovalPolicy only governs Agent plans and rule-version votes.
// Other approval boundaries (including destructive data operations) stay separate.
type WorkspaceApprovalPolicy struct {
	AllowAgentPlanSelfApproval bool       `json:"allow_agent_plan_self_approval"`
	AllowRuleSelfApproval      bool       `json:"allow_rule_self_approval"`
	Revision                   int        `json:"revision"`
	UpdatedBy                  string     `json:"updated_by"`
	UpdatedAt                  *time.Time `json:"updated_at,omitempty"`
	CanUpdate                  bool       `json:"can_update"`
}

type WorkspaceApprovalPolicyInput struct {
	AllowAgentPlanSelfApproval *bool `json:"allow_agent_plan_self_approval"`
	AllowRuleSelfApproval      *bool `json:"allow_rule_self_approval"`
	ExpectedRevision           int   `json:"expected_revision"`
}

func (input WorkspaceApprovalPolicyInput) Valid() bool {
	return input.AllowAgentPlanSelfApproval != nil && input.AllowRuleSelfApproval != nil && input.ExpectedRevision > 0
}
