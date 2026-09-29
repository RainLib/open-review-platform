package store

import (
	"context"
	"errors"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrUnknownInstallation             = errors.New("unknown or inactive provider installation")
	ErrAmbiguousInstallation           = errors.New("multiple active provider installations match the event")
	ErrNoQueuedJob                     = errors.New("no queued review job")
	ErrJobClaimLost                    = errors.New("review job is no longer claimed by this runner")
	ErrForbidden                       = errors.New("actor is not allowed to manage this tenant")
	ErrConflict                        = errors.New("resource already exists")
	ErrRevisionConflict                = errors.New("review run revision does not match")
	ErrInboxClaimLost                  = errors.New("inbox message claim is no longer held")
	ErrNotFound                        = errors.New("resource not found")
	ErrInvalidRuleBinding              = errors.New("rule binding is invalid")
	ErrInvalidRuleRollout              = errors.New("rule rollout is invalid")
	ErrRuleRolloutEvidence             = errors.New("rule rollout observation evidence is insufficient")
	ErrRuleRolloutRequired             = errors.New("governed rule rollout is required")
	ErrActiveRuleRollout               = errors.New("active rule rollout owns this binding")
	ErrInvalidProviderIdentity         = errors.New("provider identity is invalid")
	ErrInvalidInvitation               = errors.New("workspace invitation is invalid")
	ErrInvalidAccessRequest            = errors.New("workspace access request is invalid")
	ErrInvalidRuleSet                  = errors.New("rule set is invalid")
	ErrInvalidRuleCatalog              = errors.New("rule catalog request is invalid")
	ErrInvalidRuleApproval             = errors.New("rule approval is invalid")
	ErrInvalidRuleImpact               = errors.New("rule impact preview is invalid")
	ErrInvalidRuleException            = errors.New("rule exception is invalid")
	ErrInvalidNotification             = errors.New("notification configuration is invalid")
	ErrInvalidFindingFeedback          = errors.New("finding feedback is invalid")
	ErrInvalidFindingFilter            = errors.New("finding explorer filter is invalid")
	ErrInvalidIssueFilter              = errors.New("issue filter is invalid")
	ErrInvalidAuditFilter              = errors.New("audit filter is invalid")
	ErrInvalidWorkQueueFilter          = errors.New("work queue filter is invalid")
	ErrInvalidPullRequestFilter        = errors.New("pull request filter is invalid")
	ErrInvalidUsageEntitlement         = errors.New("usage entitlement is invalid")
	ErrInvalidUsageReconciliation      = errors.New("usage reconciliation request is invalid")
	ErrInvalidUsageExport              = errors.New("usage export request is invalid")
	ErrInvalidReviewConfig             = errors.New("review configuration is invalid")
	ErrInvalidIssueFormatTemplate      = errors.New("issue format template is invalid")
	ErrInvalidReviewConfigApproval     = errors.New("review configuration approval is invalid")
	ErrReviewConfigApprovalRequired    = errors.New("review configuration change requires independent approval")
	ErrSetupRepositoryScopeIncomplete  = errors.New("setup repository scope has no synchronized authorized repository")
	ErrSetupReviewPolicyIncomplete     = errors.New("setup merge policy has not been recorded")
	ErrWorkspaceSetupIncomplete        = errors.New("workspace setup is incomplete")
	ErrInvalidAPIKey                   = errors.New("API key configuration is invalid")
	ErrInvalidAPIKeyCredential         = errors.New("API key credential is invalid")
	ErrInvalidCLIReview                = errors.New("CLI review request is invalid")
	ErrInvalidRunRetry                 = errors.New("review run retry request is invalid")
	ErrInteractionResponseNotExhausted = errors.New("interaction response delivery has not exhausted its retries")
	ErrSelectedRuleSetUnavailable      = errors.New("selected rule set is not active for this review target")
	ErrInvalidSSO                      = errors.New("SSO configuration is invalid")
	ErrNoQueuedSSOProbe                = errors.New("no queued SSO probe")
	ErrSSOProbeClaimLost               = errors.New("SSO probe claim is no longer held")
	ErrSSODomainUnverified             = errors.New("SSO domain challenge was not observed")
	ErrSSONotReady                     = errors.New("SSO configuration is not ready for enforcement")
	ErrInvalidDataGovernance           = errors.New("data governance request is invalid")
	ErrInvalidIssueAction              = errors.New("issue action is invalid")
	ErrInvalidIssueAssignee            = errors.New("issue assignee is invalid")
	ErrNoQueuedModelProbe              = errors.New("no queued model probe")
	ErrModelProbeClaimLost             = errors.New("model probe lease is no longer held")
	ErrLegalHold                       = errors.New("data is protected by an active legal hold")
	ErrSeparationOfDuties              = errors.New("requester cannot approve this operation")
	ErrGovernanceNotCancellable        = errors.New("data governance job is not cancellable")
	ErrNoQueuedGovernanceJob           = errors.New("no executable data governance job")
	ErrGovernanceClaimLost             = errors.New("data governance job lease is no longer held")
	ErrGovernanceArtifactGone          = errors.New("data governance artifact is unavailable or expired")
	ErrQuotaExceeded                   = errors.New("tenant review quota is exhausted")
	ErrInvalidPlatformIncident         = errors.New("platform incident is invalid")
	ErrNoProviderIssueFeedbackPoll     = errors.New("no provider Issue feedback poll is due")
	ErrInvalidProviderIssueRetry       = errors.New("provider Issue analysis retry request is invalid")
	ErrInvalidAgentTask                = errors.New("agent task request is invalid")
	ErrInvalidAgentTaskPlan            = errors.New("agent task plan is invalid")
	ErrAgentTaskDisabled               = errors.New("agent task automation is disabled for this repository")
	ErrNoQueuedAgentTask               = errors.New("no executable agent task attempt")
	ErrAgentTaskClaimLost              = errors.New("agent task attempt lease is no longer held")
)

