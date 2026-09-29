package domain

import (
	"sort"
	"strings"
	"time"
)

type WorkspaceSetupStep string

const (
	WorkspaceSetupConnect     WorkspaceSetupStep = "connect"
	WorkspaceSetupReviewScope WorkspaceSetupStep = "review_scope"
	WorkspaceSetupLearning    WorkspaceSetupStep = "learning"
	WorkspaceSetupSeverity    WorkspaceSetupStep = "severity"
	WorkspaceSetupRules       WorkspaceSetupStep = "rules"
	WorkspaceSetupComplete    WorkspaceSetupStep = "complete"
)

func (step WorkspaceSetupStep) Valid() bool {
	switch step {
	case WorkspaceSetupConnect, WorkspaceSetupReviewScope, WorkspaceSetupLearning, WorkspaceSetupSeverity, WorkspaceSetupRules, WorkspaceSetupComplete:
		return true
	default:
		return false
	}
}

// CanAdvanceWorkspaceSetup makes the persisted checkpoint a state machine,
// rather than trusting a browser to report an arbitrary completed step.
func CanAdvanceWorkspaceSetup(current, next WorkspaceSetupStep) bool {
	switch current {
	case WorkspaceSetupConnect:
		return next == WorkspaceSetupReviewScope
	case WorkspaceSetupReviewScope:
		return next == WorkspaceSetupLearning
	case WorkspaceSetupLearning:
		return next == WorkspaceSetupSeverity
	case WorkspaceSetupSeverity:
		return next == WorkspaceSetupRules
	case WorkspaceSetupRules:
		return next == WorkspaceSetupComplete
	default:
		return false
	}
}

type WorkspaceSetupCheckpoint struct {
	CurrentStep      WorkspaceSetupStep         `json:"current_step"`
	Revision         int                        `json:"revision"`
	LearningBoundary *WorkspaceLearningBoundary `json:"learning_boundary,omitempty"`
	Readiness        *WorkspaceSetupReadiness   `json:"readiness,omitempty"`
	UpdatedBy        string                     `json:"updated_by,omitempty"`
	UpdatedAt        *time.Time                 `json:"updated_at,omitempty"`
	CompletedAt      *time.Time                 `json:"completed_at,omitempty"`
}

type WorkspaceSetupReadiness struct {
	CheckpointRevision         int                   `json:"checkpoint_revision"`
	InstallationID             string                `json:"installation_id"`
	Provider                   Provider              `json:"provider"`
	RepositoryScope            string                `json:"repository_scope"`
	GeneralConfigRevision      int                   `json:"general_config_revision"`
	GeneralConfigContentSHA256 string                `json:"general_config_content_sha256"`
	LearningMode               WorkspaceLearningMode `json:"learning_mode,omitempty"`
	RuleSetCount               int                   `json:"rule_set_count"`
	CreatedAt                  time.Time             `json:"created_at"`
}

type WorkspaceSetupCheckpointInput struct {
	CurrentStep      WorkspaceSetupStep
	ExpectedRevision int
	LearningBoundary *WorkspaceLearningBoundary
}

// WorkspaceLearningMode records an explicit onboarding decision. It does not
// enable model training: source code and historic review comments remain out
// of training in either mode. The choice tells operators whether the workspace
// starts from governed policy only or deliberately defers this setup concern.
type WorkspaceLearningMode string

const (
	WorkspaceLearningGovernedPolicy WorkspaceLearningMode = "governed_policy"
	WorkspaceLearningSkipped        WorkspaceLearningMode = "skipped"
)

func (mode WorkspaceLearningMode) Valid() bool {
	return mode == WorkspaceLearningGovernedPolicy || mode == WorkspaceLearningSkipped
}

type WorkspaceLearningBoundary struct {
	Mode               WorkspaceLearningMode `json:"mode"`
	ReviewerExclusions []string              `json:"reviewer_exclusions"`
}

// Normalize validates this small, auditable boundary before it is retained.
// Reviewer exclusions are meaningful only for a future opt-in learning source;
// retaining them now prevents a later feature from silently broadening scope.
func (boundary WorkspaceLearningBoundary) Normalize() (WorkspaceLearningBoundary, bool) {
	if !boundary.Mode.Valid() || len(boundary.ReviewerExclusions) > 100 {
		return WorkspaceLearningBoundary{}, false
	}
	if boundary.Mode == WorkspaceLearningSkipped && len(boundary.ReviewerExclusions) != 0 {
		return WorkspaceLearningBoundary{}, false
	}
	unique := make(map[string]struct{}, len(boundary.ReviewerExclusions))
	for _, subject := range boundary.ReviewerExclusions {
		subject = strings.TrimSpace(subject)
		if subject == "" || len(subject) > 256 {
			return WorkspaceLearningBoundary{}, false
		}
		unique[subject] = struct{}{}
	}
	boundary.ReviewerExclusions = make([]string, 0, len(unique))
	for subject := range unique {
		boundary.ReviewerExclusions = append(boundary.ReviewerExclusions, subject)
	}
	sort.Strings(boundary.ReviewerExclusions)
	return boundary, true
}
