package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentWorkflowPolicy is frozen on admission. Legacy tasks retain their
// original execution contract; enabling a repository never expands them.
type AgentWorkflowPolicy struct {
	Enabled         bool     `json:"enabled"`
	MaxRepairCycles int      `json:"max_repair_cycles"`
	MaxTaskAttempts int      `json:"max_task_attempts"`
	RequiredChecks  []string `json:"required_checks,omitempty"`
}

func (p AgentWorkflowPolicy) Valid() bool {
	if !p.Enabled {
		return p.MaxRepairCycles == 0 && p.MaxTaskAttempts == 0 && len(p.RequiredChecks) == 0
	}
	if p.MaxRepairCycles < 0 || p.MaxRepairCycles > 3 || p.MaxTaskAttempts < 1 || p.MaxTaskAttempts > 5 || len(p.RequiredChecks) > 10 {
		return false
	}
	seen := map[string]bool{}
	for _, name := range p.RequiredChecks {
		if name != strings.TrimSpace(name) || len(name) < 1 || len(name) > 180 || strings.ContainsAny(name, "\r\n\x00") || seen[name] || strings.EqualFold(name, "Open Review / Analysis") {
			return false
		}
		seen[name] = true
	}
	return true
}

// Acceptance belongs to one exact delivered commit, independently of the
// coding task's legacy completed (Draft delivered) state.
type AgentTaskAcceptance struct {
	RemediationTaskID *uuid.UUID `json:"remediation_task_id,omitempty"`
	CanDecide         bool       `json:"can_decide"`
	Evidence          []string   `json:"evidence,omitempty"`
	TaskID            uuid.UUID  `json:"task_id"`
	AttemptID         uuid.UUID  `json:"attempt_id"`
	HeadSHA           string     `json:"head_sha"`
	Revision          int        `json:"revision"`
	State             string     `json:"state"`
	Criteria          []string   `json:"criteria"`
	ReviewRunID       *uuid.UUID `json:"review_run_id,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	DecidedBy         string     `json:"decided_by,omitempty"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type AgentTaskAcceptanceInput struct {
	Revision int      `json:"revision"`
	HeadSHA  string   `json:"head_sha"`
	Decision string   `json:"decision"`
	Evidence []string `json:"evidence"`
	Reason   string   `json:"reason"`
}

func (input AgentTaskAcceptanceInput) Valid() bool {
	if input.Revision < 1 || !validAgentHeadSHAForWorkflow(input.HeadSHA) || (input.Decision != "accepted" && input.Decision != "changes_requested") || len(strings.TrimSpace(input.Reason)) < 3 || len(input.Reason) > 2000 || strings.ContainsRune(input.Reason, 0) || len(input.Evidence) > 20 {
		return false
	}
	for _, evidence := range input.Evidence {
		if len(strings.TrimSpace(evidence)) < 3 || len(evidence) > 2000 || strings.ContainsRune(evidence, 0) {
			return false
		}
	}
	return true
}

func validAgentHeadSHAForWorkflow(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, digit := range value {
		if !(digit >= '0' && digit <= '9' || digit >= 'a' && digit <= 'f') {
			return false
		}
	}
	return true
}

// Checks are considered only after a complete, exact-SHA provider snapshot.
// Neither Open Review's own success nor an absent CI proves independent tests.
func AgentChecksReady(observation ProviderCheckObservation, policy AgentWorkflowPolicy, head string) (bool, string) {
	if observation.State != "observed" || observation.HeadSHA != head || observation.Truncated || observation.Stale || observation.ObservedAt == nil || (time.Since(*observation.ObservedAt) > 10*time.Minute || time.Until(*observation.ObservedAt) > time.Minute) {
		return false, "waiting_for_current_checks"
	}
	success := map[string]bool{}
	for _, check := range observation.Checks {
		if check.Origin != "independent" {
			continue
		}
		if check.State != "success" && check.State != "passed" {
			return false, "independent_checks_not_passed"
		}
		success[check.Name] = true
	}
	for _, name := range policy.RequiredChecks {
		if !success[name] {
			return false, "required_check_missing"
		}
	}
	if len(success) == 0 {
		return false, "independent_checks_missing"
	}
	return true, ""
}