// Store owns durable state transitions. A job can only be produced by a
// verified provider delivery and every delivery is recorded once per provider.
type Store interface {
	CreateTenant(ctx context.Context, actor, slug, name string) (domain.Tenant, error)
	ListTenants(ctx context.Context, actor string, limit int) ([]domain.TenantSummary, error)
	ListAuditEvents(ctx context.Context, actor, tenantSlug string, filter domain.AuditFilter) ([]domain.AuditEvent, error)
	GetAuditEvent(ctx context.Context, actor, tenantSlug string, eventID uuid.UUID) (domain.AuditEvent, error)
	UpsertMembership(ctx context.Context, actor, tenantSlug, subject, role string) (domain.Membership, error)
	ListMemberships(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.Membership, error)
	SetMembershipActive(ctx context.Context, actor, tenantSlug, subject string, active bool) (domain.Membership, error)
	CreateWorkspaceInvitation(ctx context.Context, actor, tenantSlug string, input domain.WorkspaceInvitationInput) (domain.WorkspaceInvitationCreation, error)
	ListWorkspaceInvitations(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.WorkspaceInvitation, error)
	AcceptWorkspaceInvitation(ctx context.Context, actor, token string) (domain.Membership, error)
	RevokeWorkspaceInvitation(ctx context.Context, actor, tenantSlug string, invitationID uuid.UUID) (domain.WorkspaceInvitation, error)
	RequestWorkspaceAccess(ctx context.Context, actor, tenantSlug, note string) error
	ListWorkspaceAccessRequests(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.WorkspaceAccessRequest, error)
	DecideWorkspaceAccessRequest(ctx context.Context, actor, tenantSlug string, requestID uuid.UUID, expectedRevision int, decision string) (domain.WorkspaceAccessRequest, error)
	CreateAPIKey(ctx context.Context, actor, tenantSlug string, input domain.APIKeyInput) (domain.APIKeyCreation, error)
	ListAPIKeys(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, actor, tenantSlug string, keyID uuid.UUID) (domain.APIKey, error)
	AuthenticateAPIKey(ctx context.Context, secret string) (domain.APIKeyPrincipal, error)
	SubmitCLIReview(ctx context.Context, principal domain.APIKeyPrincipal, tenantSlug, idempotencyKey string, input domain.CLIReviewInput) (domain.CLIReviewSubmission, error)
	ListCLIReviewRuns(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.CLIReviewRun, error)
	GetCLIReview(ctx context.Context, principal domain.APIKeyPrincipal, runID uuid.UUID) (domain.CLIReviewRun, error)
	GetCLIReviewEvidence(ctx context.Context, principal domain.APIKeyPrincipal, runID uuid.UUID) (domain.ReviewEvidence, error)
	RequestCLIReviewCancellation(ctx context.Context, principal domain.APIKeyPrincipal, runID uuid.UUID, expectedRevision int) (domain.ReviewRun, error)
	GetSSOOverview(ctx context.Context, actor, tenantSlug string) (domain.SSOOverview, error)
	SaveSSOConfiguration(ctx context.Context, actor, tenantSlug string, input domain.SSOConfigurationInput) (domain.SSOOverview, error)
	RequestSSOProbe(ctx context.Context, actor, tenantSlug string, expectedRevision int) (domain.SSOProbeReceipt, error)
	ClaimSSOProbe(ctx context.Context, workerID string) (*domain.SSOProbeTarget, error)
	CompleteSSOProbe(ctx context.Context, receiptID uuid.UUID, workerID, metadataSHA256, errorCode, errorMessage string) error
	ListModelProbes(ctx context.Context, actor, tenantSlug string, scope domain.ReviewConfigScope, limit int) ([]domain.ModelProbeReceipt, error)
	RequestModelProbe(ctx context.Context, actor, tenantSlug string, input domain.ModelProbeInput) (domain.ModelProbeReceipt, error)
	ClaimModelProbe(ctx context.Context, workerID string) (*domain.ModelProbeTarget, error)
	CompleteModelProbe(ctx context.Context, receiptID uuid.UUID, workerID string, result domain.ModelProbeResult) error
	AddSSODomain(ctx context.Context, actor, tenantSlug, domain string) (domain.SSODomain, error)
	VerifySSODomain(ctx context.Context, actor, tenantSlug string, domainID uuid.UUID, observedTXT []string) (domain.SSODomain, error)
	CreateSSORoleMapping(ctx context.Context, actor, tenantSlug string, input domain.SSORoleMappingInput) (domain.SSORoleMapping, error)
	DeleteSSORoleMapping(ctx context.Context, actor, tenantSlug string, mappingID uuid.UUID, expectedRevision int) error
	EnforceSSO(ctx context.Context, actor, tenantSlug string, input domain.SSOEnforcementInput) (domain.SSOOverview, error)
	SuspendSSO(ctx context.Context, actor, tenantSlug string, expectedRevision int) (domain.SSOOverview, error)
	GetDataGovernanceOverview(ctx context.Context, actor, tenantSlug string) (domain.DataGovernanceOverview, error)
	CreateRetentionPolicy(ctx context.Context, actor, tenantSlug string, input domain.RetentionPolicyInput) (domain.RetentionPolicy, error)
	DecideRetentionPolicy(ctx context.Context, actor, tenantSlug string, policyID uuid.UUID, input domain.GovernanceDecisionInput) (domain.RetentionPolicy, error)
	CreateDataLegalHold(ctx context.Context, actor, tenantSlug string, input domain.DataLegalHoldInput) (domain.DataLegalHold, error)
	ReleaseDataLegalHold(ctx context.Context, actor, tenantSlug string, holdID uuid.UUID, expectedRevision int) (domain.DataLegalHold, error)
	CreateDataGovernanceJob(ctx context.Context, actor, tenantSlug string, input domain.DataGovernanceJobInput) (domain.DataGovernanceJob, error)
	DecideDataGovernanceJob(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, input domain.GovernanceDecisionInput) (domain.DataGovernanceJob, error)
	CancelDataGovernanceJob(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, expectedRevision int) (domain.DataGovernanceJob, error)
	RetryDataGovernanceJob(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, expectedRevision int, idempotencyKey, reason string) (domain.DataGovernanceJob, error)
	ClaimDataGovernanceJob(ctx context.Context, workerID string, lease time.Duration) (*domain.DataGovernanceJobTarget, error)
	RenewDataGovernanceJobClaim(ctx context.Context, jobID uuid.UUID, workerID string, lease time.Duration) error
	BuildDataGovernanceExport(ctx context.Context, target domain.DataGovernanceJobTarget, maxRecords int) ([]byte, map[string]int64, error)
	ExecuteDataGovernanceDeletion(ctx context.Context, target domain.DataGovernanceJobTarget) (map[string]any, error)
	CompleteDataGovernanceExport(ctx context.Context, target domain.DataGovernanceJobTarget, artifact domain.DataGovernanceArtifact, receipt map[string]any) error
	CompleteDataGovernanceOperation(ctx context.Context, target domain.DataGovernanceJobTarget, receipt map[string]any) error
	DeferDataGovernanceRegionMigration(ctx context.Context, target domain.DataGovernanceJobTarget, externalOperationID string, progress int, receipt map[string]any, availableAt time.Time) error
	CompleteDataGovernanceRegionMigration(ctx context.Context, target domain.DataGovernanceJobTarget, observed domain.DataResidency, receipt map[string]any) error
	FailDataGovernanceJob(ctx context.Context, target domain.DataGovernanceJobTarget, code, message string) error
	GetDataGovernanceArtifact(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID) (domain.DataGovernanceArtifact, error)
	RecordDataGovernanceArtifactDownload(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, plaintextSHA256 string) error
	GetPlatformHealthOverview(ctx context.Context, actor, tenantSlug string) (domain.PlatformHealthOverview, error)
	GetUsageDashboard(ctx context.Context, actor, tenantSlug string, limit int) (domain.UsageDashboard, error)
	ExportUsage(ctx context.Context, actor, tenantSlug string, periodStart time.Time) (domain.UsageExport, error)
	UpdateUsageEntitlement(ctx context.Context, actor, tenantSlug string, input domain.UsageEntitlementInput) (domain.UsageEntitlement, error)
	ReconcileUsage(ctx context.Context, actor, tenantSlug string, input domain.UsageReconciliationInput) (domain.UsageReconciliationReport, error)
	CreateInstallation(ctx context.Context, actor, tenantSlug string, input domain.InstallationInput) (domain.Installation, error)
	ListInstallations(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.InstallationSummary, error)
	GetInstallation(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID) (domain.InstallationSummary, error)
	ListInstallationRepositories(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID, query string, limit int) ([]domain.ProviderRepository, error)
	UpdateInstallationRepositoryScope(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID, input domain.InstallationRepositoryScopeInput) (domain.InstallationSummary, error)
	DeactivateInstallation(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID) (domain.InstallationSummary, error)
	ListInstallationWebhookReceipts(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID, limit int) ([]domain.InstallationWebhookReceipt, error)
	RequestInstallationVerification(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID) (domain.InstallationSummary, error)
	GetWorkspaceSetupCheckpoint(ctx context.Context, actor, tenantSlug string) (domain.WorkspaceSetupCheckpoint, error)
	UpdateWorkspaceSetupCheckpoint(ctx context.Context, actor, tenantSlug string, input domain.WorkspaceSetupCheckpointInput) (domain.WorkspaceSetupCheckpoint, error)
	CreateNotificationDestination(ctx context.Context, actor, tenantSlug string, input domain.NotificationDestinationInput) (domain.NotificationDestination, error)
	ListNotificationDestinations(ctx context.Context, actor, tenantSlug string) ([]domain.NotificationDestination, error)
	UpdateNotificationDestination(ctx context.Context, actor, tenantSlug string, destinationID uuid.UUID, input domain.NotificationDestinationUpdateInput) (domain.NotificationDestination, error)
	RequestNotificationTest(ctx context.Context, actor, tenantSlug string, destinationID uuid.UUID, input domain.NotificationTestInput) (domain.NotificationDeliverySummary, error)
	CreateNotificationRoute(ctx context.Context, actor, tenantSlug string, input domain.NotificationRouteInput) (domain.NotificationRoute, error)
	ListNotificationRoutes(ctx context.Context, actor, tenantSlug string) ([]domain.NotificationRoute, error)
	PreviewNotificationRoutes(ctx context.Context, actor, tenantSlug string, input domain.NotificationRoutePreviewInput) (domain.NotificationRoutePreview, error)
	UpdateNotificationRoute(ctx context.Context, actor, tenantSlug string, routeID uuid.UUID, input domain.NotificationRouteUpdateInput) (domain.NotificationRoute, error)
	ReorderNotificationRoutes(ctx context.Context, actor, tenantSlug string, input domain.NotificationRouteReorderInput) ([]domain.NotificationRoute, error)
	ListNotificationDeliveries(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.NotificationDeliverySummary, error)
	RetryNotificationDelivery(ctx context.Context, actor, tenantSlug string, deliveryID uuid.UUID) error
	CreateRuleSet(ctx context.Context, actor, tenantSlug string, input domain.RuleSetInput) (domain.RuleSetWithDraft, error)
	ListRuleSets(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleSet, error)
	ListRuleCatalog(ctx context.Context, actor, tenantSlug string) ([]domain.RuleCatalogEntry, error)
	InstallRuleCatalogEntry(ctx context.Context, actor, tenantSlug, catalogID string, input domain.RuleCatalogInstallInput) (domain.RuleCatalogInstallResult, error)
	ListRuleApprovalRequests(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleApprovalSummary, error)
	PreviewRuleVersionImpact(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int, input domain.RuleImpactPreviewInput) (domain.RuleImpactPreview, error)
	CreateRuleTestRun(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int, input domain.RuleTestRunInput) (domain.RuleTestRun, error)
	ListRuleTestRuns(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleTestRun, error)
	RequestRuleApproval(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int, input domain.RuleApprovalRequestInput) (domain.RuleApprovalRequest, error)
	DecideRuleApproval(ctx context.Context, actor, tenantSlug string, requestID uuid.UUID, input domain.RuleApprovalDecisionInput) (domain.RuleApprovalRequest, error)
	PublishRuleVersion(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int) (domain.RuleVersion, error)
	CreateRuleBinding(ctx context.Context, actor, tenantSlug string, input domain.RuleBindingInput) (domain.RuleBinding, error)
	UpdateRuleBinding(ctx context.Context, actor, tenantSlug string, bindingID uuid.UUID, input domain.RuleBindingUpdateInput) (domain.RuleBinding, error)
	ListRuleBindings(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleBinding, error)
	CreateRuleRollout(ctx context.Context, actor, tenantSlug string, input domain.RuleRolloutInput) (domain.RuleRollout, error)
	ListRuleRollouts(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleRollout, error)
	UpdateRuleRollout(ctx context.Context, actor, tenantSlug string, rolloutID uuid.UUID, input domain.RuleRolloutUpdateInput) (domain.RuleRollout, error)
	ListRuleRolloutComparisons(ctx context.Context, actor, tenantSlug string, rolloutID uuid.UUID, limit int) ([]domain.RuleRolloutComparison, error)
	CreateAgentTask(ctx context.Context, actor, tenantSlug string, input domain.AgentTaskInput) (domain.AgentTask, error)
	CreateAgentTaskFromProviderIssue(ctx context.Context, actor, tenantSlug string, analysisID uuid.UUID, expectedRevision int) (domain.AgentTask, error)
	ListAgentTasks(ctx context.Context, actor, tenantSlug string, limit int, before uuid.UUID) (domain.AgentTaskPage, error)
	GetAgentTask(ctx context.Context, actor, tenantSlug string, taskID uuid.UUID) (domain.AgentTaskDetail, error)
	RetryAgentTaskSource(ctx context.Context, actor, tenantSlug string, taskID uuid.UUID, expectedRevision int) (domain.AgentTask, error)
	CreateAgentTaskPlan(ctx context.Context, actor, tenantSlug string, taskID uuid.UUID, input domain.AgentTaskPlanInput) (domain.AgentTaskPlan, error)
	ApproveAgentTaskPlan(ctx context.Context, actor, tenantSlug string, taskID, planID uuid.UUID, input domain.AgentTaskPlanApprovalInput) (domain.AgentTaskPlan, error)
	ListAgentTaskPolicies(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.AgentTaskPolicy, error)
	GetAgentTaskPolicy(ctx context.Context, actor, tenantSlug string, provider domain.Provider, apiBaseURL, repository string) (domain.AgentTaskPolicy, error)
	SaveAgentTaskPolicy(ctx context.Context, actor, tenantSlug string, input domain.AgentTaskPolicyInput) (domain.AgentTaskPolicy, error)
	CreateRuleException(ctx context.Context, actor, tenantSlug string, input domain.RuleExceptionInput) (domain.RuleException, error)
	ListRuleExceptions(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleException, error)
	DecideRuleException(ctx context.Context, actor, tenantSlug string, exceptionID uuid.UUID, input domain.RuleExceptionDecisionInput) (domain.RuleException, error)
	RevokeRuleException(ctx context.Context, actor, tenantSlug string, exceptionID uuid.UUID) (domain.RuleException, error)
	UpsertProviderIdentity(ctx context.Context, actor, tenantSlug string, input domain.ProviderIdentity) (domain.ProviderIdentity, error)
	ProcessInteraction(ctx context.Context, input domain.InteractionCommand) (domain.InteractionOutcome, error)
	ProcessAgentTaskCommand(ctx context.Context, event domain.AgentTaskCommandEvent, command, normalized string) (domain.AgentTaskCommandOutcome, error)
	RecordFindingReaction(ctx context.Context, input domain.FindingReaction) error
	GetFindingFeedbackDashboard(ctx context.Context, actor, tenantSlug string, limit int) (domain.FindingFeedbackDashboard, error)
	ListFindings(ctx context.Context, actor, tenantSlug string, filter domain.FindingFilter) (domain.FindingPage, error)
	SetFindingDisposition(ctx context.Context, actor, tenantSlug string, findingID uuid.UUID, input domain.FindingDispositionInput) error
	ListIssues(ctx context.Context, actor, tenantSlug string, filter domain.IssueFilter) (domain.IssuePage, error)
	ListIssueViews(ctx context.Context, actor, tenantSlug string) (domain.IssueSavedViewPage, error)
	CreateIssueView(ctx context.Context, actor, tenantSlug string, input domain.IssueSavedViewInput) (domain.IssueSavedView, error)
	UpdateIssueView(ctx context.Context, actor, tenantSlug string, id uuid.UUID, input domain.IssueSavedViewInput) (domain.IssueSavedView, error)
	DeleteIssueView(ctx context.Context, actor, tenantSlug string, id uuid.UUID, revision int) error
	GetIssue(ctx context.Context, actor, tenantSlug string, issueID uuid.UUID) (domain.IssueDetail, error)
	MutateIssue(ctx context.Context, actor, tenantSlug string, issueID uuid.UUID, input domain.IssueActionInput) (domain.IssueDetail, error)
	GetIssueAutoCreatePolicy(ctx context.Context, actor, tenantSlug string) (domain.IssueAutoCreatePolicy, error)
	SaveIssueAutoCreatePolicy(ctx context.Context, actor, tenantSlug string, input domain.IssueAutoCreatePolicy) (domain.IssueAutoCreatePolicy, error)
	PreviewIssueAutoCreatePolicy(ctx context.Context, actor, tenantSlug string, input domain.IssueAutoCreatePolicy) (domain.IssueAutoCreatePreview, error)
	ListProviderIssueAnalyses(ctx context.Context, actor, tenantSlug string, filter domain.ProviderIssueAnalysisFilter) (domain.ProviderIssueAnalysisPage, error)
	GetProviderIssueAnalysis(ctx context.Context, actor, tenantSlug string, analysisID uuid.UUID) (domain.ProviderIssueAnalysisDetail, error)
	RetryProviderIssueAnalysis(ctx context.Context, actor, tenantSlug string, analysisID uuid.UUID, input domain.ProviderIssueAnalysisRetryInput) (domain.ProviderIssueAnalysisRetryResult, error)
	RecordProviderIssueReaction(ctx context.Context, input domain.ProviderIssueReaction) error
	ListWorkQueue(ctx context.Context, actor, tenantSlug string, filter domain.WorkQueueFilter) (domain.WorkQueuePage, error)
	ListPullRequests(ctx context.Context, actor, tenantSlug string, filter domain.PullRequestFilter) (domain.PullRequestPage, error)
	ListReviewRuns(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.ReviewRunSummary, error)
	GetReviewRun(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.ReviewRunSummary, error)
	GetReviewEvidence(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.ReviewEvidence, error)
	GetReviewConfig(ctx context.Context, actor, tenantSlug string, section domain.ReviewConfigSection, scope domain.ReviewConfigScope) (domain.ReviewConfigView, error)
	ListReviewConfigVersions(ctx context.Context, actor, tenantSlug string, section domain.ReviewConfigSection, scope domain.ReviewConfigScope, limit int) (domain.ReviewConfigHistory, error)
	ListIssueFormatTemplates(ctx context.Context, actor, tenantSlug string) ([]domain.IssueFormatTemplate, error)
	CreateIssueFormatTemplate(ctx context.Context, actor, tenantSlug string, input domain.IssueFormatTemplateInput) (domain.IssueFormatTemplate, error)
	UpdateIssueFormatTemplate(ctx context.Context, actor, tenantSlug string, templateID uuid.UUID, input domain.IssueFormatTemplateInput) (domain.IssueFormatTemplate, error)
	ArchiveIssueFormatTemplate(ctx context.Context, actor, tenantSlug string, templateID uuid.UUID, expectedRevision int) error
	SaveReviewConfig(ctx context.Context, actor, tenantSlug string, input domain.ReviewConfigInput) (domain.ReviewConfigView, error)
	RestoreInheritedReviewConfig(ctx context.Context, actor, tenantSlug string, input domain.ReviewConfigInput) (domain.ReviewConfigView, error)
	RequestReviewConfigChange(ctx context.Context, actor, tenantSlug string, input domain.ReviewConfigChangeRequestInput) (domain.ReviewConfigChangeRequest, error)
	ListReviewConfigChangeRequests(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.ReviewConfigChangeRequest, error)
	DecideReviewConfigChange(ctx context.Context, actor, tenantSlug string, requestID uuid.UUID, input domain.ReviewConfigChangeDecisionInput) (domain.ReviewConfigChangeRequest, error)
	GetRuleSnapshot(ctx context.Context, actor, tenantSlug string, runID uuid.UUID) (domain.RuleSnapshot, error)
	ListRunEvents(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, afterRevision int) ([]domain.RunEvent, error)
	RequestRunCancellation(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, expectedRevision int) (domain.ReviewRun, error)
	RequestRunRetry(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, input domain.RunRetryInput) (domain.RunRetryResult, error)
	RetryInteractionResponse(ctx context.Context, actor, tenantSlug string, runID uuid.UUID, input domain.RunRetryInput) (domain.InteractionResponseRetryResult, error)
	// AdvanceRun is used by durable stage consumers that receive a review-run
	// message before a runner has claimed its backing job.
	AdvanceRun(ctx context.Context, runID uuid.UUID, next domain.RunState) (domain.ReviewRun, error)
	AdvanceLegacyRun(ctx context.Context, jobID uuid.UUID, next domain.RunState) (domain.ReviewRun, error)
	Enqueue(ctx context.Context, event domain.InboundEvent) (job domain.ReviewJob, duplicate bool, err error)
	EnqueueProviderIssueAnalysis(ctx context.Context, event domain.ProviderIssueEvent) (domain.ProviderIssueAnalysisEnqueue, error)
	Claim(ctx context.Context, workerID string) (*domain.ReviewJob, error)
	// ClaimForRun makes a broker message an execution hint for its own run,
	// rather than allowing a consumer to claim an unrelated tenant job.
	ClaimForRun(ctx context.Context, workerID string, runID uuid.UUID) (*domain.ReviewJob, error)
	// RenewClaim extends an active worker lease. A worker must stop execution if
	// this operation reports a lost claim so a recovered worker cannot publish a
	// duplicate or stale review.
	RenewClaim(ctx context.Context, jobID uuid.UUID, workerID string, lease time.Duration) error
	RuleSnapshotForJob(ctx context.Context, jobID uuid.UUID) (domain.RuleSnapshot, error)
	SaveFindings(ctx context.Context, jobID uuid.UUID, findings []domain.Finding) error
	ExternalIssuePublication(ctx context.Context, receiptID uuid.UUID) (domain.ExternalIssuePublication, error)
	MarkExternalIssuePublicationCreated(ctx context.Context, receiptID uuid.UUID, externalID, externalURL string) error
	MarkExternalIssuePublicationFailed(ctx context.Context, receiptID uuid.UUID, message string) error
	MarkExternalIssuePublicationClosed(ctx context.Context, receiptID uuid.UUID) error
	MarkExternalIssueSyncFailed(ctx context.Context, receiptID uuid.UUID, message string) error
	CancelExternalIssuePublication(ctx context.Context, receiptID uuid.UUID, reason string) error
	Succeed(ctx context.Context, jobID uuid.UUID, workerID string) error
	Fail(ctx context.Context, jobID uuid.UUID, workerID, message string) error
	Cancel(ctx context.Context, jobID uuid.UUID, workerID string) error
	Close()
}

// ProviderOAuthCredentialStore is intentionally separate from Store so test
// doubles and read-only services do not gain an accidental secret capability.
// Only the control API writes encrypted material and only workers read it.
type ProviderOAuthCredentialStore interface {
	CreateProviderOAuthCredential(ctx context.Context, actor, tenantSlug string, input domain.ProviderOAuthCredentialInput) (domain.ProviderOAuthCredential, error)
	LoadProviderOAuthCredential(ctx context.Context, tenantID uuid.UUID, credentialRef string) (domain.ProviderOAuthCredential, error)
	RefreshProviderOAuthCredential(ctx context.Context, tenantID uuid.UUID, credentialRef string, refresh domain.ProviderOAuthCredentialRefresh) (bool, error)
}

// WorkflowStore is intentionally separate from the legacy runner Store. It
// allows the polling runner to remain a recovery path while new workers use
// revisioned runs and durable message hand-off.
type WorkflowStore interface {
	ClaimOutbox(ctx context.Context, relayID string, limit int) ([]domain.OutboxMessage, error)
	MarkOutboxPublished(ctx context.Context, messageID uuid.UUID, relayID string) error
	ReleaseOutbox(ctx context.Context, messageID uuid.UUID, relayID, reason string) error
	ClaimInbox(ctx context.Context, consumer string, messageID uuid.UUID) (claimToken uuid.UUID, claimed bool, err error)
	CompleteInbox(ctx context.Context, consumer string, messageID, claimToken uuid.UUID) error
	ReleaseInbox(ctx context.Context, consumer string, messageID, claimToken uuid.UUID, reason string) error
}
