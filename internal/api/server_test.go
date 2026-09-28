package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/governance"
	"github.com/RainLib/open-review-platform/internal/identity"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type recordingStore struct {
	event                               domain.InboundEvent
	providerIssueEvent                  domain.ProviderIssueEvent
	workspaceAccessSlug                 string
	workspaceAccessNote                 string
	workspaceAccessErr                  error
	workspaceAccessRequests             []domain.WorkspaceAccessRequest
	workspaceAccessDecision             string
	workspaceAccessRevision             int
	providerIssueOutcome                domain.ProviderIssueAnalysisEnqueue
	providerIssueErr                    error
	automaticAgentTaskEvent             domain.ProviderIssueEvent
	automaticAgentTaskOutcome           domain.AgentTaskCommandOutcome
	automaticAgentTaskErr               error
	enqueueJob                          domain.ReviewJob
	interaction                         domain.InteractionCommand
	agentTaskCommandEvent               domain.AgentTaskCommandEvent
	agentTaskCommand                    string
	agentTaskCommandNormalized          string
	agentTaskCommandOutcome             domain.AgentTaskCommandOutcome
	agentTaskCommandErr                 error
	installation                        domain.Installation
	installationInput                   domain.InstallationInput
	createInstallationErr               error
	called                              bool
	interactionCalled                   bool
	createRuleSetErr                    error
	ruleCatalogEntries                  []domain.RuleCatalogEntry
	ruleCatalogInstall                  domain.RuleCatalogInstallResult
	ruleCatalogInstallID                string
	ruleCatalogInstallInput             domain.RuleCatalogInstallInput
	ruleCatalogErr                      error
	requestRuleApprovalErr              error
	decideRuleApprovalErr               error
	ruleApprovalRequests                []domain.RuleApprovalSummary
	listRuleApprovalRequestsErr         error
	ruleImpactPreview                   domain.RuleImpactPreview
	previewRuleImpactErr                error
	ruleImpactPreviewInput              domain.RuleImpactPreviewInput
	ruleExceptionInput                  domain.RuleExceptionInput
	ruleBindingInput                    domain.RuleBindingInput
	createBindingErr                    error
	updateBindingErr                    error
	ruleRolloutInput                    domain.RuleRolloutInput
	ruleRollouts                        []domain.RuleRollout
	ruleRolloutComparisons              []domain.RuleRolloutComparison
	createRuleRolloutErr                error
	updateRuleRolloutErr                error
	agentTaskPolicy                     domain.AgentTaskPolicy
	agentTaskPolicyInput                domain.AgentTaskPolicyInput
	agentTaskPolicyErr                  error
	agentTaskPolicies                   []domain.AgentTaskPolicy
	agentTask                           domain.AgentTask
	agentTaskInput                      domain.AgentTaskInput
	agentTaskErr                        error
	agentTaskListPage                   domain.AgentTaskPage
	agentTaskListBefore                 uuid.UUID
	agentTaskListErr                    error
	agentTaskFromAnalysisID             uuid.UUID
	agentTaskFromAnalysisRevision       int
	agentTaskFromAnalysisErr            error
	agentTaskSourceRetryRevision        int
	agentTaskSourceRetryErr             error
	agentTaskPlan                       domain.AgentTaskPlan
	agentTaskPlanInput                  domain.AgentTaskPlanInput
	agentTaskPlanErr                    error
	agentTaskPlanApproval               domain.AgentTaskPlanApprovalInput
	agentTaskPlanApprovalErr            error
	agentTaskCancellation               domain.AgentTaskCancellationInput
	agentTaskCancellationErr            error
	providerIdentityErr                 error
	providerIdentity                    domain.ProviderIdentity
	providerIdentityActor               string
	providerIdentityTenant              string
	listRunEventsErr                    error
	runEvents                           []domain.RunEvent
	listRunEventsAfter                  int
	listRunEventsHook                   func(context.Context, int)
	tenants                             []domain.TenantSummary
	listTenantsErr                      error
	listTenantsActor                    string
	listTenantsLimit                    int
	listInstallationsErr                error
	installationSummary                 domain.InstallationSummary
	getInstallationID                   uuid.UUID
	getInstallationErr                  error
	installationRepositories            []domain.ProviderRepository
	installationRepositoryID            uuid.UUID
	installationRepositoryQuery         string
	installationRepositoryErr           error
	installationScopeInput              domain.InstallationRepositoryScopeInput
	installationScopeID                 uuid.UUID
	installationScopeErr                error
	notificationDestination             domain.NotificationDestination
	notificationDestinationInput        domain.NotificationDestinationInput
	notificationTestDelivery            domain.NotificationDeliverySummary
	notificationTestDestinationID       uuid.UUID
	notificationTestInput               domain.NotificationTestInput
	requestNotificationTestErr          error
	notificationRoute                   domain.NotificationRoute
	notificationRouteInput              domain.NotificationRouteInput
	notificationRoutes                  []domain.NotificationRoute
	notificationRouteOrderInput         domain.NotificationRouteReorderInput
	reorderNotificationRoutesErr        error
	notificationRoutePreview            domain.NotificationRoutePreview
	notificationRoutePreviewInput       domain.NotificationRoutePreviewInput
	previewNotificationRoutesErr        error
	updateNotificationDestinationErr    error
	updateNotificationRouteErr          error
	retryNotificationDeliveryErr        error
	notificationDeliveries              []domain.NotificationDeliverySummary
	auditEvents                         []domain.AuditEvent
	listAuditEventsErr                  error
	auditFilter                         domain.AuditFilter
	auditEvent                          domain.AuditEvent
	auditEventID                        uuid.UUID
	getAuditEventErr                    error
	members                             []domain.Membership
	upsertMembershipErr                 error
	membershipActivationSubject         string
	membershipActivationState           bool
	membershipActivationErr             error
	workspaceInvitation                 domain.WorkspaceInvitationCreation
	workspaceInvitationInput            domain.WorkspaceInvitationInput
	workspaceInvitations                []domain.WorkspaceInvitation
	acceptedWorkspaceInvitation         domain.Membership
	acceptedWorkspaceInvitationToken    string
	revokedWorkspaceInvitationID        uuid.UUID
	workspaceInvitationErr              error
	usageDashboard                      domain.UsageDashboard
	usageDashboardErr                   error
	usageExport                         domain.UsageExport
	usageExportPeriod                   time.Time
	usageExportErr                      error
	usageEntitlement                    domain.UsageEntitlement
	usageEntitlementInput               domain.UsageEntitlementInput
	usageEntitlementErr                 error
	usageReconciliation                 domain.UsageReconciliationReport
	usageReconciliationInput            domain.UsageReconciliationInput
	usageReconciliationErr              error
	issues                              []domain.IssueSummary
	issuePage                           domain.IssuePage
	issueFilter                         domain.IssueFilter
	issueViewPage                       domain.IssueSavedViewPage
	issueSavedView                      domain.IssueSavedView
	issueViewInput                      domain.IssueSavedViewInput
	issueViewID                         uuid.UUID
	issueViewRevision                   int
	issueViewActor                      string
	issueViewTenant                     string
	issueViewErr                        error
	listIssuesErr                       error
	findingFilter                       domain.FindingFilter
	findingPage                         domain.FindingPage
	listFindingsErr                     error
	providerIssuePage                   domain.ProviderIssueAnalysisPage
	providerIssueFilter                 domain.ProviderIssueAnalysisFilter
	providerIssueDetail                 domain.ProviderIssueAnalysisDetail
	providerIssueReaction               domain.ProviderIssueReaction
	providerIssueAnalysisID             uuid.UUID
	providerIssueRetryInput             domain.ProviderIssueAnalysisRetryInput
	providerIssueRetryResult            domain.ProviderIssueAnalysisRetryResult
	providerIssueRetryErr               error
	listProviderIssuesErr               error
	getProviderIssueErr                 error
	workQueuePage                       domain.WorkQueuePage
	workQueueFilter                     domain.WorkQueueFilter
	listWorkQueueErr                    error
	pullRequestPage                     domain.PullRequestPage
	pullRequestFilter                   domain.PullRequestFilter
	listPullRequestErr                  error
	issue                               domain.IssueDetail
	getIssueErr                         error
	requestedIssueID                    uuid.UUID
	issueActionInput                    domain.IssueActionInput
	mutateIssueErr                      error
	issueAutoCreatePolicy               domain.IssueAutoCreatePolicy
	issueAutoCreatePolicyInput          domain.IssueAutoCreatePolicy
	issueAutoCreatePolicyErr            error
	issueAutoCreatePreview              domain.IssueAutoCreatePreview
	externalIssuePublication            domain.ExternalIssuePublication
	reviewEvidence                      domain.ReviewEvidence
	getReviewEvidenceErr                error
	requestedEvidenceRunID              uuid.UUID
	runRetry                            domain.RunRetryResult
	runRetryInput                       domain.RunRetryInput
	runRetryID                          uuid.UUID
	runRetryErr                         error
	interactionResponseRetry            domain.InteractionResponseRetryResult
	interactionResponseRetryInput       domain.RunRetryInput
	interactionResponseRetryRunID       uuid.UUID
	interactionResponseRetryErr         error
	reviewSchedules                     []domain.ReviewSchedule
	reviewSchedule                      domain.ReviewSchedule
	reviewScheduleInput                 domain.ReviewScheduleInput
	reviewScheduleRunID                 uuid.UUID
	reviewScheduleID                    uuid.UUID
	reviewScheduleExpectedRevision      int
	reviewScheduleLimit                 int
	reviewScheduleErr                   error
	reviewInterventions                 []domain.ReviewIntervention
	reviewIntervention                  domain.ReviewIntervention
	reviewInterventionFilter            domain.ReviewInterventionFilter
	reviewInterventionRunID             uuid.UUID
	reviewInterventionExpectedRevision  int
	reviewInterventionResolution        domain.ReviewInterventionResolutionInput
	reviewInterventionErr               error
	reviewConfig                        domain.ReviewConfigView
	reviewConfigHistory                 domain.ReviewConfigHistory
	issueFormatTemplates                []domain.IssueFormatTemplate
	issueFormatTemplate                 domain.IssueFormatTemplate
	issueFormatTemplateInput            domain.IssueFormatTemplateInput
	issueFormatTemplateID               uuid.UUID
	issueFormatTemplateExpectedRevision int
	issueFormatTemplateErr              error
	reviewConfigChange                  domain.ReviewConfigChangeRequest
	reviewConfigChanges                 []domain.ReviewConfigChangeRequest
	reviewConfigChangeInput             domain.ReviewConfigChangeRequestInput
	reviewConfigChangeDecisionInput     domain.ReviewConfigChangeDecisionInput
	reviewConfigChangeID                uuid.UUID
	reviewConfigChangeErr               error
	setupCheckpoint                     domain.WorkspaceSetupCheckpoint
	setupCheckpointInput                domain.WorkspaceSetupCheckpointInput
	setupCheckpointErr                  error
	reviewConfigInput                   domain.ReviewConfigInput
	reviewConfigErr                     error
	modelProbes                         []domain.ModelProbeReceipt
	modelProbe                          domain.ModelProbeReceipt
	modelProbeInput                     domain.ModelProbeInput
	modelProbeErr                       error
	installationVerificationID          uuid.UUID
	installationVerificationErr         error
	deactivatedInstallationID           uuid.UUID
	deactivatedInstallation             domain.InstallationSummary
	deactivateInstallationErr           error
	installationWebhookReceipts         []domain.InstallationWebhookReceipt
	installationWebhookReceiptID        uuid.UUID
	installationWebhookReceiptErr       error
	apiKeys                             []domain.APIKey
	apiKeyCreation                      domain.APIKeyCreation
	apiKeyInput                         domain.APIKeyInput
	apiKeyErr                           error
	revokedAPIKeyID                     uuid.UUID
	apiKeyPrincipal                     domain.APIKeyPrincipal
	cliReviewSubmission                 domain.CLIReviewSubmission
	cliReviewRuns                       []domain.CLIReviewRun
	cliReview                           domain.CLIReviewRun
	cliReviewEvidence                   domain.ReviewEvidence
	cliReviewInput                      domain.CLIReviewInput
	cliReviewIdempotencyKey             string
	cliReviewErr                        error
	ssoOverview                         domain.SSOOverview
	ssoConfigurationInput               domain.SSOConfigurationInput
	ssoProbeReceipt                     domain.SSOProbeReceipt
	ssoProbeExpectedRevision            int
	ssoDomain                           domain.SSODomain
	ssoDomainInput                      string
	ssoVerifiedDomainID                 uuid.UUID
	ssoObservedTXT                      []string
	ssoRoleMapping                      domain.SSORoleMapping
	ssoRoleMappingInput                 domain.SSORoleMappingInput
	ssoDeletedMappingID                 uuid.UUID
	ssoDeletedMappingRevision           int
	ssoEnforcementInput                 domain.SSOEnforcementInput
	ssoSuspensionRevision               int
	ssoErr                              error
	dataGovernanceOverview              domain.DataGovernanceOverview
	retentionPolicy                     domain.RetentionPolicy
	retentionPolicyInput                domain.RetentionPolicyInput
	governanceDecisionInput             domain.GovernanceDecisionInput
	legalHold                           domain.DataLegalHold
	legalHoldInput                      domain.DataLegalHoldInput
	releasedLegalHoldID                 uuid.UUID
	releasedLegalHoldRevision           int
	governanceJob                       domain.DataGovernanceJob
	governanceJobInput                  domain.DataGovernanceJobInput
	governanceJobID                     uuid.UUID
	governanceExpectedRevision          int
	governanceRetryIdempotencyKey       string
	governanceRetryReason               string
	dataGovernanceErr                   error
	governanceArtifact                  domain.DataGovernanceArtifact
	platformHealth                      domain.PlatformHealthOverview
	platformHealthErr                   error
}

type fixedAuthenticator struct {
	err error
}

func (a fixedAuthenticator) Authenticate(context.Context, *http.Request) (identity.Principal, error) {
	if a.err != nil {
		return identity.Principal{}, a.err
	}
	return identity.Principal{Subject: "operator"}, nil
}

func (s *recordingStore) CreateTenant(context.Context, string, string, string) (domain.Tenant, error) {
	return domain.Tenant{}, nil
}

func (s *recordingStore) ListTenants(_ context.Context, actor string, limit int) ([]domain.TenantSummary, error) {
	s.listTenantsActor = actor
	s.listTenantsLimit = limit
	return s.tenants, s.listTenantsErr
}

func (s *recordingStore) ListAuditEvents(_ context.Context, _ string, _ string, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	s.auditFilter = filter
	return s.auditEvents, s.listAuditEventsErr
}

func (s *recordingStore) GetAuditEvent(_ context.Context, _ string, _ string, eventID uuid.UUID) (domain.AuditEvent, error) {
	s.auditEventID = eventID
	return s.auditEvent, s.getAuditEventErr
}

func (s *recordingStore) UpsertMembership(context.Context, string, string, string, string) (domain.Membership, error) {
	return domain.Membership{}, s.upsertMembershipErr
}

func (s *recordingStore) ListMemberships(context.Context, string, string, int) ([]domain.Membership, error) {
	return s.members, nil
}

func (s *recordingStore) SetMembershipActive(_ context.Context, _ string, _ string, subject string, active bool) (domain.Membership, error) {
	s.membershipActivationSubject = subject
	s.membershipActivationState = active
	return domain.Membership{Subject: subject, Active: active}, s.membershipActivationErr
}

func (s *recordingStore) CreateWorkspaceInvitation(_ context.Context, _ string, _ string, input domain.WorkspaceInvitationInput) (domain.WorkspaceInvitationCreation, error) {
	s.workspaceInvitationInput = input
	return s.workspaceInvitation, s.workspaceInvitationErr
}

func (s *recordingStore) ListWorkspaceInvitations(context.Context, string, string, int) ([]domain.WorkspaceInvitation, error) {
	return s.workspaceInvitations, s.workspaceInvitationErr
}

func (s *recordingStore) AcceptWorkspaceInvitation(_ context.Context, _ string, token string) (domain.Membership, error) {
	s.acceptedWorkspaceInvitationToken = token
	return s.acceptedWorkspaceInvitation, s.workspaceInvitationErr
}

func (s *recordingStore) RevokeWorkspaceInvitation(_ context.Context, _ string, _ string, invitationID uuid.UUID) (domain.WorkspaceInvitation, error) {
	s.revokedWorkspaceInvitationID = invitationID
	return domain.WorkspaceInvitation{ID: invitationID}, s.workspaceInvitationErr
}

func (s *recordingStore) RequestWorkspaceAccess(_ context.Context, _, slug, note string) error {
	s.workspaceAccessSlug, s.workspaceAccessNote = slug, note
	return s.workspaceAccessErr
}

func (s *recordingStore) ListWorkspaceAccessRequests(context.Context, string, string, int) ([]domain.WorkspaceAccessRequest, error) {
	return s.workspaceAccessRequests, s.workspaceAccessErr
}

func (s *recordingStore) DecideWorkspaceAccessRequest(_ context.Context, _, _ string, id uuid.UUID, revision int, decision string) (domain.WorkspaceAccessRequest, error) {
	s.workspaceAccessDecision, s.workspaceAccessRevision = decision, revision
	return domain.WorkspaceAccessRequest{ID: id, Revision: revision + 1, Status: decision}, s.workspaceAccessErr
}

func (s *recordingStore) CreateAPIKey(_ context.Context, _, _ string, input domain.APIKeyInput) (domain.APIKeyCreation, error) {
	s.apiKeyInput = input
	return s.apiKeyCreation, s.apiKeyErr
}

func (s *recordingStore) ListAPIKeys(context.Context, string, string, int) ([]domain.APIKey, error) {
	return s.apiKeys, s.apiKeyErr
}

func (s *recordingStore) RevokeAPIKey(_ context.Context, _, _ string, keyID uuid.UUID) (domain.APIKey, error) {
	s.revokedAPIKeyID = keyID
	if len(s.apiKeys) > 0 {
		return s.apiKeys[0], s.apiKeyErr
	}
	return domain.APIKey{ID: keyID}, s.apiKeyErr
}

func (s *recordingStore) AuthenticateAPIKey(context.Context, string) (domain.APIKeyPrincipal, error) {
	if s.apiKeyPrincipal.KeyID == uuid.Nil {
		return domain.APIKeyPrincipal{}, store.ErrInvalidAPIKeyCredential
	}
	return s.apiKeyPrincipal, nil
}

func (s *recordingStore) SubmitCLIReview(_ context.Context, _ domain.APIKeyPrincipal, _ string, idempotencyKey string, input domain.CLIReviewInput) (domain.CLIReviewSubmission, error) {
	s.cliReviewInput = input
	s.cliReviewIdempotencyKey = idempotencyKey
	return s.cliReviewSubmission, s.cliReviewErr
}

func (s *recordingStore) ListCLIReviewRuns(context.Context, string, string, int) ([]domain.CLIReviewRun, error) {
	return s.cliReviewRuns, s.cliReviewErr
}

func (s *recordingStore) GetCLIReview(context.Context, domain.APIKeyPrincipal, uuid.UUID) (domain.CLIReviewRun, error) {
	return s.cliReview, s.cliReviewErr
}

func (s *recordingStore) GetCLIReviewEvidence(context.Context, domain.APIKeyPrincipal, uuid.UUID) (domain.ReviewEvidence, error) {
	return s.cliReviewEvidence, s.cliReviewErr
}

func (s *recordingStore) RequestCLIReviewCancellation(context.Context, domain.APIKeyPrincipal, uuid.UUID, int) (domain.ReviewRun, error) {
	return s.cliReview.ReviewRun, s.cliReviewErr
}

func (s *recordingStore) GetSSOOverview(context.Context, string, string) (domain.SSOOverview, error) {
	return s.ssoOverview, s.ssoErr
}

func (s *recordingStore) SaveSSOConfiguration(_ context.Context, _, _ string, input domain.SSOConfigurationInput) (domain.SSOOverview, error) {
	s.ssoConfigurationInput = input
	return s.ssoOverview, s.ssoErr
}

func (s *recordingStore) RequestSSOProbe(_ context.Context, _, _ string, expectedRevision int) (domain.SSOProbeReceipt, error) {
	s.ssoProbeExpectedRevision = expectedRevision
	return s.ssoProbeReceipt, s.ssoErr
}

func (s *recordingStore) ClaimSSOProbe(context.Context, string) (*domain.SSOProbeTarget, error) {
	return nil, store.ErrNoQueuedSSOProbe
}

func (s *recordingStore) CompleteSSOProbe(context.Context, uuid.UUID, string, string, string, string) error {
	return s.ssoErr
}

func (s *recordingStore) ListModelProbes(_ context.Context, _, _ string, scope domain.ReviewConfigScope, _ int) ([]domain.ModelProbeReceipt, error) {
	s.modelProbeInput.ScopeKind, s.modelProbeInput.ScopeRef = scope.Kind, scope.Ref
	s.modelProbeInput.ScopeProvider, s.modelProbeInput.ScopeAPIBaseURL = scope.Provider, scope.APIBaseURL
	return s.modelProbes, s.modelProbeErr
}

func (s *recordingStore) RequestModelProbe(_ context.Context, _, _ string, input domain.ModelProbeInput) (domain.ModelProbeReceipt, error) {
	s.modelProbeInput = input
	return s.modelProbe, s.modelProbeErr
}

func (*recordingStore) ClaimModelProbe(context.Context, string) (*domain.ModelProbeTarget, error) {
	return nil, store.ErrNoQueuedModelProbe
}

func (s *recordingStore) CompleteModelProbe(context.Context, uuid.UUID, string, domain.ModelProbeResult) error {
	return s.modelProbeErr
}

func (s *recordingStore) AddSSODomain(_ context.Context, _, _, domainName string) (domain.SSODomain, error) {
	s.ssoDomainInput = domainName
	return s.ssoDomain, s.ssoErr
}

func (s *recordingStore) VerifySSODomain(_ context.Context, _, _ string, domainID uuid.UUID, observedTXT []string) (domain.SSODomain, error) {
	s.ssoVerifiedDomainID = domainID
	s.ssoObservedTXT = append([]string(nil), observedTXT...)
	return s.ssoDomain, s.ssoErr
}

func (s *recordingStore) CreateSSORoleMapping(_ context.Context, _, _ string, input domain.SSORoleMappingInput) (domain.SSORoleMapping, error) {
	s.ssoRoleMappingInput = input
	return s.ssoRoleMapping, s.ssoErr
}

func (s *recordingStore) DeleteSSORoleMapping(_ context.Context, _, _ string, mappingID uuid.UUID, revision int) error {
	s.ssoDeletedMappingID = mappingID
	s.ssoDeletedMappingRevision = revision
	return s.ssoErr
}

func (s *recordingStore) EnforceSSO(_ context.Context, _, _ string, input domain.SSOEnforcementInput) (domain.SSOOverview, error) {
	s.ssoEnforcementInput = input
	return s.ssoOverview, s.ssoErr
}

func (s *recordingStore) SuspendSSO(_ context.Context, _, _ string, revision int) (domain.SSOOverview, error) {
	s.ssoSuspensionRevision = revision
	return s.ssoOverview, s.ssoErr
}

func (s *recordingStore) GetDataGovernanceOverview(context.Context, string, string) (domain.DataGovernanceOverview, error) {
	return s.dataGovernanceOverview, s.dataGovernanceErr
}

func (s *recordingStore) CreateRetentionPolicy(_ context.Context, _, _ string, input domain.RetentionPolicyInput) (domain.RetentionPolicy, error) {
	s.retentionPolicyInput = input
	return s.retentionPolicy, s.dataGovernanceErr
}

func (s *recordingStore) DecideRetentionPolicy(_ context.Context, _, _ string, _ uuid.UUID, input domain.GovernanceDecisionInput) (domain.RetentionPolicy, error) {
	s.governanceDecisionInput = input
	return s.retentionPolicy, s.dataGovernanceErr
}

func (s *recordingStore) CreateDataLegalHold(_ context.Context, _, _ string, input domain.DataLegalHoldInput) (domain.DataLegalHold, error) {
	s.legalHoldInput = input
	return s.legalHold, s.dataGovernanceErr
}

func (s *recordingStore) ReleaseDataLegalHold(_ context.Context, _, _ string, holdID uuid.UUID, expectedRevision int) (domain.DataLegalHold, error) {
	s.releasedLegalHoldID = holdID
	s.releasedLegalHoldRevision = expectedRevision
	return s.legalHold, s.dataGovernanceErr
}

func (s *recordingStore) CreateDataGovernanceJob(_ context.Context, _, _ string, input domain.DataGovernanceJobInput) (domain.DataGovernanceJob, error) {
	s.governanceJobInput = input
	return s.governanceJob, s.dataGovernanceErr
}

func (s *recordingStore) DecideDataGovernanceJob(_ context.Context, _, _ string, jobID uuid.UUID, input domain.GovernanceDecisionInput) (domain.DataGovernanceJob, error) {
	s.governanceJobID = jobID
	s.governanceDecisionInput = input
	return s.governanceJob, s.dataGovernanceErr
}

func (s *recordingStore) CancelDataGovernanceJob(_ context.Context, _, _ string, jobID uuid.UUID, expectedRevision int) (domain.DataGovernanceJob, error) {
	s.governanceJobID = jobID
	s.governanceExpectedRevision = expectedRevision
	return s.governanceJob, s.dataGovernanceErr
}

func (s *recordingStore) RetryDataGovernanceJob(_ context.Context, _, _ string, jobID uuid.UUID, expectedRevision int, idempotencyKey, reason string) (domain.DataGovernanceJob, error) {
	s.governanceJobID = jobID
	s.governanceExpectedRevision = expectedRevision
	s.governanceRetryIdempotencyKey = idempotencyKey
	s.governanceRetryReason = reason
	return s.governanceJob, s.dataGovernanceErr
}

func (s *recordingStore) ClaimDataGovernanceJob(context.Context, string, time.Duration) (*domain.DataGovernanceJobTarget, error) {
	return nil, store.ErrNoQueuedGovernanceJob
}

func (s *recordingStore) RenewDataGovernanceJobClaim(context.Context, uuid.UUID, string, time.Duration) error {
	return nil
}

func (s *recordingStore) BuildDataGovernanceExport(context.Context, domain.DataGovernanceJobTarget, int) ([]byte, map[string]int64, error) {
	return nil, nil, nil
}

func (s *recordingStore) ExecuteDataGovernanceDeletion(context.Context, domain.DataGovernanceJobTarget) (map[string]any, error) {
	return nil, nil
}

func (s *recordingStore) CompleteDataGovernanceExport(context.Context, domain.DataGovernanceJobTarget, domain.DataGovernanceArtifact, map[string]any) error {
	return nil
}

func (s *recordingStore) CompleteDataGovernanceOperation(context.Context, domain.DataGovernanceJobTarget, map[string]any) error {
	return nil
}

func (s *recordingStore) DeferDataGovernanceRegionMigration(context.Context, domain.DataGovernanceJobTarget, string, int, map[string]any, time.Time) error {
	return nil
}

func (s *recordingStore) CompleteDataGovernanceRegionMigration(context.Context, domain.DataGovernanceJobTarget, domain.DataResidency, map[string]any) error {
	return nil
}

func (s *recordingStore) FailDataGovernanceJob(context.Context, domain.DataGovernanceJobTarget, string, string) error {
	return nil
}

func (s *recordingStore) GetDataGovernanceArtifact(context.Context, string, string, uuid.UUID) (domain.DataGovernanceArtifact, error) {
	return s.governanceArtifact, s.dataGovernanceErr
}

func (s *recordingStore) RecordDataGovernanceArtifactDownload(context.Context, string, string, uuid.UUID, string) error {
	return s.dataGovernanceErr
}

func (s *recordingStore) GetPlatformHealthOverview(context.Context, string, string) (domain.PlatformHealthOverview, error) {
	return s.platformHealth, s.platformHealthErr
}

func (s *recordingStore) GetUsageDashboard(context.Context, string, string, int) (domain.UsageDashboard, error) {
	return s.usageDashboard, s.usageDashboardErr
}

func (s *recordingStore) ExportUsage(_ context.Context, _, _ string, periodStart time.Time) (domain.UsageExport, error) {
	s.usageExportPeriod = periodStart
	return s.usageExport, s.usageExportErr
}

func (s *recordingStore) UpdateUsageEntitlement(_ context.Context, _, _ string, input domain.UsageEntitlementInput) (domain.UsageEntitlement, error) {
	s.usageEntitlementInput = input
	return s.usageEntitlement, s.usageEntitlementErr
}

func (s *recordingStore) ReconcileUsage(_ context.Context, _, _ string, input domain.UsageReconciliationInput) (domain.UsageReconciliationReport, error) {
	s.usageReconciliationInput = input
	return s.usageReconciliation, s.usageReconciliationErr
}

func (s *recordingStore) ListIssues(_ context.Context, _, _ string, filter domain.IssueFilter) (domain.IssuePage, error) {
	s.issueFilter = filter
	if s.issuePage.Issues != nil {
		return s.issuePage, s.listIssuesErr
	}
	return domain.IssuePage{Issues: s.issues}, s.listIssuesErr
}

func (s *recordingStore) GetIssue(_ context.Context, _, _ string, issueID uuid.UUID) (domain.IssueDetail, error) {
	s.requestedIssueID = issueID
	return s.issue, s.getIssueErr
}

func (s *recordingStore) ListProviderIssueAnalyses(_ context.Context, _, _ string, filter domain.ProviderIssueAnalysisFilter) (domain.ProviderIssueAnalysisPage, error) {
	s.providerIssueFilter = filter
	return s.providerIssuePage, s.listProviderIssuesErr
}

func (s *recordingStore) GetProviderIssueAnalysis(_ context.Context, _, _ string, analysisID uuid.UUID) (domain.ProviderIssueAnalysisDetail, error) {
	s.providerIssueAnalysisID = analysisID
	return s.providerIssueDetail, s.getProviderIssueErr
}

func (s *recordingStore) RetryProviderIssueAnalysis(_ context.Context, _, _ string, analysisID uuid.UUID, input domain.ProviderIssueAnalysisRetryInput) (domain.ProviderIssueAnalysisRetryResult, error) {
	s.providerIssueAnalysisID = analysisID
	s.providerIssueRetryInput = input
	return s.providerIssueRetryResult, s.providerIssueRetryErr
}

func (s *recordingStore) MutateIssue(_ context.Context, _, _ string, issueID uuid.UUID, input domain.IssueActionInput) (domain.IssueDetail, error) {
	s.requestedIssueID = issueID
	s.issueActionInput = input
	return s.issue, s.mutateIssueErr
}

func (s *recordingStore) GetIssueAutoCreatePolicy(context.Context, string, string) (domain.IssueAutoCreatePolicy, error) {
	return s.issueAutoCreatePolicy, s.issueAutoCreatePolicyErr
}

func (s *recordingStore) SaveIssueAutoCreatePolicy(_ context.Context, _, _ string, input domain.IssueAutoCreatePolicy) (domain.IssueAutoCreatePolicy, error) {
	s.issueAutoCreatePolicyInput = input
	return s.issueAutoCreatePolicy, s.issueAutoCreatePolicyErr
}

func (s *recordingStore) PreviewIssueAutoCreatePolicy(_ context.Context, _, _ string, input domain.IssueAutoCreatePolicy) (domain.IssueAutoCreatePreview, error) {
	s.issueAutoCreatePolicyInput = input
	return s.issueAutoCreatePreview, s.issueAutoCreatePolicyErr
}

func (s *recordingStore) ExternalIssuePublication(context.Context, uuid.UUID) (domain.ExternalIssuePublication, error) {
	return s.externalIssuePublication, nil
}

func (s *recordingStore) MarkExternalIssuePublicationCreated(context.Context, uuid.UUID, string, string) error {
	return nil
}
func (s *recordingStore) MarkExternalIssuePublicationFailed(context.Context, uuid.UUID, string) error {
	return nil
}
func (s *recordingStore) MarkExternalIssuePublicationClosed(context.Context, uuid.UUID) error {
	return nil
}
func (s *recordingStore) MarkExternalIssueSyncFailed(context.Context, uuid.UUID, string) error {
	return nil
}
func (s *recordingStore) CancelExternalIssuePublication(context.Context, uuid.UUID, string) error {
	return nil
}

func TestIssueEndpointsExposeStableAggregatesAndValidateFilters(t *testing.T) {
	issueID := uuid.New()
	backend := &recordingStore{
		issuePage: domain.IssuePage{
			Issues:     []domain.IssueSummary{{ID: issueID, Status: domain.IssueRegressed, Repository: "RainLib/open-review-platform", OccurrenceCount: 3}},
			Counts:     domain.IssueInboxCounts{Open: 12, Regressed: 3, Critical: 2, Assigned: 4, Resolved: 8, Suppressed: 1},
			Facets:     domain.IssueInboxFacets{Repositories: []string{"RainLib/open-review-platform"}, Categories: []string{"security"}},
			NextCursor: "next-page-cursor",
		},
		issue: domain.IssueDetail{IssueSummary: domain.IssueSummary{ID: issueID, Status: domain.IssueRegressed}, Occurrences: []domain.IssueOccurrence{{ID: uuid.New(), Active: true}}},
	}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues?status=regressed&severity=critical&repository=RainLib%2Fopen-review-platform&category=security&q=redirect&active=true&seen_after=2026-09-18T00%3A00%3A00Z&cursor=opaque-cursor&cursor_direction=after&limit=25", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), issueID.String()) || !strings.Contains(recorder.Body.String(), `"next_cursor":"next-page-cursor"`) || !strings.Contains(recorder.Body.String(), `"open":12`) || !strings.Contains(recorder.Body.String(), `"resolved":8`) || !strings.Contains(recorder.Body.String(), `"repositories":["RainLib/open-review-platform"]`) {
		t.Fatalf("GET issues status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if backend.issueFilter.Status != domain.IssueRegressed || backend.issueFilter.Severity != "critical" || backend.issueFilter.Repository != "RainLib/open-review-platform" || backend.issueFilter.Category != "security" || backend.issueFilter.Query != "redirect" || !backend.issueFilter.ActiveOnly || backend.issueFilter.Cursor != "opaque-cursor" || backend.issueFilter.CursorDirection != domain.IssueCursorAfter || backend.issueFilter.SeenAfter == nil || backend.issueFilter.Limit != 25 {
		t.Fatalf("unexpected issue filter: %#v", backend.issueFilter)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues?view=regressed&selected="+issueID.String(), nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.issueFilter.SelectedIssueID == nil || *backend.issueFilter.SelectedIssueID != issueID {
		t.Fatalf("selected issue filter status=%d body=%s filter=%#v", recorder.Code, recorder.Body.String(), backend.issueFilter)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues?selected=not-an-issue-id", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "selected must be an issue ID") {
		t.Fatalf("invalid selected issue status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues?assignee=me", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.issueFilter.AssigneeSubject != "operator" {
		t.Fatalf("assigned issues status=%d body=%s filter=%#v", recorder.Code, recorder.Body.String(), backend.issueFilter)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues/"+issueID.String(), nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"occurrences"`) || backend.requestedIssueID != issueID {
		t.Fatalf("GET issue status=%d body=%s id=%s", recorder.Code, recorder.Body.String(), backend.requestedIssueID)
	}

	request = httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/issues/"+issueID.String(), strings.NewReader(`{"action":"resolve","reason":"fixed in current head","expected_revision":4}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.issueActionInput.Action != "resolve" || backend.issueActionInput.ExpectedRevision != 4 || backend.issueActionInput.Reason != "fixed in current head" {
		t.Fatalf("PATCH issue status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.issueActionInput)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues?status=unknown", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "issue filter is invalid") {
		t.Fatalf("invalid filter status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues?assignee=another-user", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "assignee filter must be me") {
		t.Fatalf("invalid assignee filter status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues/not-a-uuid", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "issue id is invalid") {
		t.Fatalf("invalid issue id status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProviderIssueAnalysisEndpointsExposeSafeTenantEvidence(t *testing.T) {
	analysisID := uuid.New()
	installationID := uuid.New()
	now := time.Date(2026, 9, 21, 7, 30, 0, 0, time.UTC)
	backend := &recordingStore{
		providerIssuePage: domain.ProviderIssueAnalysisPage{
			Items: []domain.ProviderIssueAnalysisSummary{{
				ID: analysisID, InstallationID: installationID, Provider: domain.ProviderGitHub,
				APIBaseURL: "https://api.github.com", Repository: "RainLib/open-review-platform",
				IssueNumber: 9, Revision: 2, Action: "edited", Title: "Webhook retry contract",
				Author: "contributor", Labels: []string{"bug"}, State: domain.ProviderIssueAnalysisCompleted,
				ReceiptCount: 2, CreatedAt: now, UpdatedAt: now, ModelRouteSHA256: "model-hash", PromptConfigSHA256: "prompt-hash",
			}},
			Counts: domain.ProviderIssueAnalysisCounts{Completed: 1},
		},
		providerIssueDetail: domain.ProviderIssueAnalysisDetail{
			ProviderIssueAnalysisSummary: domain.ProviderIssueAnalysisSummary{
				ID: analysisID, InstallationID: installationID, Provider: domain.ProviderGitHub,
				APIBaseURL: "https://api.github.com", Repository: "RainLib/open-review-platform",
				IssueNumber: 9, Revision: 2, Action: "edited", Title: "Webhook retry contract",
				State: domain.ProviderIssueAnalysisCompleted, ReceiptCount: 2, CreatedAt: now, UpdatedAt: now,
				ModelRouteSHA256: "model-hash", PromptConfigSHA256: "prompt-hash",
			},
			Analysis: "## Outcome\nAnalysis completed.",
			Receipts: []domain.ProviderIssueAnalysisReceipt{{Revision: 2, Action: "edited", EventName: "issues", AdmittedAt: now}},
		},
	}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/provider-issues?state=completed&q=webhook&limit=25", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), analysisID.String()) || !strings.Contains(recorder.Body.String(), `"completed":1`) {
		t.Fatalf("GET provider issues status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if backend.providerIssueFilter.State != domain.ProviderIssueAnalysisCompleted || backend.providerIssueFilter.Query != "webhook" || backend.providerIssueFilter.Limit != 25 {
		t.Fatalf("unexpected provider issue filter: %#v", backend.providerIssueFilter)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/provider-issues?state=needs_attention", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.providerIssueFilter.State != domain.ProviderIssueAnalysisNeedsAttention {
		t.Fatalf("needs-attention provider issue filter status=%d filter=%#v", recorder.Code, backend.providerIssueFilter)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/provider-issues/"+analysisID.String(), nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"analysis":"## Outcome`) || !strings.Contains(body, `"receipts"`) || backend.providerIssueAnalysisID != analysisID {
		t.Fatalf("GET provider issue detail status=%d body=%s", recorder.Code, body)
	}
	for _, secretField := range []string{"credential_ref", "stable_marker", "model_route\"", "prompt_config\"", "\"body\""} {
		if strings.Contains(body, secretField) {
			t.Fatalf("provider issue response leaked %q: %s", secretField, body)
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/provider-issues?state=unknown", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider issue filter status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateAgentTaskFromProviderIssueEndpoint(t *testing.T) {
	analysisID, taskID := uuid.New(), uuid.New()
	backend := &recordingStore{agentTask: domain.AgentTask{ID: taskID, State: "received", Revision: 1}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)
	path := "/v1/tenants/acme/provider-issues/" + analysisID.String() + "/agent-task"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":2}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || backend.agentTaskFromAnalysisID != analysisID || backend.agentTaskFromAnalysisRevision != 2 || recorder.Header().Get("Location") != "/v1/tenants/acme/agent-tasks/"+taskID.String() {
		t.Fatalf("agent request status=%d body=%s location=%q analysis=%s revision=%d", recorder.Code, recorder.Body.String(), recorder.Header().Get("Location"), backend.agentTaskFromAnalysisID, backend.agentTaskFromAnalysisRevision)
	}
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{"stale Issue", store.ErrRevisionConflict, http.StatusConflict},
		{"manual policy disabled", store.ErrAgentTaskDisabled, http.StatusConflict},
		{"duplicate task", store.ErrConflict, http.StatusConflict},
		{"forbidden role", store.ErrForbidden, http.StatusForbidden},
		{"foreign analysis", store.ErrNotFound, http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend.agentTaskFromAnalysisErr = test.err
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":2}`))
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	backend.agentTaskFromAnalysisErr = nil
	for _, input := range []string{`{"expected_revision":0}`, `{}`} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(input))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("input=%s status=%d", input, recorder.Code)
		}
	}
}

func TestRetryProviderIssueAnalysisEndpoint(t *testing.T) {
	analysisID := uuid.New()
	backend := &recordingStore{providerIssueRetryResult: domain.ProviderIssueAnalysisRetryResult{
		AnalysisID: analysisID, Revision: 3, Attempt: 2, State: domain.ProviderIssueAnalysisQueued,
	}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/provider-issues/"+analysisID.String()+"/retry", strings.NewReader(`{"expected_revision":3,"idempotency_key":"issue-retry-0001"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Location") != "/v1/tenants/acme/provider-issues/"+analysisID.String() || !strings.Contains(recorder.Body.String(), `"attempt":2`) {
		t.Fatalf("retry provider issue status=%d location=%q body=%s", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
	}
	if backend.providerIssueAnalysisID != analysisID || backend.providerIssueRetryInput.ExpectedRevision != 3 || backend.providerIssueRetryInput.IdempotencyKey != "issue-retry-0001" {
		t.Fatalf("unexpected retry input id=%s input=%#v", backend.providerIssueAnalysisID, backend.providerIssueRetryInput)
	}

	for _, test := range []struct {
		name       string
		err        error
		statusCode int
		message    string
	}{
		{name: "invalid", err: store.ErrInvalidProviderIssueRetry, statusCode: http.StatusBadRequest, message: "retry request is invalid"},
		{name: "stale revision", err: store.ErrRevisionConflict, statusCode: http.StatusConflict, message: "revision changed"},
		{name: "not retryable", err: store.ErrConflict, statusCode: http.StatusConflict, message: "not retryable"},
		{name: "setup incomplete", err: store.ErrWorkspaceSetupIncomplete, statusCode: http.StatusConflict, message: "setup is incomplete"},
		{name: "hidden", err: store.ErrForbidden, statusCode: http.StatusNotFound, message: "not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend.providerIssueRetryErr = test.err
			request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/provider-issues/"+analysisID.String()+"/retry", strings.NewReader(`{"expected_revision":3,"idempotency_key":"issue-retry-0002"}`))
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != test.statusCode || !strings.Contains(recorder.Body.String(), test.message) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestIssueAutoCreatePolicyEndpointsPreserveRevisionAndPreviewDraft(t *testing.T) {
	backend := &recordingStore{
		issueAutoCreatePolicy:  domain.IssueAutoCreatePolicy{Revision: 3, Enabled: true, Target: "provider", RepositoryScopes: []string{"RainLib/*"}, MinimumSeverity: "high", TriggerFirstSeen: true, TitleTemplate: "[Review] {{severity}}", BodyTemplate: "{{evidence}}", CanManage: true},
		issueAutoCreatePreview: domain.IssueAutoCreatePreview{Candidates: []domain.IssueAutoCreatePreviewItem{{IssueID: uuid.New(), Repository: "RainLib/demo", Severity: "high"}}},
	}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issues/auto-create-policy", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"revision":3`) || !strings.Contains(recorder.Body.String(), `"can_manage":true`) {
		t.Fatalf("GET auto-create policy status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	body := `{"revision":3,"enabled":true,"target":"provider","repository_scopes":["RainLib/*"],"minimum_severity":"high","categories":["security"],"trigger_first_seen":true,"trigger_regressed":true,"repeat_occurrence_threshold":3,"labels":["open-review"],"title_template":"[Review] {{severity}}","body_template":"{{evidence}}"}`
	request = httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/issues/auto-create-policy", strings.NewReader(body))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.issueAutoCreatePolicyInput.Revision != 3 || backend.issueAutoCreatePolicyInput.RepeatOccurrenceThreshold != 3 || len(backend.issueAutoCreatePolicyInput.Categories) != 1 {
		t.Fatalf("PUT auto-create policy status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.issueAutoCreatePolicyInput)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/issues/auto-create-policy/preview", strings.NewReader(body))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"candidates"`) || backend.issueAutoCreatePolicyInput.TitleTemplate != "[Review] {{severity}}" {
		t.Fatalf("preview auto-create policy status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.issueAutoCreatePolicyInput)
	}

	backend.issueAutoCreatePolicyErr = store.ErrRevisionConflict
	request = httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/issues/auto-create-policy", strings.NewReader(body))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "refresh before saving") {
		t.Fatalf("conflicting policy save status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkQueueEndpointUsesBoundedServerFilter(t *testing.T) {
	runID := uuid.New()
	backend := &recordingStore{workQueuePage: domain.WorkQueuePage{
		Runs: []domain.ReviewRunSummary{{
			ReviewRun:  domain.ReviewRun{ID: runID, State: domain.RunNeedsAttention},
			Repository: "RainLib/open-review-platform", ReviewNumber: 3,
		}},
		Counts:     domain.WorkQueueCounts{Running: 7, NeedsAttention: 2},
		NextCursor: "opaque-next",
	}}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/work-queue?view=needs_attention&repository=RainLib%2Fopen-review-platform&q=provider+timeout&cursor=opaque-cursor&cursor_direction=after&limit=25", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), runID.String()) || !strings.Contains(response.Body.String(), `"needs_attention":2`) || !strings.Contains(response.Body.String(), `"next_cursor":"opaque-next"`) {
		t.Fatalf("GET work queue status=%d body=%s", response.Code, response.Body.String())
	}
	if backend.workQueueFilter.View != domain.WorkQueueNeedsAttention || backend.workQueueFilter.Repository != "RainLib/open-review-platform" || backend.workQueueFilter.Query != "provider timeout" || backend.workQueueFilter.Cursor != "opaque-cursor" || backend.workQueueFilter.CursorDirection != domain.WorkQueueCursorAfter || backend.workQueueFilter.Limit != 25 {
		t.Fatalf("work queue filter=%#v", backend.workQueueFilter)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/work-queue?view=invalid", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "work queue view or cursor is invalid") {
		t.Fatalf("invalid work queue status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPullRequestEndpointReturnsOneFilteredCurrentRunPage(t *testing.T) {
	runID := uuid.New()
	backend := &recordingStore{pullRequestPage: domain.PullRequestPage{
		Runs: []domain.ReviewRunSummary{{
			ReviewRun:  domain.ReviewRun{ID: runID, State: domain.RunCompleted},
			Repository: "RainLib/open-review-platform", ReviewNumber: 42,
		}},
		Counts:     domain.PullRequestCounts{Active: 5, Attention: 2, Completed: 9, All: 16},
		NextCursor: "next-review-page",
	}}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/pull-requests?view=completed&repository=RainLib%2Fopen-review-platform&q=%2342&cursor=opaque-cursor&cursor_direction=after&limit=25", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), runID.String()) || !strings.Contains(response.Body.String(), `"completed":9`) || !strings.Contains(response.Body.String(), `"next_cursor":"next-review-page"`) {
		t.Fatalf("GET pull requests status=%d body=%s", response.Code, response.Body.String())
	}
	if backend.pullRequestFilter.View != domain.PullRequestCompleted || backend.pullRequestFilter.Repository != "RainLib/open-review-platform" || backend.pullRequestFilter.Query != "#42" || backend.pullRequestFilter.Cursor != "opaque-cursor" || backend.pullRequestFilter.CursorDirection != domain.WorkQueueCursorAfter || backend.pullRequestFilter.Limit != 25 {
		t.Fatalf("pull request filter=%#v", backend.pullRequestFilter)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/pull-requests?view=unknown", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "pull request view or cursor is invalid") {
		t.Fatalf("invalid pull request filter status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCLIReviewEndpointsUseMachineScopesAndReturnDurableLinks(t *testing.T) {
	runID := uuid.New()
	keyID := uuid.New()
	installationID := uuid.New()
	backend := &recordingStore{
		apiKeyPrincipal: domain.APIKeyPrincipal{
			KeyID: keyID, TenantID: uuid.New(), TenantSlug: "acme", Subject: "api-key:" + keyID.String(),
			Scopes:       []string{domain.APIKeyScopeReviewsCreate, domain.APIKeyScopeReviewsRead, domain.APIKeyScopeRunsCancel},
			Repositories: []string{"RainLib/open-review-platform"},
		},
		cliReviewSubmission: domain.CLIReviewSubmission{
			Run: domain.CLIReviewRun{ReviewRunSummary: domain.ReviewRunSummary{ReviewRun: domain.ReviewRun{ID: runID, Revision: 1, State: domain.RunAcknowledged, TriggerKind: "cli", ReviewMode: domain.ReviewModeSecurity}, Repository: "RainLib/open-review-platform", ReviewNumber: 3}},
		},
		cliReview:         domain.CLIReviewRun{ReviewRunSummary: domain.ReviewRunSummary{ReviewRun: domain.ReviewRun{ID: runID, Revision: 1, State: domain.RunAcknowledged, TriggerKind: "cli"}, Repository: "RainLib/open-review-platform", ReviewNumber: 3}},
		cliReviewEvidence: domain.ReviewEvidence{Run: domain.ReviewRunSummary{ReviewRun: domain.ReviewRun{ID: runID, TriggerKind: "cli"}}},
	}
	backend.cliReviewRuns = []domain.CLIReviewRun{backend.cliReview}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	body := fmt.Sprintf(`{"installation_id":%q,"repository":"RainLib/open-review-platform","review_number":3,"base_ref":"main","base_sha":"0123456789abcdef0123456789abcdef01234567","head_ref":"feature/cli","head_sha":"89abcdef0123456789abcdef0123456789abcdef","mode":"security"}`, installationID)
	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/cli-reviews", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer orp_live_test-machine-key-that-is-long-enough")
	request.Header.Set("Idempotency-Key", "review:cli:pr-3:0001")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Location") != "/v1/tenants/acme/cli-reviews/"+runID.String() || !strings.Contains(recorder.Body.String(), `"evidence_url"`) {
		t.Fatalf("POST CLI review status=%d location=%q body=%s", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
	}
	if backend.cliReviewInput.InstallationID != installationID || backend.cliReviewIdempotencyKey != "review:cli:pr-3:0001" {
		t.Fatalf("CLI input=%#v idempotency=%q", backend.cliReviewInput, backend.cliReviewIdempotencyKey)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/cli-reviews", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), runID.String()) {
		t.Fatalf("GET CLI reviews status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for _, endpoint := range []string{
		"/v1/tenants/acme/cli-reviews/" + runID.String(),
		"/v1/tenants/acme/cli-reviews/" + runID.String() + "/evidence",
	} {
		request = httptest.NewRequest(http.MethodGet, endpoint, nil)
		request.Header.Set("Authorization", "Bearer orp_live_test-machine-key-that-is-long-enough")
		recorder = httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), runID.String()) {
			t.Fatalf("GET %s status=%d body=%s", endpoint, recorder.Code, recorder.Body.String())
		}
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/cli-reviews/"+runID.String()+"/cancel", strings.NewReader(`{"revision":1}`))
	request.Header.Set("Authorization", "Bearer orp_live_test-machine-key-that-is-long-enough")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("cancel CLI review status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	backend.apiKeyPrincipal.Scopes = []string{domain.APIKeyScopeReviewsRead}
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/cli-reviews", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer orp_live_test-machine-key-that-is-long-enough")
	request.Header.Set("Idempotency-Key", "review:cli:pr-3:0002")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("insufficient create scope status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	backend.apiKeyPrincipal.Scopes = []string{domain.APIKeyScopeReviewsCreate}
	backend.cliReviewErr = store.ErrWorkspaceSetupIncomplete
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/cli-reviews", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer orp_live_test-machine-key-that-is-long-enough")
	request.Header.Set("Idempotency-Key", "review:cli:pr-3:setup-incomplete")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "finish setup") {
		t.Fatalf("incomplete setup CLI status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRunRetryEndpointCreatesOrReplaysANewDurableRun(t *testing.T) {
	sourceRunID, retryRunID := uuid.New(), uuid.New()
	backend := &recordingStore{
		runRetry: domain.RunRetryResult{Run: domain.ReviewRun{
			ID: retryRunID, Revision: 1, State: domain.RunAcknowledged,
			TriggerKind: "retry", HeadSHA: "next-head", BaseSHA: "base",
		}},
	}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	retry := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+sourceRunID.String()+"/retry", strings.NewReader(`{"expected_revision":4,"idempotency_key":"console-retry-0001"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, retry)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") != "/v1/tenants/acme/runs/"+retryRunID.String() || !strings.Contains(response.Body.String(), `"evidence_url"`) {
		t.Fatalf("retry status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	if backend.runRetryID != sourceRunID || backend.runRetryInput.ExpectedRevision != 4 || backend.runRetryInput.IdempotencyKey != "console-retry-0001" {
		t.Fatalf("retry request id=%s input=%#v", backend.runRetryID, backend.runRetryInput)
	}

	backend.runRetryErr = store.ErrRevisionConflict
	stale := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+sourceRunID.String()+"/retry", strings.NewReader(`{"expected_revision":4,"idempotency_key":"console-retry-0002"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, stale)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "stale") {
		t.Fatalf("stale retry status=%d body=%s", response.Code, response.Body.String())
	}

	backend.runRetryErr = store.ErrInvalidRunRetry
	invalid := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+sourceRunID.String()+"/retry", strings.NewReader(`{"expected_revision":0,"idempotency_key":"short"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "idempotency") {
		t.Fatalf("invalid retry status=%d body=%s", response.Code, response.Body.String())
	}

	backend.runRetryErr = store.ErrWorkspaceSetupIncomplete
	incomplete := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+sourceRunID.String()+"/retry", strings.NewReader(`{"expected_revision":4,"idempotency_key":"console-retry-0003"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, incomplete)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "finish setup") {
		t.Fatalf("incomplete setup retry status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInteractionResponseRetryEndpointPreservesTheAcknowledgementBarrier(t *testing.T) {
	runID := uuid.New()
	backend := &recordingStore{interactionResponseRetry: domain.InteractionResponseRetryResult{RunID: runID, Attempt: 1}}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)
	path := "/v1/tenants/acme/runs/" + runID.String() + "/acknowledgement/retry"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":2,"idempotency_key":"reply-retry-0001"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"attempt":1`) || backend.interactionResponseRetryRunID != runID || backend.interactionResponseRetryInput.ExpectedRevision != 2 {
		t.Fatalf("acknowledgement retry status=%d body=%s input=%#v", response.Code, response.Body.String(), backend.interactionResponseRetryInput)
	}
	backend.interactionResponseRetryErr = store.ErrInteractionResponseNotExhausted
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":2,"idempotency_key":"reply-retry-0002"}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "still being delivered") {
		t.Fatalf("active reply status=%d body=%s", response.Code, response.Body.String())
	}
	backend.interactionResponseRetryErr = store.ErrForbidden
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":2,"idempotency_key":"reply-retry-0003"}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("forbidden reply status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReviewScheduleEndpointsKeepFutureAdmissionSeparateFromTheRunQueue(t *testing.T) {
	sourceRunID, scheduleID := uuid.New(), uuid.New()
	scheduledFor := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	backend := &recordingStore{
		reviewSchedule: domain.ReviewSchedule{
			ID: scheduleID, SourceRunID: sourceRunID, Revision: 1,
			State: domain.ReviewScheduleScheduled, Provider: domain.ProviderGitHub,
			Repository: "RainLib/open-review-platform", ReviewNumber: 3,
			ReviewMode: domain.ReviewModeSecurity, BaseSHA: "base", HeadSHA: "head",
			ScheduledFor: scheduledFor, RequestedBy: "operator",
		},
	}
	backend.reviewSchedules = []domain.ReviewSchedule{backend.reviewSchedule}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/review-schedules?limit=20", nil))
	if listed.Code != http.StatusOK || backend.reviewScheduleLimit != 20 || !strings.Contains(listed.Body.String(), scheduleID.String()) {
		t.Fatalf("list schedules status=%d limit=%d body=%s", listed.Code, backend.reviewScheduleLimit, listed.Body.String())
	}

	created := httptest.NewRecorder()
	createBody := fmt.Sprintf(`{"scheduled_for":%q}`, scheduledFor.Format(time.RFC3339))
	mux.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+sourceRunID.String()+"/schedules", strings.NewReader(createBody)))
	if created.Code != http.StatusAccepted || created.Header().Get("Location") != "/v1/tenants/acme/review-schedules/"+scheduleID.String() {
		t.Fatalf("create schedule status=%d location=%q body=%s", created.Code, created.Header().Get("Location"), created.Body.String())
	}
	if backend.reviewScheduleRunID != sourceRunID || !backend.reviewScheduleInput.ScheduledFor.Equal(scheduledFor) {
		t.Fatalf("create schedule run=%s input=%#v", backend.reviewScheduleRunID, backend.reviewScheduleInput)
	}

	cancelled := httptest.NewRecorder()
	mux.ServeHTTP(cancelled, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/review-schedules/"+scheduleID.String()+"/cancel", strings.NewReader(`{"expected_revision":1}`)))
	if cancelled.Code != http.StatusAccepted || backend.reviewScheduleID != scheduleID || backend.reviewScheduleExpectedRevision != 1 {
		t.Fatalf("cancel schedule status=%d id=%s revision=%d body=%s", cancelled.Code, backend.reviewScheduleID, backend.reviewScheduleExpectedRevision, cancelled.Body.String())
	}

	backend.reviewScheduleErr = store.ErrRevisionConflict
	stale := httptest.NewRecorder()
	mux.ServeHTTP(stale, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/review-schedules/"+scheduleID.String()+"/cancel", strings.NewReader(`{"expected_revision":1}`)))
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "stale") {
		t.Fatalf("stale schedule cancellation status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func TestReviewInterventionEndpointsRequireFreshHumanDecisions(t *testing.T) {
	runID, interventionID := uuid.New(), uuid.New()
	backend := &recordingStore{
		reviewIntervention: domain.ReviewIntervention{
			ID: interventionID, RunID: runID, Revision: 3,
			State: domain.ReviewInterventionOpen, OpenedAt: time.Now().UTC(),
		},
	}
	backend.reviewInterventions = []domain.ReviewIntervention{backend.reviewIntervention}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/review-interventions?run_id="+runID.String()+"&active_only=true&limit=20", nil))
	if listed.Code != http.StatusOK || backend.reviewInterventionFilter.RunID == nil || *backend.reviewInterventionFilter.RunID != runID || !backend.reviewInterventionFilter.ActiveOnly || backend.reviewInterventionFilter.Limit != 20 || !strings.Contains(listed.Body.String(), interventionID.String()) {
		t.Fatalf("list interventions status=%d filter=%#v body=%s", listed.Code, backend.reviewInterventionFilter, listed.Body.String())
	}

	claimed := httptest.NewRecorder()
	mux.ServeHTTP(claimed, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+runID.String()+"/intervention/claim", strings.NewReader(`{"expected_revision":3}`)))
	if claimed.Code != http.StatusAccepted || backend.reviewInterventionRunID != runID || backend.reviewInterventionExpectedRevision != 3 {
		t.Fatalf("claim intervention status=%d run=%s revision=%d body=%s", claimed.Code, backend.reviewInterventionRunID, backend.reviewInterventionExpectedRevision, claimed.Body.String())
	}

	resolved := httptest.NewRecorder()
	mux.ServeHTTP(resolved, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+runID.String()+"/intervention/resolve", strings.NewReader(`{"expected_revision":3,"reason":"Provider outage is tracked in the incident."}`)))
	if resolved.Code != http.StatusAccepted || backend.reviewInterventionRunID != runID || backend.reviewInterventionResolution.ExpectedRevision != 3 || backend.reviewInterventionResolution.Reason != "Provider outage is tracked in the incident." {
		t.Fatalf("resolve intervention status=%d run=%s input=%#v body=%s", resolved.Code, backend.reviewInterventionRunID, backend.reviewInterventionResolution, resolved.Body.String())
	}

	backend.reviewInterventionErr = store.ErrRevisionConflict
	stale := httptest.NewRecorder()
	mux.ServeHTTP(stale, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/runs/"+runID.String()+"/intervention/claim", strings.NewReader(`{"expected_revision":3}`)))
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "stale") {
		t.Fatalf("stale intervention claim status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func TestSSOEndpointsPreserveRevisionAndServerObservedDNSEvidence(t *testing.T) {
	domainID := uuid.New()
	mappingID := uuid.New()
	probeID := uuid.New()
	revision := 3
	backend := &recordingStore{
		ssoOverview: domain.SSOOverview{
			Configuration: &domain.SSOConfiguration{Protocol: domain.SSOProtocolOIDC, DisplayName: "Acme workforce", State: domain.SSOStateReady, Revision: revision},
			Domains:       []domain.SSODomain{{ID: domainID, Domain: "engineering.example.com", ChallengeToken: "open-review-verification=token", State: "pending", Revision: 1}},
			Mappings:      []domain.SSORoleMapping{},
			Probes:        []domain.SSOProbeReceipt{},
			Readiness:     domain.SSOReadiness{Ready: true},
		},
		ssoProbeReceipt: domain.SSOProbeReceipt{ID: probeID, ConfigRevision: revision, Protocol: domain.SSOProtocolOIDC, State: domain.SSOProbeQueued},
		ssoDomain:       domain.SSODomain{ID: domainID, Domain: "engineering.example.com", ChallengeToken: "open-review-verification=token", State: "verified", Revision: 2},
		ssoRoleMapping:  domain.SSORoleMapping{ID: mappingID, GroupValue: "platform", Role: "reviewer", RepositoryScope: "RainLib/*", Revision: 1},
	}
	server := New(backend, fixedAuthenticator{}, "", "")
	var lookedUpName string
	server.lookupTXT = func(_ context.Context, name string) ([]string, error) {
		lookedUpName = name
		return []string{"unrelated=value", "open-review-verification=token"}, nil
	}
	mux := http.NewServeMux()
	server.Register(mux)

	assertResponse := func(method, path, body string, expectedStatus int, expectedText string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != expectedStatus || (expectedText != "" && !strings.Contains(response.Body.String(), expectedText)) {
			t.Fatalf("%s %s status=%d body=%s", method, path, response.Code, response.Body.String())
		}
	}

	assertResponse(http.MethodGet, "/v1/tenants/acme/sso", "", http.StatusOK, "Acme workforce")
	assertResponse(http.MethodPut, "/v1/tenants/acme/sso/configuration", `{"protocol":"oidc","display_name":"Acme workforce","issuer_url":"https://id.example.com","client_id":"open-review","secret_ref":"secret://sso/acme","group_claim":"groups","expected_revision":3}`, http.StatusOK, "enforcement_ready")
	if backend.ssoConfigurationInput.ExpectedRevision != revision || backend.ssoConfigurationInput.SecretRef != "secret://sso/acme" {
		t.Fatalf("configuration input=%#v", backend.ssoConfigurationInput)
	}

	assertResponse(http.MethodPost, "/v1/tenants/acme/sso/probes", `{"expected_revision":3}`, http.StatusAccepted, probeID.String())
	if backend.ssoProbeExpectedRevision != revision {
		t.Fatalf("probe revision=%d, want %d", backend.ssoProbeExpectedRevision, revision)
	}
	assertResponse(http.MethodPost, "/v1/tenants/acme/sso/domains", `{"domain":"engineering.example.com"}`, http.StatusCreated, domainID.String())
	if backend.ssoDomainInput != "engineering.example.com" {
		t.Fatalf("domain input=%q", backend.ssoDomainInput)
	}

	assertResponse(http.MethodPost, "/v1/tenants/acme/sso/domains/"+domainID.String()+"/verify", `{}`, http.StatusOK, "verified")
	if lookedUpName != "_open-review.engineering.example.com" || backend.ssoVerifiedDomainID != domainID || len(backend.ssoObservedTXT) != 2 || backend.ssoObservedTXT[1] != "open-review-verification=token" {
		t.Fatalf("lookup=%q domain=%s observed=%v", lookedUpName, backend.ssoVerifiedDomainID, backend.ssoObservedTXT)
	}

	assertResponse(http.MethodPost, "/v1/tenants/acme/sso/mappings", `{"group_value":"platform","role":"reviewer","repository_scope":"RainLib/*"}`, http.StatusCreated, mappingID.String())
	if backend.ssoRoleMappingInput.Role != "reviewer" || backend.ssoRoleMappingInput.RepositoryScope != "RainLib/*" {
		t.Fatalf("mapping input=%#v", backend.ssoRoleMappingInput)
	}
	assertResponse(http.MethodDelete, "/v1/tenants/acme/sso/mappings/"+mappingID.String()+"?revision=1", "", http.StatusNoContent, "")
	if backend.ssoDeletedMappingID != mappingID || backend.ssoDeletedMappingRevision != 1 {
		t.Fatalf("deleted mapping=%s revision=%d", backend.ssoDeletedMappingID, backend.ssoDeletedMappingRevision)
	}

	assertResponse(http.MethodPost, "/v1/tenants/acme/sso/enforce", `{"expected_revision":3,"break_glass_subject":"owner-subject"}`, http.StatusOK, "enforcement_ready")
	if backend.ssoEnforcementInput.ExpectedRevision != revision || backend.ssoEnforcementInput.BreakGlassSubject != "owner-subject" {
		t.Fatalf("enforcement input=%#v", backend.ssoEnforcementInput)
	}
	assertResponse(http.MethodPost, "/v1/tenants/acme/sso/suspend", `{"expected_revision":3}`, http.StatusOK, "enforcement_ready")
	if backend.ssoSuspensionRevision != revision {
		t.Fatalf("suspension revision=%d", backend.ssoSuspensionRevision)
	}
}

func TestSSODomainVerificationCannotTrustCallerSuppliedEvidence(t *testing.T) {
	domainID := uuid.New()
	backend := &recordingStore{ssoOverview: domain.SSOOverview{Domains: []domain.SSODomain{{ID: domainID, Domain: "example.com"}}}}
	server := New(backend, fixedAuthenticator{}, "", "")
	var lookedUpName string
	server.lookupTXT = func(_ context.Context, name string) ([]string, error) {
		lookedUpName = name
		return nil, errors.New("not found")
	}
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/sso/domains/"+domainID.String()+"/verify", strings.NewReader(`{"observed_txt":["open-review-verification=forged"]}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "DNS challenge record was not found") {
		t.Fatalf("caller-supplied evidence status=%d body=%s", response.Code, response.Body.String())
	}
	if lookedUpName != "_open-review.example.com" || backend.ssoVerifiedDomainID != uuid.Nil {
		t.Fatalf("lookup=%q; verification store was called with caller evidence: %s", lookedUpName, backend.ssoVerifiedDomainID)
	}
}

func TestSSOMutationErrorsHaveStableHTTPClassification(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		err        error
		statusCode int
		message    string
	}{
		{name: "invalid configuration", method: http.MethodPut, path: "/v1/tenants/acme/sso/configuration", body: `{}`, err: store.ErrInvalidSSO, statusCode: http.StatusBadRequest, message: "configuration is invalid"},
		{name: "stale configuration", method: http.MethodPut, path: "/v1/tenants/acme/sso/configuration", body: `{}`, err: store.ErrRevisionConflict, statusCode: http.StatusConflict, message: "revision changed"},
		{name: "active probe", method: http.MethodPost, path: "/v1/tenants/acme/sso/probes", body: `{"expected_revision":1}`, err: store.ErrConflict, statusCode: http.StatusConflict, message: "already active"},
		{name: "not ready", method: http.MethodPost, path: "/v1/tenants/acme/sso/enforce", body: `{"expected_revision":1,"break_glass_subject":"owner"}`, err: store.ErrSSONotReady, statusCode: http.StatusConflict, message: "not ready"},
		{name: "forbidden is hidden", method: http.MethodGet, path: "/v1/tenants/acme/sso", body: "", err: store.ErrForbidden, statusCode: http.StatusNotFound, message: "tenant not found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := New(&recordingStore{ssoErr: test.err}, fixedAuthenticator{}, "", "")
			mux := http.NewServeMux()
			server.Register(mux)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.statusCode || !strings.Contains(response.Body.String(), test.message) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDataGovernanceEndpointsExposeApprovalAndJobContracts(t *testing.T) {
	policyID := uuid.New()
	holdID := uuid.New()
	jobID := uuid.New()
	backend := &recordingStore{
		dataGovernanceOverview: domain.DataGovernanceOverview{
			Residency:  domain.DataResidency{Configured: false, ModelBoundary: "external-provider"},
			Boundaries: []domain.DataBoundary{{Name: "PostgreSQL", Value: "unknown", Observed: false}},
			Policies:   []domain.RetentionPolicy{},
			LegalHolds: []domain.DataLegalHold{},
			Jobs:       []domain.DataGovernanceJob{},
		},
		retentionPolicy: domain.RetentionPolicy{ID: policyID, State: "awaiting_approval", Revision: 1},
		legalHold:       domain.DataLegalHold{ID: holdID, State: "active", Revision: 1},
		governanceJob:   domain.DataGovernanceJob{ID: jobID, Kind: domain.DataJobDeletion, State: domain.DataJobAwaitingApproval, Revision: 1},
	}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	assertResponse := func(method, path, body string, expectedStatus int, expectedText string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != expectedStatus || !strings.Contains(response.Body.String(), expectedText) {
			t.Fatalf("%s %s status=%d body=%s", method, path, response.Code, response.Body.String())
		}
	}

	assertResponse(http.MethodGet, "/v1/tenants/acme/data-governance", "", http.StatusOK, `"configured":false`)
	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/retention-policies", `{"scope_kind":"tenant","data_class":"findings","retention_days":30,"expected_revision":1,"change_reason":"reduce retention"}`, http.StatusAccepted, `"state":"awaiting_approval"`)
	if backend.retentionPolicyInput.RetentionDays != 30 || backend.retentionPolicyInput.DataClass != domain.DataClassFindings {
		t.Fatalf("retention input=%#v", backend.retentionPolicyInput)
	}

	backend.retentionPolicy.State = "active"
	backend.retentionPolicy.Revision = 2
	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/retention-policies/"+policyID.String()+"/decisions", `{"decision":"approved","reason":"impact accepted","expected_revision":1}`, http.StatusOK, `"state":"active"`)
	if backend.governanceDecisionInput.Decision != "approved" || backend.governanceDecisionInput.ExpectedRevision != 1 {
		t.Fatalf("retention decision=%#v", backend.governanceDecisionInput)
	}

	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/legal-holds", `{"scope_kind":"repository","scope_ref":"RainLib/open-review-platform","data_class":"all","reason":"investigation"}`, http.StatusCreated, holdID.String())
	if backend.legalHoldInput.ScopeRef != "RainLib/open-review-platform" || backend.legalHoldInput.DataClass != domain.DataClassAll {
		t.Fatalf("legal hold input=%#v", backend.legalHoldInput)
	}
	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/legal-holds/"+holdID.String()+"/release", `{"expected_revision":1}`, http.StatusOK, holdID.String())
	if backend.releasedLegalHoldID != holdID || backend.releasedLegalHoldRevision != 1 {
		t.Fatalf("released hold=%s revision=%d", backend.releasedLegalHoldID, backend.releasedLegalHoldRevision)
	}

	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/jobs", `{"kind":"deletion","scope_kind":"repository","scope_ref":"RainLib/open-review-platform","data_classes":["findings"],"idempotency_key":"delete-findings-1","reason":"approved cleanup"}`, http.StatusAccepted, jobID.String())
	if backend.governanceJobInput.Kind != domain.DataJobDeletion || backend.governanceJobInput.IdempotencyKey != "delete-findings-1" {
		t.Fatalf("job input=%#v", backend.governanceJobInput)
	}

	backend.governanceJob.State = domain.DataJobQueued
	backend.governanceJob.Revision = 2
	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/jobs/"+jobID.String()+"/decisions", `{"decision":"approved","reason":"scope verified","expected_revision":1}`, http.StatusOK, `"state":"queued"`)
	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/jobs/"+jobID.String()+"/cancel", `{"expected_revision":2}`, http.StatusOK, jobID.String())
	if backend.governanceJobID != jobID || backend.governanceExpectedRevision != 2 {
		t.Fatalf("cancel job=%s revision=%d", backend.governanceJobID, backend.governanceExpectedRevision)
	}
	assertResponse(http.MethodPost, "/v1/tenants/acme/data-governance/jobs/"+jobID.String()+"/retry", `{"expected_revision":3,"idempotency_key":"retry-delete-1","reason":"transient failure resolved"}`, http.StatusAccepted, jobID.String())
	if backend.governanceRetryIdempotencyKey != "retry-delete-1" || backend.governanceRetryReason != "transient failure resolved" {
		t.Fatalf("retry key=%q reason=%q", backend.governanceRetryIdempotencyKey, backend.governanceRetryReason)
	}
}

func TestDataGovernanceArtifactDownloadDecryptsAndSetsPrivateHeaders(t *testing.T) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	cipher, err := governance.NewArtifactCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	jobID, tenantID := uuid.New(), uuid.New()
	artifact, err := cipher.Encrypt(jobID.String(), tenantID.String(), []byte(`{"schema":"open-review.data-export.v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	artifact.JobID = jobID
	artifact.TenantID = tenantID
	artifact.Filename = "open-review-export.json"
	artifact.ContentType = "application/json"
	artifact.ExpiresAt = time.Now().Add(time.Hour)
	backend := &recordingStore{governanceArtifact: artifact}
	server := New(backend, fixedAuthenticator{}, "", "")
	server.artifactCipher = &cipher
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/data-governance/jobs/"+jobID.String()+"/artifact", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "open-review.data-export.v1") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Header().Get("Content-Disposition"), "open-review-export.json") {
		t.Fatalf("headers=%v", response.Header())
	}
}

func TestDataGovernanceErrorsHaveStableHTTPClassification(t *testing.T) {
	jobID := uuid.New()
	tests := []struct {
		name       string
		err        error
		statusCode int
		message    string
	}{
		{name: "invalid", err: store.ErrInvalidDataGovernance, statusCode: http.StatusBadRequest, message: "request is invalid"},
		{name: "stale", err: store.ErrRevisionConflict, statusCode: http.StatusConflict, message: "revision changed"},
		{name: "legal hold", err: store.ErrLegalHold, statusCode: http.StatusConflict, message: "active legal hold"},
		{name: "requester approval", err: store.ErrSeparationOfDuties, statusCode: http.StatusForbidden, message: "requester cannot approve"},
		{name: "not cancellable", err: store.ErrGovernanceNotCancellable, statusCode: http.StatusConflict, message: "no longer cancellable"},
		{name: "forbidden hidden", err: store.ErrForbidden, statusCode: http.StatusNotFound, message: "resource not found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := New(&recordingStore{dataGovernanceErr: test.err}, fixedAuthenticator{}, "", "")
			mux := http.NewServeMux()
			server.Register(mux)
			request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/data-governance/jobs/"+jobID.String()+"/cancel", strings.NewReader(`{"expected_revision":1}`))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.statusCode || !strings.Contains(response.Body.String(), test.message) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPlatformHealthEndpointReturnsObservedAndConfiguredBoundaries(t *testing.T) {
	now := time.Now().UTC()
	backend := &recordingStore{platformHealth: domain.PlatformHealthOverview{
		SampledAt: now, FreshnessWindowSeconds: 300,
		Components: []domain.HealthComponent{
			{Key: "postgresql", Name: "PostgreSQL", State: domain.HealthLive, Observed: true, SampledAt: &now},
			{Key: "rabbitmq", Name: "RabbitMQ", State: domain.HealthConfiguredOnly, Observed: false},
		},
		Queues:                    []domain.QueueHealth{{Key: "review-execution", State: domain.HealthLive, SampledAt: now}},
		Workers:                   []domain.WorkerHealth{},
		Providers:                 []domain.ProviderHealth{},
		Incidents:                 []domain.PlatformIncident{},
		IncidentTrackingAvailable: false,
		Runbooks:                  []domain.PlatformRunbook{{Key: "review-backlog", Title: "Review backlog recovery", Executable: false}},
	}}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/platform-health", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"live"`) || !strings.Contains(response.Body.String(), `"state":"configured_only"`) || !strings.Contains(response.Body.String(), `"incident_tracking_available":false`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", response.Header().Get("Cache-Control"))
	}

	backend.platformHealthErr = store.ErrForbidden
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "tenant not found") {
		t.Fatalf("forbidden status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReviewEvidenceEndpointReturnsTabbedReadModel(t *testing.T) {
	runID := uuid.New()
	findingID := uuid.New()
	backend := &recordingStore{reviewEvidence: domain.ReviewEvidence{
		Run:         domain.ReviewRunSummary{ReviewRun: domain.ReviewRun{ID: runID, State: domain.RunCompleted}, Repository: "RainLib/open-review-platform", ReviewNumber: 3},
		RelatedRuns: []domain.ReviewRunSummary{},
		Findings:    []domain.ReviewFindingEvidence{{ID: findingID, Path: "internal/api/server.go", Severity: "high"}},
		Stages:      []domain.ReviewRunStageEvidence{{ID: uuid.New(), Stage: "analyze", State: "succeeded", Details: map[string]any{}}},
		Receipts:    []domain.PublicationReceiptEvidence{},
		Events:      []domain.RunEvent{},
	}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/runs/"+runID.String()+"/evidence", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), findingID.String()) || !strings.Contains(recorder.Body.String(), `"stage":"analyze"`) {
		t.Fatalf("GET review evidence status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if backend.requestedEvidenceRunID != runID {
		t.Fatalf("requested run=%s, want %s", backend.requestedEvidenceRunID, runID)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/runs/not-a-uuid/evidence", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "run id is invalid") {
		t.Fatalf("invalid run status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentTaskEndpointsRequireAnExplicitRepositoryPolicyAndPlanApproval(t *testing.T) {
	taskID, planID := uuid.New(), uuid.New()
	backend := &recordingStore{
		agentTaskPolicy: domain.AgentTaskPolicy{ID: uuid.New(), Mode: "manual", Revision: 2},
		agentTask:       domain.AgentTask{ID: taskID, State: "received", Revision: 1},
		agentTaskPlan:   domain.AgentTaskPlan{ID: planID, TaskID: taskID, State: "awaiting_approval", Revision: 1},
	}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/agent-task-policies", strings.NewReader(`{"provider":"github","api_base_url":"https://api.github.com","repository":"RainLib/open-review-platform","mode":"manual","revision":1}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.agentTaskPolicyInput.Mode != "manual" || backend.agentTaskPolicyInput.Revision != 1 {
		t.Fatalf("PUT agent task policy status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.agentTaskPolicyInput)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks", strings.NewReader(`{"provider":"github","api_base_url":"https://api.github.com","repository":"RainLib/open-review-platform","origin_kind":"issue","origin_number":42,"origin_revision":"issue-v3","intent":"implement"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || backend.agentTaskInput.OriginKind != "issue" || backend.agentTaskInput.OriginNumber != 42 || backend.agentTaskInput.Intent != "implement" {
		t.Fatalf("POST agent task status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.agentTaskInput)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks/"+taskID.String()+"/retry-source", strings.NewReader(`{"revision":2}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || backend.agentTaskSourceRetryRevision != 2 || !strings.Contains(recorder.Body.String(), `"source_state":"pending"`) {
		t.Fatalf("POST source retry status=%d body=%s revision=%d", recorder.Code, recorder.Body.String(), backend.agentTaskSourceRetryRevision)
	}
	backend.agentTaskSourceRetryErr = store.ErrConflict
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks/"+taskID.String()+"/retry-source", strings.NewReader(`{"revision":2}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("non-retryable source status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	backend.agentTaskSourceRetryErr = nil

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks/"+taskID.String()+"/plans", strings.NewReader(`{"summary":"Validate the report, make the smallest bounded change, add regression coverage, then run the focused verification command."}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(backend.agentTaskPlanInput.Summary, "smallest bounded change") {
		t.Fatalf("POST agent plan status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.agentTaskPlanInput)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks/"+taskID.String()+"/plans/"+planID.String()+"/approve", strings.NewReader(`{"revision":1}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.agentTaskPlanApproval.Revision != 1 || !strings.Contains(recorder.Body.String(), `"state":"approved"`) {
		t.Fatalf("POST agent plan approval status=%d body=%s approval=%#v", recorder.Code, recorder.Body.String(), backend.agentTaskPlanApproval)
	}

	backend.agentTaskErr = store.ErrAgentTaskDisabled
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks", strings.NewReader(`{"provider":"github","api_base_url":"https://api.github.com","repository":"RainLib/open-review-platform","origin_kind":"issue","origin_number":43,"origin_revision":"issue-v4","intent":"implement"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "disabled") {
		t.Fatalf("disabled task status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks/"+taskID.String()+"/cancel", strings.NewReader(`{"revision":3,"reason":"The linked Issue requirements changed."}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.agentTaskCancellation.Revision != 3 || !strings.Contains(recorder.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("POST agent task cancel status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.agentTaskCancellation)
	}
}

func TestAgentTaskListRequiresAValidTenantScopedCursor(t *testing.T) {
	cursor := uuid.New()
	backend := &recordingStore{agentTaskListPage: domain.AgentTaskPage{
		Tasks: []domain.AgentTask{{ID: uuid.New(), State: "received"}}, NextCursor: cursor.String(),
	}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	for _, raw := range []string{"bad", uuid.Nil.String(), "", cursor.String() + "&cursor=" + cursor.String()} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/agent-tasks?cursor="+raw, nil))
		if response.Code != http.StatusBadRequest || backend.agentTaskListBefore != uuid.Nil {
			t.Fatalf("invalid cursor=%q status=%d body=%s", raw, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/agent-tasks?cursor="+cursor.String(), nil))
	if response.Code != http.StatusOK || backend.agentTaskListBefore != cursor || !strings.Contains(response.Body.String(), `"next_cursor":"`+cursor.String()+`"`) {
		t.Fatalf("valid cursor status=%d body=%s before=%s", response.Code, response.Body.String(), backend.agentTaskListBefore)
	}
	backend.agentTaskListErr = store.ErrNotFound
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/agent-tasks?cursor="+cursor.String(), nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign cursor status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentTaskPolicyExactLookupKeepsRevisionOutsideOverviewPage(t *testing.T) {
	policy := domain.AgentTaskPolicy{ID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "RainLib/open-review-platform", Mode: "manual", Revision: 7}
	backend := &recordingStore{agentTaskPolicy: policy}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)
	path := "/v1/tenants/acme/agent-task-policies?provider=github&api_base_url=https%3A%2F%2Fapi.github.com&repository=RainLib%2Fopen-review-platform"

	request := httptest.NewRequest(http.MethodGet, path, nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"revision":7`) || backend.agentTaskPolicyInput.Repository != policy.Repository {
		t.Fatalf("exact policy lookup status=%d body=%s identity=%#v", recorder.Code, recorder.Body.String(), backend.agentTaskPolicyInput)
	}

	backend.agentTaskPolicy = domain.AgentTaskPolicy{}
	request = httptest.NewRequest(http.MethodGet, path, nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"agent_task_policies":[]`) {
		t.Fatalf("missing exact policy status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for _, path := range []string{
		"/v1/tenants/acme/agent-task-policies?provider=github&repository=RainLib%2Fopen-review-platform",
		"/v1/tenants/acme/agent-task-policies?provider=unknown&api_base_url=https%3A%2F%2Fapi.github.com&repository=RainLib%2Fopen-review-platform",
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		recorder = httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid exact lookup %q status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	backend.agentTaskPolicyErr = store.ErrForbidden
	request = httptest.NewRequest(http.MethodGet, path, nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant lookup status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestReviewConfigurationEndpointsPreserveScopeRevisionAndContent(t *testing.T) {
	backend := &recordingStore{reviewConfig: domain.ReviewConfigView{
		Section:             domain.ReviewConfigGeneral,
		RequestedScopeKind:  domain.ReviewConfigRepositoryScope,
		RequestedScopeRef:   "RainLib/open-review-platform",
		RequestedProvider:   domain.ProviderGitHub,
		RequestedAPIBaseURL: "https://api.github.com",
		OriginScopeKind:     "repository",
		Revision:            2,
		Content:             json.RawMessage(`{"automatic_review":true}`),
	}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/review-config/general?scope_kind=repository&scope_ref=RainLib%2Fopen-review-platform&scope_provider=github&scope_api_base_url=https%3A%2F%2Fapi.github.com", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"automatic_review":true`) {
		t.Fatalf("GET review config status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if backend.reviewConfigInput.ScopeKind != domain.ReviewConfigRepositoryScope || backend.reviewConfigInput.ScopeRef != "RainLib/open-review-platform" || backend.reviewConfigInput.ScopeProvider != domain.ProviderGitHub || backend.reviewConfigInput.ScopeAPIBaseURL != "https://api.github.com" {
		t.Fatalf("unexpected GET review config input: %#v", backend.reviewConfigInput)
	}

	backend.reviewConfigHistory = domain.ReviewConfigHistory{OriginScopeKind: "repository", Versions: []domain.ReviewConfigVersion{{Revision: 2, ContentSHA256: "hash", CreatedBy: "owner"}}}
	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/review-config/general/history?scope_kind=repository&scope_ref=RainLib%2Fopen-review-platform&scope_provider=github&scope_api_base_url=https%3A%2F%2Fapi.github.com", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"revision":2`) || backend.reviewConfigInput.ScopeRef != "RainLib/open-review-platform" || backend.reviewConfigInput.ScopeProvider != domain.ProviderGitHub {
		t.Fatalf("GET review config history status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.reviewConfigInput)
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/review-config/general", strings.NewReader(`{"scope_kind":"repository","scope_ref":"RainLib/open-review-platform","scope_provider":"github","scope_api_base_url":"https://api.github.com","expected_revision":2,"content":{"automatic_review":false}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.reviewConfigInput.ExpectedRevision != 2 || backend.reviewConfigInput.ScopeProvider != domain.ProviderGitHub || backend.reviewConfigInput.ScopeAPIBaseURL != "https://api.github.com" || !strings.Contains(string(backend.reviewConfigInput.Content), `"automatic_review":false`) {
		t.Fatalf("PUT review config status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.reviewConfigInput)
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/tenants/acme/review-config/general?scope_kind=repository&scope_ref=RainLib%2Fopen-review-platform&scope_provider=github&scope_api_base_url=https%3A%2F%2Fapi.github.com&expected_revision=2", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.reviewConfigInput.ExpectedRevision != 2 || backend.reviewConfigInput.ScopeProvider != domain.ProviderGitHub || backend.reviewConfigInput.ScopeAPIBaseURL != "https://api.github.com" {
		t.Fatalf("DELETE review config status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.reviewConfigInput)
	}
}

func TestIssueFormatTemplateEndpointsPreserveVersionedCatalogContract(t *testing.T) {
	templateID := uuid.New()
	content := json.RawMessage(`{"preset":"security","language":"inherit","required_issue_sections":["outcome","evidence","security_impact","risk"],"response_sections":["assessment","risk","next_steps","provenance"],"collapse_secondary":true,"link_file_references":true,"reaction_feedback":true,"max_items_per_section":8,"custom_guidance":"Require CWE evidence."}`)
	backend := &recordingStore{
		issueFormatTemplates: []domain.IssueFormatTemplate{{ID: templateID, Name: "Security boundary", Revision: 3, Content: content, ContentSHA256: strings.Repeat("a", 64)}},
		issueFormatTemplate:  domain.IssueFormatTemplate{ID: templateID, Name: "Security boundary", Revision: 3, Content: content, ContentSHA256: strings.Repeat("a", 64)},
	}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/issue-format-templates", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), templateID.String()) || !strings.Contains(recorder.Body.String(), `"templates"`) {
		t.Fatalf("GET issue formats status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/issue-format-templates", strings.NewReader(`{"name":"Security boundary","description":"Reusable security policy","content":{"preset":"security"}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || backend.issueFormatTemplateInput.Name != "Security boundary" || !strings.Contains(string(backend.issueFormatTemplateInput.Content), `"preset":"security"`) {
		t.Fatalf("POST issue format status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.issueFormatTemplateInput)
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/issue-format-templates/"+templateID.String(), strings.NewReader(`{"name":"Security boundary v2","description":"Updated","expected_revision":3,"content":{"preset":"custom"}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.issueFormatTemplateID != templateID || backend.issueFormatTemplateInput.ExpectedRevision != 3 {
		t.Fatalf("PUT issue format status=%d body=%s id=%s input=%#v", recorder.Code, recorder.Body.String(), backend.issueFormatTemplateID, backend.issueFormatTemplateInput)
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/tenants/acme/issue-format-templates/"+templateID.String()+"?expected_revision=3", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || backend.issueFormatTemplateID != templateID || backend.issueFormatTemplateExpectedRevision != 3 {
		t.Fatalf("DELETE issue format status=%d body=%s id=%s revision=%d", recorder.Code, recorder.Body.String(), backend.issueFormatTemplateID, backend.issueFormatTemplateExpectedRevision)
	}
}

func TestReviewConfigChangeApprovalEndpointsPreserveHashBoundProposal(t *testing.T) {
	requestID := uuid.New()
	backend := &recordingStore{reviewConfigChange: domain.ReviewConfigChangeRequest{
		ID: requestID, Section: domain.ReviewConfigModels, ScopeKind: domain.ReviewConfigTenantScope,
		BaseRevision: 2, BaseContentSHA256: "base-hash", ProposedContentSHA256: "proposed-hash",
		State: domain.ReviewConfigChangePending,
	}}
	backend.reviewConfigChanges = []domain.ReviewConfigChangeRequest{backend.reviewConfigChange}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/review-config-change-requests", strings.NewReader(`{"section":"models","scope_kind":"tenant","expected_revision":2,"content":{"provider":"openai","model":"gpt-5"},"reason":"rotate the approved production route"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || backend.reviewConfigChangeInput.ExpectedRevision != 2 || backend.reviewConfigChangeInput.Reason != "rotate the approved production route" || !strings.Contains(string(backend.reviewConfigChangeInput.Content), `"provider":"openai"`) {
		t.Fatalf("POST review config change status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.reviewConfigChangeInput)
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/review-config-change-requests", strings.NewReader(`{"section":"models","scope_kind":"repository","scope_ref":"team/service","scope_provider":"gitlab","scope_api_base_url":"https://gitlab.example/api/v4","expected_revision":2,"content":{"model":"model-b"},"reason":"isolate self-managed route"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || backend.reviewConfigChangeInput.ScopeProvider != domain.ProviderGitLab || backend.reviewConfigChangeInput.ScopeAPIBaseURL != "https://gitlab.example/api/v4" || backend.reviewConfigChangeInput.ScopeRef != "team/service" {
		t.Fatalf("qualified proposal lost identity: status=%d input=%#v", recorder.Code, backend.reviewConfigChangeInput)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/review-config-change-requests?limit=20", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), requestID.String()) {
		t.Fatalf("GET review config changes status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/review-config-change-requests/"+requestID.String()+"/decision", strings.NewReader(`{"decision":"approved","comment":"reviewed independently"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.reviewConfigChangeID != requestID || backend.reviewConfigChangeDecisionInput.Decision != "approved" {
		t.Fatalf("POST review config decision status=%d body=%s id=%s input=%#v", recorder.Code, recorder.Body.String(), backend.reviewConfigChangeID, backend.reviewConfigChangeDecisionInput)
	}
}

func TestActiveModelRouteMutationsExplainIndependentApprovalRequirement(t *testing.T) {
	backend := &recordingStore{reviewConfigErr: store.ErrReviewConfigApprovalRequired}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/review-config/models", strings.NewReader(`{"scope_kind":"tenant","expected_revision":3,"content":{"enabled":true}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "independent approval") {
		t.Fatalf("active model route update status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/tenants/acme/review-config/models?scope_kind=repository&scope_ref=RainLib%2Fopen-review-platform&expected_revision=3", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "independent approval") {
		t.Fatalf("active model route inheritance restore status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkspaceSetupCheckpointEndpointsPreserveRevision(t *testing.T) {
	backend := &recordingStore{setupCheckpoint: domain.WorkspaceSetupCheckpoint{CurrentStep: domain.WorkspaceSetupLearning, Revision: 3}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/setup-checkpoint", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"current_step":"learning"`) {
		t.Fatalf("GET setup checkpoint status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/setup-checkpoint", strings.NewReader(`{"current_step":"severity","expected_revision":3,"learning_boundary":{"mode":"governed_policy","reviewer_exclusions":["reviewer-a"]}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.setupCheckpointInput.CurrentStep != domain.WorkspaceSetupSeverity || backend.setupCheckpointInput.ExpectedRevision != 3 || backend.setupCheckpointInput.LearningBoundary == nil || backend.setupCheckpointInput.LearningBoundary.Mode != domain.WorkspaceLearningGovernedPolicy {
		t.Fatalf("PUT setup checkpoint status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.setupCheckpointInput)
	}
}

func TestWorkspaceSetupCheckpointRequiresSynchronizedRepository(t *testing.T) {
	backend := &recordingStore{setupCheckpointErr: store.ErrSetupRepositoryScopeIncomplete}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/setup-checkpoint", strings.NewReader(`{"current_step":"learning","expected_revision":1}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "synchronized repository") {
		t.Fatalf("repository scope gate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkspaceSetupCheckpointRequiresRecordedMergePolicy(t *testing.T) {
	backend := &recordingStore{setupCheckpointErr: store.ErrSetupReviewPolicyIncomplete}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/setup-checkpoint", strings.NewReader(`{"current_step":"rules","expected_revision":3}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "merge-blocking threshold") {
		t.Fatalf("merge policy gate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestModelProbeEndpointsUseRouteScopeAndRevision(t *testing.T) {
	backend := &recordingStore{modelProbes: []domain.ModelProbeReceipt{{ID: uuid.New(), State: domain.ModelProbeSucceeded, ConfigRevision: 3}}, modelProbe: domain.ModelProbeReceipt{ID: uuid.New(), State: domain.ModelProbeQueued, ConfigRevision: 3}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/model-probes?scope_kind=repository&scope_ref=RainLib%2Fopen-review-platform&scope_provider=gitlab&scope_api_base_url=https%3A%2F%2Fgitlab.example%2Fapi%2Fv4", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.modelProbeInput.ScopeKind != domain.ReviewConfigRepositoryScope || backend.modelProbeInput.ScopeRef != "RainLib/open-review-platform" || backend.modelProbeInput.ScopeProvider != domain.ProviderGitLab || backend.modelProbeInput.ScopeAPIBaseURL != "https://gitlab.example/api/v4" || !strings.Contains(recorder.Body.String(), `"probes"`) {
		t.Fatalf("GET model probes status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.modelProbeInput)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/model-probes", strings.NewReader(`{"scope_kind":"repository","scope_ref":"RainLib/open-review-platform","scope_provider":"gitlab","scope_api_base_url":"https://gitlab.example/api/v4","expected_revision":3,"acknowledged":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || backend.modelProbeInput.ExpectedRevision != 3 || backend.modelProbeInput.ScopeKind != domain.ReviewConfigRepositoryScope || backend.modelProbeInput.ScopeProvider != domain.ProviderGitLab || backend.modelProbeInput.ScopeAPIBaseURL != "https://gitlab.example/api/v4" || !backend.modelProbeInput.Acknowledged {
		t.Fatalf("POST model probes status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), backend.modelProbeInput)
	}
}

func TestUsageEndpointsExposeDashboardAndValidateEntitlement(t *testing.T) {
	backend := &recordingStore{
		usageDashboard:      domain.UsageDashboard{Settled: 7, Reserved: 2},
		usageEntitlement:    domain.UsageEntitlement{MonthlyReviewLimit: 100, SoftWarningPercent: 75},
		usageReconciliation: domain.UsageReconciliationReport{RepairedRuns: 2, Adjustments: 2},
	}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/usage?limit=25", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"settled":7`) {
		t.Fatalf("GET usage status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/usage/entitlement", strings.NewReader(`{"monthly_review_limit":100,"soft_warning_percent":75}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT usage entitlement status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if backend.usageEntitlementInput.MonthlyReviewLimit != 100 || backend.usageEntitlementInput.SoftWarningPercent != 75 {
		t.Fatalf("unexpected usage entitlement input: %#v", backend.usageEntitlementInput)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/usage/reconcile", strings.NewReader(`{"period_start":"2026-09-01","reason":"monthly integrity check","idempotency_key":"reconcile-september-1"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"repaired_runs":2`) {
		t.Fatalf("POST usage reconcile status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if backend.usageReconciliationInput.PeriodStart != "2026-09-01" || backend.usageReconciliationInput.Reason != "monthly integrity check" || backend.usageReconciliationInput.IdempotencyKey != "reconcile-september-1" {
		t.Fatalf("unexpected usage reconciliation input: %#v", backend.usageReconciliationInput)
	}
}

func TestUsageExportReturnsTenantSafeCSV(t *testing.T) {
	backend := &recordingStore{usageExport: domain.UsageExport{
		PeriodStart:        time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC),
		GeneratedAt:        time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC),
		MonthlyReviewLimit: 100,
		SoftWarningPercent: 80,
		Settled:            3,
		Reserved:           1,
		Released:           2,
		Repositories: []domain.UsageExportRow{
			{Repository: "=unsafe/repository", Settled: 3, Reserved: 1, Released: 2},
		},
	}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	server.now = func() time.Time { return time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/usage/export?period_start=2026-09-01", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET usage export status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := backend.usageExportPeriod; !got.Equal(time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("export period=%s", got)
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(recorder.Header().Get("Content-Type"), "text/csv") || !strings.Contains(recorder.Header().Get("Content-Disposition"), "open-review-usage-2026-09.csv") {
		t.Fatalf("headers=%v", recorder.Header())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "summary,2026-09-01,2026-10-01,,review_run,3,1,2,4,100,80") || !strings.Contains(body, "'=unsafe/repository") {
		t.Fatalf("unexpected export body=%s", body)
	}
	if strings.Contains(body, "tenant_id") || strings.Contains(body, "run_id") || strings.Contains(body, "metadata") {
		t.Fatalf("export exposed internal fields: %s", body)
	}

	for _, path := range []string{
		"/v1/tenants/acme/usage/export",
		"/v1/tenants/acme/usage/export?period_start=2026-09-02",
		"/v1/tenants/acme/usage/export?period_start=2026-10-01",
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		recorder = httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestUsageReconciliationErrorsHaveStableHTTPClassification(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode int
		message    string
	}{
		{name: "invalid", err: store.ErrInvalidUsageReconciliation, statusCode: http.StatusBadRequest, message: "request is invalid"},
		{name: "forbidden", err: store.ErrForbidden, statusCode: http.StatusForbidden, message: "administrator role is required"},
		{name: "idempotency conflict", err: store.ErrConflict, statusCode: http.StatusConflict, message: "idempotency key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &recordingStore{usageReconciliationErr: test.err}
			server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
			mux := http.NewServeMux()
			server.Register(mux)
			request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/usage/reconcile", strings.NewReader(`{"period_start":"2026-09-01","reason":"monthly integrity check","idempotency_key":"reconcile-september-1"}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != test.statusCode || !strings.Contains(recorder.Body.String(), test.message) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAPIKeyEndpointsReturnSecretOnlyAtCreation(t *testing.T) {
	keyID := uuid.New()
	item := domain.APIKey{
		ID: keyID, Name: "CLI automation", Prefix: "orp_live_exampleprefix",
		CallerSubject: "api-key:" + keyID.String(), Scopes: []string{domain.APIKeyScopeReviewsCreate},
		Repositories: []string{"RainLib/open-review-platform"}, CreatedBy: "operator", CreatedAt: time.Now(),
	}
	backend := &recordingStore{
		apiKeys:        []domain.APIKey{item},
		apiKeyCreation: domain.APIKeyCreation{APIKey: item, Secret: "orp_live_once_only_secret"},
	}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/api-keys", strings.NewReader(`{"name":"CLI automation","scopes":["reviews:create"],"repositories":["RainLib/open-review-platform"]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"secret":"orp_live_once_only_secret"`) || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("POST API key status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if backend.apiKeyInput.Name != "CLI automation" || len(backend.apiKeyInput.Repositories) != 1 {
		t.Fatalf("unexpected API key input: %#v", backend.apiKeyInput)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/api-keys", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "once_only_secret") || !strings.Contains(recorder.Body.String(), item.Prefix) {
		t.Fatalf("GET API keys status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/api-keys/"+keyID.String()+"/revoke", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.revokedAPIKeyID != keyID {
		t.Fatalf("revoke API key status=%d id=%s body=%s", recorder.Code, backend.revokedAPIKeyID, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/api-keys/not-a-uuid/revoke", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid API key id status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func (s *recordingStore) CreateInstallation(_ context.Context, _ string, _ string, input domain.InstallationInput) (domain.Installation, error) {
	s.installationInput = input
	return s.installation, s.createInstallationErr
}

func (s *recordingStore) ListInstallations(context.Context, string, string, int) ([]domain.InstallationSummary, error) {
	return nil, s.listInstallationsErr
}

func (s *recordingStore) GetInstallation(_ context.Context, _, _ string, installationID uuid.UUID) (domain.InstallationSummary, error) {
	s.getInstallationID = installationID
	return s.installationSummary, s.getInstallationErr
}

func (s *recordingStore) ListInstallationRepositories(_ context.Context, _, _ string, installationID uuid.UUID, query string, _ int) ([]domain.ProviderRepository, error) {
	s.installationRepositoryID = installationID
	s.installationRepositoryQuery = query
	return s.installationRepositories, s.installationRepositoryErr
}

func (s *recordingStore) UpdateInstallationRepositoryScope(_ context.Context, _, _ string, installationID uuid.UUID, input domain.InstallationRepositoryScopeInput) (domain.InstallationSummary, error) {
	s.installationScopeID = installationID
	s.installationScopeInput = input
	if s.installationScopeErr != nil {
		return domain.InstallationSummary{}, s.installationScopeErr
	}
	return domain.InstallationSummary{ID: installationID, RepositoryScope: input.RepositoryScope, Active: true}, nil
}

func (s *recordingStore) DeactivateInstallation(_ context.Context, _, _ string, installationID uuid.UUID) (domain.InstallationSummary, error) {
	s.deactivatedInstallationID = installationID
	if s.deactivateInstallationErr != nil {
		return domain.InstallationSummary{}, s.deactivateInstallationErr
	}
	if s.deactivatedInstallation.ID != uuid.Nil {
		return s.deactivatedInstallation, nil
	}
	return domain.InstallationSummary{ID: installationID, Active: false}, nil
}

func (s *recordingStore) ListInstallationWebhookReceipts(_ context.Context, _ string, _ string, installationID uuid.UUID, _ int) ([]domain.InstallationWebhookReceipt, error) {
	s.installationWebhookReceiptID = installationID
	return s.installationWebhookReceipts, s.installationWebhookReceiptErr
}

func (s *recordingStore) RequestInstallationVerification(_ context.Context, _ string, _ string, installationID uuid.UUID) (domain.InstallationSummary, error) {
	s.installationVerificationID = installationID
	return domain.InstallationSummary{ID: installationID, VerificationState: domain.InstallationVerificationPending}, s.installationVerificationErr
}

func (s *recordingStore) CreateNotificationDestination(_ context.Context, _, _ string, input domain.NotificationDestinationInput) (domain.NotificationDestination, error) {
	s.notificationDestinationInput = input
	return s.notificationDestination, nil
}

func (*recordingStore) ListNotificationDestinations(context.Context, string, string) ([]domain.NotificationDestination, error) {
	return nil, nil
}

func (s *recordingStore) UpdateNotificationDestination(context.Context, string, string, uuid.UUID, domain.NotificationDestinationUpdateInput) (domain.NotificationDestination, error) {
	return domain.NotificationDestination{}, s.updateNotificationDestinationErr
}

func (s *recordingStore) RequestNotificationTest(_ context.Context, _, _ string, destinationID uuid.UUID, input domain.NotificationTestInput) (domain.NotificationDeliverySummary, error) {
	s.notificationTestDestinationID = destinationID
	s.notificationTestInput = input
	return s.notificationTestDelivery, s.requestNotificationTestErr
}

func (s *recordingStore) CreateNotificationRoute(_ context.Context, _, _ string, input domain.NotificationRouteInput) (domain.NotificationRoute, error) {
	s.notificationRouteInput = input
	return s.notificationRoute, nil
}

func (s *recordingStore) ListNotificationRoutes(context.Context, string, string) ([]domain.NotificationRoute, error) {
	return s.notificationRoutes, nil
}

func (s *recordingStore) ReorderNotificationRoutes(_ context.Context, _, _ string, input domain.NotificationRouteReorderInput) ([]domain.NotificationRoute, error) {
	s.notificationRouteOrderInput = input
	return s.notificationRoutes, s.reorderNotificationRoutesErr
}

func (s *recordingStore) PreviewNotificationRoutes(_ context.Context, _, _ string, input domain.NotificationRoutePreviewInput) (domain.NotificationRoutePreview, error) {
	s.notificationRoutePreviewInput = input
	return s.notificationRoutePreview, s.previewNotificationRoutesErr
}

func (s *recordingStore) UpdateNotificationRoute(context.Context, string, string, uuid.UUID, domain.NotificationRouteUpdateInput) (domain.NotificationRoute, error) {
	return domain.NotificationRoute{}, s.updateNotificationRouteErr
}

func (s *recordingStore) ListNotificationDeliveries(context.Context, string, string, int) ([]domain.NotificationDeliverySummary, error) {
	return s.notificationDeliveries, nil
}

func (s *recordingStore) RetryNotificationDelivery(context.Context, string, string, uuid.UUID) error {
	return s.retryNotificationDeliveryErr
}

func (s *recordingStore) CreateRuleSet(context.Context, string, string, domain.RuleSetInput) (domain.RuleSetWithDraft, error) {
	return domain.RuleSetWithDraft{}, s.createRuleSetErr
}

func (*recordingStore) ListRuleSets(context.Context, string, string, int) ([]domain.RuleSet, error) {
	return nil, nil
}

func (s *recordingStore) ListRuleCatalog(context.Context, string, string) ([]domain.RuleCatalogEntry, error) {
	return s.ruleCatalogEntries, s.ruleCatalogErr
}

func (s *recordingStore) InstallRuleCatalogEntry(_ context.Context, _, _ string, catalogID string, input domain.RuleCatalogInstallInput) (domain.RuleCatalogInstallResult, error) {
	s.ruleCatalogInstallID = catalogID
	s.ruleCatalogInstallInput = input
	return s.ruleCatalogInstall, s.ruleCatalogErr
}

func (s *recordingStore) ListRuleApprovalRequests(context.Context, string, string, int) ([]domain.RuleApprovalSummary, error) {
	return s.ruleApprovalRequests, s.listRuleApprovalRequestsErr
}

func (s *recordingStore) PreviewRuleVersionImpact(_ context.Context, _ string, _ string, _ uuid.UUID, _ int, input domain.RuleImpactPreviewInput) (domain.RuleImpactPreview, error) {
	s.ruleImpactPreviewInput = input
	return s.ruleImpactPreview, s.previewRuleImpactErr
}
func (*recordingStore) CreateRuleTestRun(context.Context, string, string, uuid.UUID, int, domain.RuleTestRunInput) (domain.RuleTestRun, error) {
	return domain.RuleTestRun{ID: uuid.New(), State: "queued", Findings: []domain.Finding{}}, nil
}
func (*recordingStore) ListRuleTestRuns(context.Context, string, string, int) ([]domain.RuleTestRun, error) {
	return []domain.RuleTestRun{}, nil
}
func (s *recordingStore) CreateRuleException(_ context.Context, _, _ string, input domain.RuleExceptionInput) (domain.RuleException, error) {
	s.ruleExceptionInput = input
	return domain.RuleException{ID: uuid.New(), State: "pending", EffectiveState: "pending"}, nil
}
func (*recordingStore) ListRuleExceptions(context.Context, string, string, int) ([]domain.RuleException, error) {
	return []domain.RuleException{}, nil
}
func (*recordingStore) DecideRuleException(context.Context, string, string, uuid.UUID, domain.RuleExceptionDecisionInput) (domain.RuleException, error) {
	return domain.RuleException{ID: uuid.New(), State: "approved", EffectiveState: "approved"}, nil
}
func (*recordingStore) RevokeRuleException(context.Context, string, string, uuid.UUID) (domain.RuleException, error) {
	return domain.RuleException{ID: uuid.New(), State: "revoked", EffectiveState: "revoked"}, nil
}

func (s *recordingStore) RequestRuleApproval(context.Context, string, string, uuid.UUID, int, domain.RuleApprovalRequestInput) (domain.RuleApprovalRequest, error) {
	return domain.RuleApprovalRequest{}, s.requestRuleApprovalErr
}

func (s *recordingStore) DecideRuleApproval(context.Context, string, string, uuid.UUID, domain.RuleApprovalDecisionInput) (domain.RuleApprovalRequest, error) {
	return domain.RuleApprovalRequest{}, s.decideRuleApprovalErr
}

func (*recordingStore) PublishRuleVersion(context.Context, string, string, uuid.UUID, int) (domain.RuleVersion, error) {
	return domain.RuleVersion{}, nil
}

func (s *recordingStore) CreateRuleBinding(_ context.Context, _ string, _ string, input domain.RuleBindingInput) (domain.RuleBinding, error) {
	s.ruleBindingInput = input
	return domain.RuleBinding{}, s.createBindingErr
}

func (s *recordingStore) UpdateRuleBinding(context.Context, string, string, uuid.UUID, domain.RuleBindingUpdateInput) (domain.RuleBinding, error) {
	return domain.RuleBinding{}, s.updateBindingErr
}

func (*recordingStore) ListRuleBindings(context.Context, string, string, int) ([]domain.RuleBinding, error) {
	return nil, nil
}

func (s *recordingStore) CreateRuleRollout(_ context.Context, _ string, _ string, input domain.RuleRolloutInput) (domain.RuleRollout, error) {
	s.ruleRolloutInput = input
	if s.createRuleRolloutErr != nil {
		return domain.RuleRollout{}, s.createRuleRolloutErr
	}
	return domain.RuleRollout{ID: uuid.New(), Mode: input.Mode, State: "active", CanaryBasisPoints: input.CanaryBasisPoints, Revision: 1}, nil
}

func (s *recordingStore) ListRuleRollouts(context.Context, string, string, int) ([]domain.RuleRollout, error) {
	return s.ruleRollouts, nil
}

func (s *recordingStore) ListRuleRolloutComparisons(context.Context, string, string, uuid.UUID, int) ([]domain.RuleRolloutComparison, error) {
	return s.ruleRolloutComparisons, nil
}

func (s *recordingStore) UpdateRuleRollout(_ context.Context, _ string, _ string, rolloutID uuid.UUID, input domain.RuleRolloutUpdateInput) (domain.RuleRollout, error) {
	if s.updateRuleRolloutErr != nil {
		return domain.RuleRollout{}, s.updateRuleRolloutErr
	}
	return domain.RuleRollout{ID: rolloutID, State: input.State, Revision: input.Revision + 1}, nil
}

func (s *recordingStore) ListAgentTaskPolicies(context.Context, string, string, int) ([]domain.AgentTaskPolicy, error) {
	return s.agentTaskPolicies, s.agentTaskPolicyErr
}

func (s *recordingStore) GetAgentTaskPolicy(_ context.Context, _ string, _ string, provider domain.Provider, apiBaseURL, repository string) (domain.AgentTaskPolicy, error) {
	s.agentTaskPolicyInput.Provider = provider
	s.agentTaskPolicyInput.APIBaseURL = apiBaseURL
	s.agentTaskPolicyInput.Repository = repository
	if s.agentTaskPolicyErr != nil {
		return domain.AgentTaskPolicy{}, s.agentTaskPolicyErr
	}
	if s.agentTaskPolicy.ID == uuid.Nil {
		return domain.AgentTaskPolicy{}, store.ErrNotFound
	}
	return s.agentTaskPolicy, nil
}

func (s *recordingStore) SaveAgentTaskPolicy(_ context.Context, _ string, _ string, input domain.AgentTaskPolicyInput) (domain.AgentTaskPolicy, error) {
	s.agentTaskPolicyInput = input
	if s.agentTaskPolicyErr != nil {
		return domain.AgentTaskPolicy{}, s.agentTaskPolicyErr
	}
	if s.agentTaskPolicy.ID == uuid.Nil {
		s.agentTaskPolicy = domain.AgentTaskPolicy{ID: uuid.New(), Mode: input.Mode, Revision: 1}
	}
	return s.agentTaskPolicy, nil
}

func (s *recordingStore) CreateAgentTask(_ context.Context, _ string, _ string, input domain.AgentTaskInput) (domain.AgentTask, error) {
	s.agentTaskInput = input
	if s.agentTaskErr != nil {
		return domain.AgentTask{}, s.agentTaskErr
	}
	if s.agentTask.ID == uuid.Nil {
		s.agentTask = domain.AgentTask{ID: uuid.New(), State: "received", Revision: 1}
	}
	return s.agentTask, nil
}

func (s *recordingStore) CreateAgentTaskFromProviderIssue(_ context.Context, _ string, _ string, analysisID uuid.UUID, expectedRevision int) (domain.AgentTask, error) {
	s.agentTaskFromAnalysisID = analysisID
	s.agentTaskFromAnalysisRevision = expectedRevision
	if s.agentTaskFromAnalysisErr != nil {
		return domain.AgentTask{}, s.agentTaskFromAnalysisErr
	}
	if s.agentTask.ID == uuid.Nil {
		s.agentTask = domain.AgentTask{ID: uuid.New(), State: "received", Revision: 1}
	}
	return s.agentTask, nil
}

func (s *recordingStore) ListAgentTasks(_ context.Context, _ string, _ string, _ int, before uuid.UUID) (domain.AgentTaskPage, error) {
	s.agentTaskListBefore = before
	if s.agentTaskListErr != nil {
		return domain.AgentTaskPage{}, s.agentTaskListErr
	}
	if s.agentTaskListPage.Tasks == nil {
		return domain.AgentTaskPage{Tasks: []domain.AgentTask{}}, nil
	}
	return s.agentTaskListPage, nil
}

func (s *recordingStore) GetAgentTask(context.Context, string, string, uuid.UUID) (domain.AgentTaskDetail, error) {
	return domain.AgentTaskDetail{Task: s.agentTask, Plans: []domain.AgentTaskPlan{s.agentTaskPlan}}, nil
}

func (s *recordingStore) RetryAgentTaskSource(_ context.Context, _ string, _ string, taskID uuid.UUID, revision int) (domain.AgentTask, error) {
	s.agentTaskSourceRetryRevision = revision
	if s.agentTaskSourceRetryErr != nil {
		return domain.AgentTask{}, s.agentTaskSourceRetryErr
	}
	return domain.AgentTask{ID: taskID, State: "received", SourceState: "pending", Revision: revision + 1}, nil
}

func (s *recordingStore) CreateAgentTaskPlan(_ context.Context, _ string, _ string, _ uuid.UUID, input domain.AgentTaskPlanInput) (domain.AgentTaskPlan, error) {
	s.agentTaskPlanInput = input
	if s.agentTaskPlanErr != nil {
		return domain.AgentTaskPlan{}, s.agentTaskPlanErr
	}
	if s.agentTaskPlan.ID == uuid.Nil {
		s.agentTaskPlan = domain.AgentTaskPlan{ID: uuid.New(), State: "awaiting_approval", Revision: 1}
	}
	return s.agentTaskPlan, nil
}

func (s *recordingStore) ApproveAgentTaskPlan(_ context.Context, _ string, _ string, _ uuid.UUID, _ uuid.UUID, input domain.AgentTaskPlanApprovalInput) (domain.AgentTaskPlan, error) {
	s.agentTaskPlanApproval = input
	if s.agentTaskPlanApprovalErr != nil {
		return domain.AgentTaskPlan{}, s.agentTaskPlanApprovalErr
	}
	return domain.AgentTaskPlan{ID: uuid.New(), State: "approved", Revision: input.Revision}, nil
}

func (s *recordingStore) CancelAgentTask(_ context.Context, _ string, _ string, taskID uuid.UUID, input domain.AgentTaskCancellationInput) (domain.AgentTask, error) {
	s.agentTaskCancellation = input
	if s.agentTaskCancellationErr != nil {
		return domain.AgentTask{}, s.agentTaskCancellationErr
	}
	return domain.AgentTask{ID: taskID, State: "cancelled", Revision: input.Revision + 1}, nil
}

func (s *recordingStore) UpsertProviderIdentity(_ context.Context, actor, tenant string, input domain.ProviderIdentity) (domain.ProviderIdentity, error) {
	s.providerIdentityActor = actor
	s.providerIdentityTenant = tenant
	s.providerIdentity = input
	return input, s.providerIdentityErr
}

func (s *recordingStore) ProcessInteraction(_ context.Context, input domain.InteractionCommand) (domain.InteractionOutcome, error) {
	s.interaction, s.interactionCalled = input, true
	return domain.InteractionOutcome{Accepted: true}, nil
}

func (s *recordingStore) ProcessAgentTaskCommand(_ context.Context, event domain.AgentTaskCommandEvent, command, normalized string) (domain.AgentTaskCommandOutcome, error) {
	s.agentTaskCommandEvent, s.agentTaskCommand, s.agentTaskCommandNormalized = event, command, normalized
	return s.agentTaskCommandOutcome, s.agentTaskCommandErr
}

func (*recordingStore) RecordFindingReaction(context.Context, domain.FindingReaction) error {
	return nil
}

func (s *recordingStore) RecordProviderIssueReaction(_ context.Context, reaction domain.ProviderIssueReaction) error {
	s.providerIssueReaction = reaction
	return nil
}

func (*recordingStore) GetFindingFeedbackDashboard(context.Context, string, string, int) (domain.FindingFeedbackDashboard, error) {
	return domain.FindingFeedbackDashboard{}, nil
}

func (s *recordingStore) ListFindings(_ context.Context, _, _ string, filter domain.FindingFilter) (domain.FindingPage, error) {
	s.findingFilter = filter
	return s.findingPage, s.listFindingsErr
}

func TestFindingExplorerFiltersBeforePaging(t *testing.T) {
	backend := &recordingStore{findingPage: domain.FindingPage{
		Findings:   []domain.FindingFeedbackItem{{ID: uuid.New(), Repository: "RainLib/open-review-platform", Severity: "high"}},
		NextCursor: "next-page",
	}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/findings?view=high-risk&repository=RainLib%2Fopen-review-platform&q=auth&cursor=opaque&cursor_direction=after&limit=25", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"next_cursor":"next-page"`) {
		t.Fatalf("list findings status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if backend.findingFilter.View != domain.FindingHighRisk || backend.findingFilter.Repository != "RainLib/open-review-platform" || backend.findingFilter.Query != "auth" || backend.findingFilter.Cursor != "opaque" || backend.findingFilter.Limit != 25 {
		t.Fatalf("finding filter not forwarded: %#v", backend.findingFilter)
	}
	for _, raw := range []string{"view=unknown", "limit=101", "cursor_direction=before", "q=" + strings.Repeat("a", 129)} {
		recorder = httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/findings?"+raw, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid finding filter %q status=%d body=%s", raw, recorder.Code, recorder.Body.String())
		}
	}
	backend.listFindingsErr = store.ErrForbidden
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/findings", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant finding status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func (*recordingStore) SetFindingDisposition(context.Context, string, string, uuid.UUID, domain.FindingDispositionInput) error {
	return nil
}

func (*recordingStore) ListReviewRuns(context.Context, string, string, int) ([]domain.ReviewRunSummary, error) {
	return nil, nil
}

func (s *recordingStore) ListWorkQueue(_ context.Context, _, _ string, filter domain.WorkQueueFilter) (domain.WorkQueuePage, error) {
	s.workQueueFilter = filter
	return s.workQueuePage, s.listWorkQueueErr
}

func (s *recordingStore) ListPullRequests(_ context.Context, _, _ string, filter domain.PullRequestFilter) (domain.PullRequestPage, error) {
	s.pullRequestFilter = filter
	return s.pullRequestPage, s.listPullRequestErr
}

func (*recordingStore) GetReviewRun(context.Context, string, string, uuid.UUID) (domain.ReviewRunSummary, error) {
	return domain.ReviewRunSummary{}, nil
}

func (s *recordingStore) GetReviewEvidence(_ context.Context, _, _ string, runID uuid.UUID) (domain.ReviewEvidence, error) {
	s.requestedEvidenceRunID = runID
	return s.reviewEvidence, s.getReviewEvidenceErr
}

func (s *recordingStore) GetReviewConfig(_ context.Context, _, _ string, section domain.ReviewConfigSection, scope domain.ReviewConfigScope) (domain.ReviewConfigView, error) {
	s.reviewConfigInput = domain.ReviewConfigInput{Section: section, ScopeKind: scope.Kind, ScopeRef: scope.Ref, ScopeProvider: scope.Provider, ScopeAPIBaseURL: scope.APIBaseURL}
	return s.reviewConfig, s.reviewConfigErr
}

func (s *recordingStore) ListReviewConfigVersions(_ context.Context, _, _ string, section domain.ReviewConfigSection, scope domain.ReviewConfigScope, _ int) (domain.ReviewConfigHistory, error) {
	s.reviewConfigInput = domain.ReviewConfigInput{Section: section, ScopeKind: scope.Kind, ScopeRef: scope.Ref, ScopeProvider: scope.Provider, ScopeAPIBaseURL: scope.APIBaseURL}
	return s.reviewConfigHistory, s.reviewConfigErr
}

func (s *recordingStore) ListIssueFormatTemplates(context.Context, string, string) ([]domain.IssueFormatTemplate, error) {
	return s.issueFormatTemplates, s.issueFormatTemplateErr
}

func (s *recordingStore) CreateIssueFormatTemplate(_ context.Context, _, _ string, input domain.IssueFormatTemplateInput) (domain.IssueFormatTemplate, error) {
	s.issueFormatTemplateInput = input
	return s.issueFormatTemplate, s.issueFormatTemplateErr
}

func (s *recordingStore) UpdateIssueFormatTemplate(_ context.Context, _, _ string, templateID uuid.UUID, input domain.IssueFormatTemplateInput) (domain.IssueFormatTemplate, error) {
	s.issueFormatTemplateID, s.issueFormatTemplateInput = templateID, input
	return s.issueFormatTemplate, s.issueFormatTemplateErr
}

func (s *recordingStore) ArchiveIssueFormatTemplate(_ context.Context, _, _ string, templateID uuid.UUID, expectedRevision int) error {
	s.issueFormatTemplateID, s.issueFormatTemplateExpectedRevision = templateID, expectedRevision
	return s.issueFormatTemplateErr
}

func (s *recordingStore) GetWorkspaceSetupCheckpoint(context.Context, string, string) (domain.WorkspaceSetupCheckpoint, error) {
	return s.setupCheckpoint, s.setupCheckpointErr
}

func (s *recordingStore) UpdateWorkspaceSetupCheckpoint(_ context.Context, _, _ string, input domain.WorkspaceSetupCheckpointInput) (domain.WorkspaceSetupCheckpoint, error) {
	s.setupCheckpointInput = input
	return s.setupCheckpoint, s.setupCheckpointErr
}

func (s *recordingStore) SaveReviewConfig(_ context.Context, _, _ string, input domain.ReviewConfigInput) (domain.ReviewConfigView, error) {
	s.reviewConfigInput = input
	return s.reviewConfig, s.reviewConfigErr
}

func (s *recordingStore) RestoreInheritedReviewConfig(_ context.Context, _, _ string, input domain.ReviewConfigInput) (domain.ReviewConfigView, error) {
	s.reviewConfigInput = input
	return s.reviewConfig, s.reviewConfigErr
}

func (s *recordingStore) RequestReviewConfigChange(_ context.Context, _, _ string, input domain.ReviewConfigChangeRequestInput) (domain.ReviewConfigChangeRequest, error) {
	s.reviewConfigChangeInput = input
	return s.reviewConfigChange, s.reviewConfigChangeErr
}

func (s *recordingStore) ListReviewConfigChangeRequests(context.Context, string, string, int) ([]domain.ReviewConfigChangeRequest, error) {
	return s.reviewConfigChanges, s.reviewConfigChangeErr
}

func (s *recordingStore) DecideReviewConfigChange(_ context.Context, _, _ string, id uuid.UUID, input domain.ReviewConfigChangeDecisionInput) (domain.ReviewConfigChangeRequest, error) {
	s.reviewConfigChangeID, s.reviewConfigChangeDecisionInput = id, input
	return s.reviewConfigChange, s.reviewConfigChangeErr
}

func (*recordingStore) GetRuleSnapshot(context.Context, string, string, uuid.UUID) (domain.RuleSnapshot, error) {
	return domain.RuleSnapshot{}, nil
}

func (s *recordingStore) ListRunEvents(ctx context.Context, _ string, _ string, _ uuid.UUID, after int) ([]domain.RunEvent, error) {
	s.listRunEventsAfter = after
	if s.listRunEventsHook != nil {
		s.listRunEventsHook(ctx, after)
	}
	if s.listRunEventsErr != nil {
		return nil, s.listRunEventsErr
	}
	events := make([]domain.RunEvent, 0, len(s.runEvents))
	for _, event := range s.runEvents {
		if event.Revision > after {
			events = append(events, event)
		}
	}
	return events, nil
}

func (*recordingStore) RequestRunCancellation(context.Context, string, string, uuid.UUID, int) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func (s *recordingStore) RequestRunRetry(_ context.Context, _, _ string, runID uuid.UUID, input domain.RunRetryInput) (domain.RunRetryResult, error) {
	s.runRetryID, s.runRetryInput = runID, input
	return s.runRetry, s.runRetryErr
}

func (s *recordingStore) RetryInteractionResponse(_ context.Context, _, _ string, runID uuid.UUID, input domain.RunRetryInput) (domain.InteractionResponseRetryResult, error) {
	s.interactionResponseRetryRunID, s.interactionResponseRetryInput = runID, input
	return s.interactionResponseRetry, s.interactionResponseRetryErr
}

func (s *recordingStore) CreateReviewSchedule(_ context.Context, _, _ string, runID uuid.UUID, input domain.ReviewScheduleInput) (domain.ReviewSchedule, error) {
	s.reviewScheduleRunID, s.reviewScheduleInput = runID, input
	return s.reviewSchedule, s.reviewScheduleErr
}

func (s *recordingStore) ListReviewSchedules(_ context.Context, _, _ string, limit int) ([]domain.ReviewSchedule, error) {
	s.reviewScheduleLimit = limit
	return s.reviewSchedules, s.reviewScheduleErr
}

func (s *recordingStore) CancelReviewSchedule(_ context.Context, _, _ string, scheduleID uuid.UUID, expectedRevision int) (domain.ReviewSchedule, error) {
	s.reviewScheduleID, s.reviewScheduleExpectedRevision = scheduleID, expectedRevision
	return s.reviewSchedule, s.reviewScheduleErr
}

func (*recordingStore) AdmitDueReviewSchedule(context.Context, string) (domain.ReviewSchedule, bool, error) {
	return domain.ReviewSchedule{}, false, nil
}

func (s *recordingStore) ListReviewInterventions(_ context.Context, _, _ string, filter domain.ReviewInterventionFilter) ([]domain.ReviewIntervention, error) {
	s.reviewInterventionFilter = filter
	return s.reviewInterventions, s.reviewInterventionErr
}

func (s *recordingStore) ClaimReviewIntervention(_ context.Context, _, _ string, runID uuid.UUID, expectedRevision int) (domain.ReviewIntervention, error) {
	s.reviewInterventionRunID, s.reviewInterventionExpectedRevision = runID, expectedRevision
	return s.reviewIntervention, s.reviewInterventionErr
}

func (s *recordingStore) ResolveReviewIntervention(_ context.Context, _, _ string, runID uuid.UUID, input domain.ReviewInterventionResolutionInput) (domain.ReviewIntervention, error) {
	s.reviewInterventionRunID, s.reviewInterventionResolution = runID, input
	return s.reviewIntervention, s.reviewInterventionErr
}

func (*recordingStore) AdvanceRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func (*recordingStore) AdvanceLegacyRun(context.Context, uuid.UUID, domain.RunState) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func TestGitHubIssueCommentCommandIsVerifiedAndNormalized(t *testing.T) {
	secret := "secret"
	body := []byte(`{"action":"created","installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"issue":{"number":42,"pull_request":{"url":"https://api.github.com/repos/acme/api/pulls/42"}},"comment":{"id":99,"body":"@openreview review --mode=deep","user":{"id":7}}}`)
	recording := &recordingStore{}
	server := New(recording, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issue_comment")
	request.Header.Set("X-GitHub-Delivery", "delivery-comment-1")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !recording.interactionCalled {
		t.Fatalf("expected accepted interaction, status=%d called=%v", response.Code, recording.interactionCalled)
	}
	if recording.interaction.Command != "review" || recording.interaction.Mode != "deep" || recording.interaction.Event.ActorExternalID != "7" {
		t.Fatalf("unexpected interaction: %#v", recording.interaction)
	}

	recording.interactionCalled = false
	request = httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issue_comment")
	request.Header.Set("X-GitHub-Delivery", "delivery-comment-2")
	request.Header.Set("X-Hub-Signature-256", "sha256=wrong")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || recording.interactionCalled {
		t.Fatalf("invalid signature must not process command, status=%d called=%v", response.Code, recording.interactionCalled)
	}
}

func TestGitHubPullRequestCannotRouteAgentPlanApproval(t *testing.T) {
	secret := "secret"
	body := []byte(fmt.Sprintf(`{"action":"created","installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"issue":{"number":42,"pull_request":{"url":"https://api.github.com/repos/acme/api/pulls/42"}},"comment":{"id":101,"body":%q,"user":{"id":7}}}`, "@openreview approve "+strings.Repeat("ab", 32)))
	recording := &recordingStore{}
	server := New(recording, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issue_comment")
	request.Header.Set("X-GitHub-Delivery", "delivery-pr-approval-rejected")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || recording.interaction.Command != "invalid" || recording.agentTaskCommand != "" {
		t.Fatalf("PR plan-approval routing status=%d interaction=%#v taskCommand=%q", response.Code, recording.interaction, recording.agentTaskCommand)
	}
}

func TestGitHubIssueImplementCommandCreatesOnlyAnAgentTaskCandidate(t *testing.T) {
	secret, taskID := "secret", uuid.New()
	body := []byte(`{"action":"created","installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"issue":{"number":42,"title":"Retry loses state","body":"Retries duplicate work","updated_at":"2026-09-22T01:02:03Z"},"comment":{"id":99,"body":"@openreview implement","user":{"id":7}}}`)
	recording := &recordingStore{agentTaskCommandOutcome: domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &taskID, Reason: "agent task recorded"}}
	server := New(recording, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issue_comment")
	request.Header.Set("X-GitHub-Delivery", "delivery-agent-issue-1")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	expectedRevision := domain.AgentIssueRevision(domain.ProviderGitHub, "https://api.github.com", "acme/api", 42, "Retry loses state", "Retries duplicate work")
	if response.Code != http.StatusAccepted || recording.agentTaskCommand != "implement" || recording.agentTaskCommandEvent.IssueNumber != 42 || recording.agentTaskCommandEvent.IssueRevision != expectedRevision || !strings.Contains(response.Body.String(), taskID.String()) {
		t.Fatalf("issue implement status=%d body=%s event=%#v command=%q", response.Code, response.Body.String(), recording.agentTaskCommandEvent, recording.agentTaskCommand)
	}
	if recording.interactionCalled {
		t.Fatal("Issue implement must not fabricate a pull-request review interaction")
	}
}

func TestGitHubIssueAgentControlCommandsRouteWithoutStartingAReview(t *testing.T) {
	secret, taskID := "secret", uuid.New()
	cases := []struct {
		name    string
		body    string
		command string
	}{
		{name: "status", body: "@openreview status", command: "status"},
		{name: "stop aliases cancellation", body: "@openreview stop", command: "cancel"},
		{name: "approve exact digest", body: "@openreview approve " + strings.Repeat("ab", 32), command: "approve"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"action":"created","installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"issue":{"number":42,"updated_at":"2026-09-22T01:02:03Z"},"comment":{"id":99,"body":%q,"user":{"id":7}}}`, testCase.body))
			recording := &recordingStore{agentTaskCommandOutcome: domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &taskID}}
			server := New(recording, nil, secret, "")
			mux := http.NewServeMux()
			server.Register(mux)
			request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
			request.Header.Set("X-GitHub-Event", "issue_comment")
			request.Header.Set("X-GitHub-Delivery", "delivery-agent-control-"+testCase.command)
			request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)

			if response.Code != http.StatusAccepted || recording.agentTaskCommand != testCase.command || recording.agentTaskCommandEvent.IssueNumber != 42 {
				t.Fatalf("status=%d body=%s event=%#v command=%q", response.Code, response.Body.String(), recording.agentTaskCommandEvent, recording.agentTaskCommand)
			}
			if testCase.command == "approve" && recording.agentTaskCommandNormalized != testCase.body {
				t.Fatalf("approval digest lost in normalized command: %q", recording.agentTaskCommandNormalized)
			}
			if recording.interactionCalled {
				t.Fatal("Issue agent control commands must not fabricate a pull-request review interaction")
			}
		})
	}
}

func TestGitLabIssueImplementNoteIsSeparatedFromMergeRequestReview(t *testing.T) {
	secret, taskID := "gitlab-secret", uuid.New()
	body := []byte(`{"object_kind":"note","project":{"id":123,"path_with_namespace":"acme/api","http_url":"https://gitlab.example/acme/api.git"},"issue":{"iid":19,"updated_at":"2026-09-22T01:02:03Z"},"object_attributes":{"action":"create","id":99,"note":"@openreview implement","noteable_type":"Issue"},"user":{"id":7}}`)
	recording := &recordingStore{agentTaskCommandOutcome: domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &taskID}}
	server := New(recording, nil, "", secret)
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/gitlab", bytes.NewReader(body))
	request.Header.Set("X-Gitlab-Event", "Note Hook")
	request.Header.Set("X-Gitlab-Event-UUID", "delivery-agent-issue-2")
	request.Header.Set("X-Gitlab-Token", secret)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || recording.agentTaskCommand != "implement" || recording.agentTaskCommandEvent.Provider != domain.ProviderGitLab || recording.agentTaskCommandEvent.IssueNumber != 19 {
		t.Fatalf("GitLab issue implement status=%d body=%s event=%#v command=%q", response.Code, response.Body.String(), recording.agentTaskCommandEvent, recording.agentTaskCommand)
	}
}

func TestGitLabIssuePlanApprovalNoteRoutesToAgentTask(t *testing.T) {
	secret, digest := "gitlab-secret", strings.Repeat("ab", 32)
	body := []byte(fmt.Sprintf(`{"object_kind":"note","project":{"id":123,"path_with_namespace":"acme/api","http_url":"https://gitlab.example/acme/api.git"},"issue":{"iid":19,"updated_at":"2026-09-22T01:02:03Z"},"object_attributes":{"action":"create","id":100,"note":%q,"noteable_type":"Issue"},"user":{"id":7}}`, "@openreview approve "+digest))
	recording := &recordingStore{agentTaskCommandOutcome: domain.AgentTaskCommandOutcome{Accepted: true}}
	server := New(recording, nil, "", secret)
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/gitlab", bytes.NewReader(body))
	request.Header.Set("X-Gitlab-Event", "Note Hook")
	request.Header.Set("X-Gitlab-Event-UUID", "delivery-agent-approval-gitlab")
	request.Header.Set("X-Gitlab-Token", secret)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || recording.agentTaskCommand != "approve" || recording.agentTaskCommandNormalized != "@openreview approve "+digest || recording.agentTaskCommandEvent.Provider != domain.ProviderGitLab {
		t.Fatalf("GitLab approval status=%d body=%s command=%q normalized=%q", response.Code, response.Body.String(), recording.agentTaskCommand, recording.agentTaskCommandNormalized)
	}
	if recording.interactionCalled {
		t.Fatal("GitLab Issue approval must not start a merge-request review")
	}
}

func TestGitHubUserIssueIsVerifiedAndQueuedForAnalysis(t *testing.T) {
	secret := "secret"
	body := []byte(`{"action":"opened","installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"issue":{"number":17,"title":"Retries lose state","body":"Steps to reproduce","user":{"login":"alice","type":"User"},"labels":[{"name":"bug"}]}}`)
	recording := &recordingStore{providerIssueOutcome: domain.ProviderIssueAnalysisEnqueue{Job: domain.ProviderIssueAnalysisJob{ID: uuid.New()}}}
	server := New(recording, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issues")
	request.Header.Set("X-GitHub-Delivery", "delivery-issue-1")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || recording.providerIssueEvent.IssueNumber != 17 || recording.providerIssueEvent.Author != "alice" || recording.automaticAgentTaskEvent.IssueNumber != 17 {
		t.Fatalf("status=%d event=%#v body=%s", response.Code, recording.providerIssueEvent, response.Body.String())
	}
}

func TestGitHubIssueAnalysisThumbReactionIsRecordedWithoutStartingWork(t *testing.T) {
	secret := "secret"
	body := []byte(`{"action":"created","repository":{"full_name":"acme/api"},"comment":{"body":"Analysis\n<!-- open-review-platform:issue-triage:4362e8f7-68eb-4d09-b0a9-747577fb0514 -->"},"reaction":{"id":92,"content":"+1","user":{"id":43}}}`)
	recording := &recordingStore{}
	server := New(recording, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "reaction")
	request.Header.Set("X-GitHub-Delivery", "delivery-issue-reaction-1")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || recording.providerIssueReaction.Kind != "useful" || recording.providerIssueReaction.AnalysisMarker == "" {
		t.Fatalf("status=%d reaction=%#v body=%s", response.Code, recording.providerIssueReaction, response.Body.String())
	}
	if recording.called || recording.interactionCalled {
		t.Fatal("feedback reaction must not enqueue analysis or interaction work")
	}
}

func TestProviderIdentitySelfBindingUsesAuthenticatedSubject(t *testing.T) {
	recording := &recordingStore{}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/provider-identities/github/42/self", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if recording.providerIdentityActor != "operator" || recording.providerIdentityTenant != "acme" {
		t.Fatalf("unexpected actor binding: actor=%q tenant=%q", recording.providerIdentityActor, recording.providerIdentityTenant)
	}
	if recording.providerIdentity.Provider != domain.ProviderGitHub || recording.providerIdentity.ExternalID != "42" || recording.providerIdentity.Subject != "operator" {
		t.Fatalf("unexpected provider identity: %#v", recording.providerIdentity)
	}
}

func (s *recordingStore) Enqueue(_ context.Context, event domain.InboundEvent) (domain.ReviewJob, bool, error) {
	s.called, s.event = true, event
	if s.enqueueJob.ID == uuid.Nil && s.enqueueJob.ErrorMessage != "" {
		return s.enqueueJob, false, nil
	}
	return domain.ReviewJob{ID: uuid.New()}, false, nil
}

func (s *recordingStore) EnqueueProviderIssueAnalysis(_ context.Context, event domain.ProviderIssueEvent) (domain.ProviderIssueAnalysisEnqueue, error) {
	s.called, s.providerIssueEvent = true, event
	return s.providerIssueOutcome, s.providerIssueErr
}

func (s *recordingStore) ProcessAutomaticAgentTask(_ context.Context, event domain.ProviderIssueEvent) (domain.AgentTaskCommandOutcome, error) {
	s.automaticAgentTaskEvent = event
	return s.automaticAgentTaskOutcome, s.automaticAgentTaskErr
}

func (*recordingStore) Claim(context.Context, string) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}

func (*recordingStore) ClaimForRun(context.Context, string, uuid.UUID) (*domain.ReviewJob, error) {
	return nil, store.ErrNoQueuedJob
}

func (*recordingStore) RuleSnapshotForJob(context.Context, uuid.UUID) (domain.RuleSnapshot, error) {
	return domain.RuleSnapshot{}, store.ErrNotFound
}

func (*recordingStore) SaveFindings(context.Context, uuid.UUID, []domain.Finding) error { return nil }
func (*recordingStore) Succeed(context.Context, uuid.UUID, string) error                { return nil }
func (*recordingStore) Fail(context.Context, uuid.UUID, string, string) error           { return nil }
func (*recordingStore) Cancel(context.Context, uuid.UUID, string) error                 { return nil }
func (*recordingStore) RenewClaim(context.Context, uuid.UUID, string, time.Duration) error {
	return nil
}
func (*recordingStore) Close() {}

func TestGitHubWebhookVerifiesBeforeQueueing(t *testing.T) {
	secret := "secret"
	body := []byte(`{"action":"opened","number":42,"installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"pull_request":{"base":{"ref":"main","sha":"base"},"head":{"ref":"feature","sha":"head"}}}`)
	store := &recordingStore{}
	server := New(store, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "pull_request")
	request.Header.Set("X-GitHub-Delivery", "delivery-1")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !store.called {
		t.Fatalf("expected accepted queued webhook, status=%d called=%v", response.Code, store.called)
	}
	if store.event.APIBaseURL != "https://api.github.com" || store.event.InstallationExternalID != "123" {
		t.Fatalf("unexpected event: %#v", store.event)
	}

	store.called = false
	request = httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "pull_request")
	request.Header.Set("X-GitHub-Delivery", "delivery-2")
	request.Header.Set("X-Hub-Signature-256", "sha256=wrong")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || store.called {
		t.Fatalf("invalid signature must not queue, status=%d called=%v", response.Code, store.called)
	}
}

func TestGitLabRelativeURLWebhookUsesConfiguredAPIBase(t *testing.T) {
	backend := &recordingStore{}
	server := NewWithProviderAPIURLs(backend, nil, "", "gitlab-secret", "https://api.github.com", "https://gitlab.example/gitlab/api/v4")
	mux := http.NewServeMux()
	server.Register(mux)
	body := []byte(`{"project":{"id":77,"path_with_namespace":"acme/api","git_http_url":"https://gitlab.example/gitlab/acme/api.git"},"user":{"id":42,"username":"author"},"object_attributes":{"action":"open","author_id":42,"title":"Guard refresh","iid":9,"target_branch":"main","source_branch":"feature","last_commit":{"id":"head"}}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/gitlab", bytes.NewReader(body))
	request.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	request.Header.Set("X-Gitlab-Event-UUID", "relative-url-mr-1")
	request.Header.Set("X-Gitlab-Token", "gitlab-secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !backend.called || backend.event.APIBaseURL != "https://gitlab.example/gitlab/api/v4" || backend.event.CloneURL != "https://gitlab.example/gitlab/acme/api.git" {
		t.Fatalf("relative URL webhook status=%d event=%#v called=%t body=%s", response.Code, backend.event, backend.called, response.Body.String())
	}

	backend.called = false
	request = httptest.NewRequest(http.MethodPost, "/v1/webhooks/gitlab", bytes.NewReader(body))
	request.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	request.Header.Set("X-Gitlab-Token", "wrong-secret")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || backend.called {
		t.Fatalf("invalid GitLab token status=%d called=%t", response.Code, backend.called)
	}
}

func TestGitLabWebhookReportsDeferredAuthorVerification(t *testing.T) {
	backend := &recordingStore{enqueueJob: domain.ReviewJob{
		State: domain.JobQueued, ErrorMessage: "provider author verification queued",
	}}
	server := NewWithProviderAPIURLs(backend, nil, "", "gitlab-secret", "https://api.github.com", "https://gitlab.example/api/v4")
	mux := http.NewServeMux()
	server.Register(mux)
	body := []byte(`{"project":{"id":77,"path_with_namespace":"acme/api","git_http_url":"https://gitlab.example/acme/api.git"},"user":{"id":99,"username":"updater"},"object_attributes":{"action":"update","author_id":42,"title":"Guard refresh","iid":9,"target_branch":"main","source_branch":"feature","last_commit":{"id":"head"}}}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/gitlab", bytes.NewReader(body))
	request.Header.Set("X-Gitlab-Event", "Merge Request Hook")
	request.Header.Set("X-Gitlab-Event-UUID", "deferred-author-1")
	request.Header.Set("X-Gitlab-Token", "gitlab-secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !backend.called {
		t.Fatalf("deferred webhook status=%d called=%t body=%s", response.Code, backend.called, response.Body.String())
	}
	var payload struct {
		Accepted bool   `json:"accepted"`
		Deferred bool   `json:"deferred"`
		Skipped  bool   `json:"skipped"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Accepted || !payload.Deferred || payload.Skipped || payload.Reason != "provider author verification queued" {
		t.Fatalf("unexpected deferred response: %#v", payload)
	}
}

func TestGitHubWebhookReportsAConfigurationPolicySkip(t *testing.T) {
	secret := "secret"
	body := []byte(`{"action":"opened","number":42,"installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"pull_request":{"base":{"ref":"main","sha":"base"},"head":{"ref":"feature","sha":"head"}}}`)
	backend := &recordingStore{enqueueJob: domain.ReviewJob{State: domain.JobCancelled, ErrorMessage: "draft pull requests are excluded by workspace policy"}}
	server := New(backend, nil, secret, "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "pull_request")
	request.Header.Set("X-GitHub-Delivery", "delivery-policy-skip")
	request.Header.Set("X-Hub-Signature-256", githubSignature(secret, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected accepted policy skip, status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Accepted bool   `json:"accepted"`
		Skipped  bool   `json:"skipped"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || !payload.Accepted || !payload.Skipped || payload.Reason != backend.enqueueJob.ErrorMessage {
		t.Fatalf("unexpected policy skip response: payload=%#v err=%v", payload, err)
	}
}

func TestRuleCatalogListsReleaseProvenanceAndInstallsAnExactDraft(t *testing.T) {
	record := &recordingStore{ruleCatalogEntries: []domain.RuleCatalogEntry{{
		ID: "core.durable-idempotency", Version: "1.0.0", Title: "Durable idempotency",
		ContentSHA256: strings.Repeat("a", 64), Origin: "open-review://catalog/core", CanInstall: true,
	}}}
	server := New(record, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/rule-catalog", nil)
	listResponse := httptest.NewRecorder()
	mux.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var listed struct {
		Entries []domain.RuleCatalogEntry `json:"entries"`
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Entries) != 1 || listed.Entries[0].Origin != "open-review://catalog/core" || !listed.Entries[0].CanInstall {
		t.Fatalf("unexpected catalog response: %#v", listed)
	}

	body := `{"version":"1.0.0","content_sha256":"` + strings.Repeat("a", 64) + `"}`
	installRequest := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/rule-catalog/core.durable-idempotency/installations", strings.NewReader(body))
	installResponse := httptest.NewRecorder()
	mux.ServeHTTP(installResponse, installRequest)
	if installResponse.Code != http.StatusCreated {
		t.Fatalf("install status=%d body=%s", installResponse.Code, installResponse.Body.String())
	}
	if record.ruleCatalogInstallID != "core.durable-idempotency" || record.ruleCatalogInstallInput.Version != "1.0.0" || record.ruleCatalogInstallInput.ContentSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("unexpected install input: id=%q input=%#v", record.ruleCatalogInstallID, record.ruleCatalogInstallInput)
	}

	record.ruleCatalogInstall.Replayed = true
	replayResponse := httptest.NewRecorder()
	mux.ServeHTTP(replayResponse, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/rule-catalog/core.durable-idempotency/installations", strings.NewReader(body)))
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
}

func TestRuleCatalogRejectsStaleTemplateIdentity(t *testing.T) {
	record := &recordingStore{ruleCatalogErr: store.ErrInvalidRuleCatalog}
	server := New(record, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/rule-catalog/core.durable-idempotency/installations", strings.NewReader(`{"version":"1.0.0","content_sha256":"stale"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "content digest") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestManagementMutationErrorsAreClassified(t *testing.T) {
	validBindingID := uuid.New()
	validRuleSetID := uuid.New()
	validApprovalRequestID := uuid.New()
	tests := []struct {
		name          string
		method        string
		path          string
		body          string
		configure     func(*recordingStore, error)
		validationErr error
		expectedError string
	}{
		{
			name:          "create rule set",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-sets",
			body:          `{}`,
			configure:     func(s *recordingStore, err error) { s.createRuleSetErr = err },
			validationErr: store.ErrInvalidRuleSet,
			expectedError: "rule set is invalid",
		},
		{
			name:          "request rule approval",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-sets/" + validRuleSetID.String() + "/versions/1/approval-requests",
			body:          `{"required_approvals":1}`,
			configure:     func(s *recordingStore, err error) { s.requestRuleApprovalErr = err },
			validationErr: store.ErrInvalidRuleApproval,
			expectedError: "rule approval request is invalid",
		},
		{
			name:          "decide rule approval",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-approval-requests/" + validApprovalRequestID.String() + "/decisions",
			body:          `{"decision":"approved"}`,
			configure:     func(s *recordingStore, err error) { s.decideRuleApprovalErr = err },
			validationErr: store.ErrInvalidRuleApproval,
			expectedError: "rule approval decision is invalid",
		},
		{
			name:          "create binding",
			method:        http.MethodPost,
			path:          "/v1/tenants/acme/rule-bindings",
			body:          `{}`,
			configure:     func(s *recordingStore, err error) { s.createBindingErr = err },
			validationErr: store.ErrInvalidRuleBinding,
			expectedError: "rule binding is invalid",
		},
		{
			name:          "update binding",
			method:        http.MethodPatch,
			path:          "/v1/tenants/acme/rule-bindings/" + validBindingID.String(),
			body:          `{"state":"active"}`,
			configure:     func(s *recordingStore, err error) { s.updateBindingErr = err },
			validationErr: store.ErrInvalidRuleBinding,
			expectedError: "rule binding state is invalid",
		},
		{
			name:          "upsert provider identity",
			method:        http.MethodPut,
			path:          "/v1/tenants/acme/provider-identities/github/42",
			body:          `{"subject":"operator"}`,
			configure:     func(s *recordingStore, err error) { s.providerIdentityErr = err },
			validationErr: store.ErrInvalidProviderIdentity,
			expectedError: "provider identity is invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, result := range []struct {
				name       string
				err        error
				statusCode int
			}{
				{name: "validation", err: test.validationErr, statusCode: http.StatusBadRequest},
				{name: "storage failure", err: errors.New("database unavailable"), statusCode: http.StatusInternalServerError},
			} {
				t.Run(result.name, func(t *testing.T) {
					recording := &recordingStore{}
					test.configure(recording, result.err)
					server := New(recording, fixedAuthenticator{}, "", "")
					mux := http.NewServeMux()
					server.Register(mux)

					request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, request)
					if response.Code != result.statusCode {
						t.Fatalf("status=%d, want %d; body=%s", response.Code, result.statusCode, response.Body.String())
					}
					if result.statusCode == http.StatusBadRequest && !strings.Contains(response.Body.String(), test.expectedError) {
						t.Fatalf("body=%q does not contain %q", response.Body.String(), test.expectedError)
					}
				})
			}
		})
	}
}

func TestRuleBindingGovernedRolloutConflicts(t *testing.T) {
	for _, test := range []struct {
		name, method, path, body, message string
		configure                         func(*recordingStore)
	}{
		{
			name: "direct active binding creation", method: http.MethodPost,
			path: "/v1/tenants/acme/rule-bindings", body: `{}`,
			message:   "create a matching Shadow candidate",
			configure: func(s *recordingStore) { s.createBindingErr = store.ErrRuleRolloutRequired },
		},
		{
			name: "direct candidate activation", method: http.MethodPatch,
			path: "/v1/tenants/acme/rule-bindings/" + uuid.NewString(), body: `{"state":"active"}`,
			message:   "direct activation is unavailable",
			configure: func(s *recordingStore) { s.updateBindingErr = store.ErrRuleRolloutRequired },
		},
		{
			name: "active Canary binding mutation", method: http.MethodPatch,
			path: "/v1/tenants/acme/rule-bindings/" + uuid.NewString(), body: `{"state":"disabled"}`,
			message:   "pause and roll back the active Canary",
			configure: func(s *recordingStore) { s.updateBindingErr = store.ErrActiveRuleRollout },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recording := &recordingStore{}
			test.configure(recording)
			server := New(recording, fixedAuthenticator{}, "", "")
			mux := http.NewServeMux()
			server.Register(mux)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), test.message) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRuleRolloutEndpointsExposeRevisionedOperationalControls(t *testing.T) {
	baseline, candidate, rolloutID := uuid.New(), uuid.New(), uuid.New()
	recording := &recordingStore{ruleRollouts: []domain.RuleRollout{{
		ID:                 rolloutID,
		BaselineBindingID:  baseline,
		CandidateBindingID: candidate,
		Mode:               "shadow",
		State:              "active",
		Revision:           3,
	}}, ruleRolloutComparisons: []domain.RuleRolloutComparison{{
		ID: rolloutID, RolloutID: rolloutID, State: "completed", BaselineFindingCount: 1, CandidateFindingCount: 2, AddedFindingCount: 1, MatchedFindingCount: 1,
	}}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	create := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/rule-rollouts", strings.NewReader(fmt.Sprintf(`{"baseline_binding_id":%q,"candidate_binding_id":%q,"mode":"shadow","canary_basis_points":0}`, baseline, candidate)))
	createResponse := httptest.NewRecorder()
	mux.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	if recording.ruleRolloutInput.BaselineBindingID != baseline || recording.ruleRolloutInput.CandidateBindingID != candidate || recording.ruleRolloutInput.Mode != "shadow" {
		t.Fatalf("unexpected rollout input: %#v", recording.ruleRolloutInput)
	}

	list := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/rule-rollouts?limit=25", nil)
	listResponse := httptest.NewRecorder()
	mux.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), rolloutID.String()) || !strings.Contains(listResponse.Body.String(), `"revision":3`) {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	update := httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/rule-rollouts/"+rolloutID.String(), strings.NewReader(`{"state":"paused","revision":3}`))
	updateResponse := httptest.NewRecorder()
	mux.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK || !strings.Contains(updateResponse.Body.String(), `"state":"paused"`) || !strings.Contains(updateResponse.Body.String(), `"revision":4`) {
		t.Fatalf("update status=%d body=%s", updateResponse.Code, updateResponse.Body.String())
	}
	recording.updateRuleRolloutErr = store.ErrRuleRolloutEvidence
	advance := httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/rule-rollouts/"+rolloutID.String(), strings.NewReader(`{"state":"active","canary_basis_points":2500,"revision":3}`))
	advanceResponse := httptest.NewRecorder()
	mux.ServeHTTP(advanceResponse, advance)
	if advanceResponse.Code != http.StatusConflict || !strings.Contains(advanceResponse.Body.String(), "observation is incomplete") {
		t.Fatalf("evidence-gated advance status=%d body=%s", advanceResponse.Code, advanceResponse.Body.String())
	}
	recording.updateRuleRolloutErr = nil
	comparisons := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/rule-rollouts/"+rolloutID.String()+"/comparisons?limit=25", nil)
	comparisonsResponse := httptest.NewRecorder()
	mux.ServeHTTP(comparisonsResponse, comparisons)
	if comparisonsResponse.Code != http.StatusOK || !strings.Contains(comparisonsResponse.Body.String(), `"added_finding_count":1`) {
		t.Fatalf("comparison status=%d body=%s", comparisonsResponse.Code, comparisonsResponse.Body.String())
	}
}

func TestListRuleApprovalRequestsReturnsGovernanceReadModel(t *testing.T) {
	requestID := uuid.New()
	recording := &recordingStore{
		ruleApprovalRequests: []domain.RuleApprovalSummary{{
			ID:                requestID,
			RuleSetName:       "Credential boundary",
			Version:           2,
			State:             "pending",
			RequiredApprovals: 2,
			ApprovalCount:     1,
			CanDecide:         true,
		}},
	}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/rule-approval-requests?limit=25", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{requestID.String(), "Credential boundary", `"approval_count":1`, `"can_decide":true`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("body=%q does not contain %q", body, expected)
		}
	}
}

func TestListRuleApprovalRequestsRejectsInvalidLimit(t *testing.T) {
	server := New(&recordingStore{}, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/rule-approval-requests?limit=101", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestPreviewRuleVersionImpactIsReadOnlyResponse(t *testing.T) {
	ruleSetID := uuid.New()
	recording := &recordingStore{ruleImpactPreview: domain.RuleImpactPreview{
		RuleSetID:          ruleSetID,
		RuleSetName:        "Credential boundary",
		Version:            1,
		Valid:              true,
		BaselineRuleCount:  2,
		CandidateRuleCount: 3,
		AddedRuleKeys:      []string{"security.credentials"},
		Uncertainty:        []string{"Static preview only; OCR was not executed."},
	}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	path := "/v1/tenants/acme/rule-sets/" + ruleSetID.String() + "/versions/1/impact-preview"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"repository":"RainLib/open-review-platform","provider":"github","api_base_url":"https://api.github.com","target_branch":"main","precedence":100}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, expected := range []string{`"valid":true`, `"candidate_rule_count":3`, "security.credentials", "OCR was not executed"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("body=%q does not contain %q", response.Body.String(), expected)
		}
	}
	if recording.ruleImpactPreviewInput.Provider != domain.ProviderGitHub || recording.ruleImpactPreviewInput.APIBaseURL != "https://api.github.com" {
		t.Fatalf("preview provider scope=%#v", recording.ruleImpactPreviewInput)
	}
}

func TestCreateRuleTestRunReturnsAcceptedIsolatedExecution(t *testing.T) {
	ruleSetID := uuid.New()
	sourceRunID := uuid.New()
	server := New(&recordingStore{}, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	path := "/v1/tenants/acme/rule-sets/" + ruleSetID.String() + "/versions/1/test-runs"
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"source_run_id":"`+sourceRunID.String()+`"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"state":"queued"`) || !strings.Contains(response.Body.String(), `"findings":[]`) {
		t.Fatalf("unexpected isolated test response: %s", response.Body.String())
	}
}

func TestRuleExceptionLifecycleEndpointsUseGovernanceAPI(t *testing.T) {
	backend := &recordingStore{}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)
	versionID := uuid.New()
	issueID := uuid.New()
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/rule-exceptions", strings.NewReader(`{"rule_version_id":"`+versionID.String()+`","rule_key":"security.auth","scope_kind":"repository","scope_ref":"RainLib/demo","scope_provider":"github","scope_api_base_url":"https://api.github.com","reason":"temporary migration","expires_at":"`+expiresAt+`","source_issue_id":"`+issueID.String()+`","source_issue_revision":7}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"state":"pending"`) {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if backend.ruleExceptionInput.SourceIssueID == nil || *backend.ruleExceptionInput.SourceIssueID != issueID || backend.ruleExceptionInput.SourceIssueRevision != 7 || backend.ruleExceptionInput.ScopeProvider != domain.ProviderGitHub || backend.ruleExceptionInput.ScopeAPIBaseURL != "https://api.github.com" {
		t.Fatalf("source issue input=%#v", backend.ruleExceptionInput)
	}

	exceptionID := uuid.New()
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/rule-exceptions/"+exceptionID.String()+"/decisions", strings.NewReader(`{"decision":"approved"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"approved"`) {
		t.Fatalf("decision status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/rule-exceptions/"+exceptionID.String()+"/revoke", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"revoked"`) {
		t.Fatalf("revoke status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstallationResponseDoesNotExposeCredentialReference(t *testing.T) {
	recording := &recordingStore{
		installation: domain.Installation{
			ID:              uuid.New(),
			Provider:        domain.ProviderGitHub,
			ExternalID:      "123",
			RepositoryScope: "RainLib/*",
			APIBaseURL:      "https://api.github.com",
			CredentialRef:   "github-app",
			Active:          true,
		},
	}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"github","external_id":"123","repository_scope":"RainLib/*","api_base_url":"https://api.github.com","credential_ref":"github-app"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "credential_ref") || strings.Contains(response.Body.String(), "github-app") {
		t.Fatalf("credential reference must not be serialized: %s", response.Body.String())
	}
}

func TestExactInstallationReadIsTenantScopedAndCredentialFree(t *testing.T) {
	installationID := uuid.New()
	recording := &recordingStore{installationSummary: domain.InstallationSummary{
		ID: installationID, Provider: domain.ProviderGitHub, ExternalID: "123",
		RepositoryScope: "RainLib/*", APIBaseURL: "https://api.github.com", Active: true,
	}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)
	path := "/v1/tenants/acme/installations/" + installationID.String()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK || recording.getInstallationID != installationID || !strings.Contains(response.Body.String(), installationID.String()) {
		t.Fatalf("exact installation status=%d id=%s body=%s", response.Code, recording.getInstallationID, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "credential_ref") {
		t.Fatalf("worker credential reference leaked in installation summary: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("exact installation response is cacheable: %#v", response.Header())
	}
	for _, hidden := range []error{store.ErrNotFound, store.ErrForbidden} {
		recording.getInstallationErr = hidden
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("hidden installation error=%v status=%d body=%s", hidden, response.Code, response.Body.String())
		}
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/not-a-uuid", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid installation id status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstallationConflictExplainsSafeRecoveryWithoutTenantDisclosure(t *testing.T) {
	recording := &recordingStore{createInstallationErr: store.ErrConflict}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/acme/installations",
		strings.NewReader(`{"provider":"github","external_id":"123","repository_scope":"RainLib/*","credential_ref":"github-app"}`),
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	for _, required := range []string{"already connected", "deactivate", "Historical evidence stays"} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatalf("conflict response must explain recovery: %s", response.Body.String())
		}
	}
	for _, forbidden := range []string{"rainlib", "other workspace"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("conflict response must not disclose ownership: %s", response.Body.String())
		}
	}
}

func TestInstallationEndpointComesFromTrustedProviderConfiguration(t *testing.T) {
	recording := &recordingStore{installation: domain.Installation{ID: uuid.New(), Active: true}}
	server := NewWithProviderAPIURLs(
		recording,
		fixedAuthenticator{},
		"",
		"",
		"https://github.example.com/api/v3",
		"https://gitlab.example.com/api/v4",
	)
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/acme/installations",
		strings.NewReader(`{"provider":"GitHub","external_id":"123","repository_scope":"acme/*","api_base_url":"https://attacker.invalid/api","credential_ref":"github-app"}`),
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if recording.installationInput.Provider != domain.ProviderGitHub {
		t.Fatalf("provider=%q, want github", recording.installationInput.Provider)
	}
	if recording.installationInput.APIBaseURL != "https://github.example.com/api/v3" {
		t.Fatalf("api base URL=%q, want trusted GitHub endpoint", recording.installationInput.APIBaseURL)
	}
}

func TestInstallationEndpointAllowsGitHubInstallationWideScopeOnly(t *testing.T) {
	recording := &recordingStore{installation: domain.Installation{ID: uuid.New(), Active: true}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/acme/installations",
		strings.NewReader(`{"provider":"github","external_id":"123","repository_scope":"*/*","credential_ref":"github-app"}`),
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || recording.installationInput.RepositoryScope != domain.AllAuthorizedRepositoriesScope {
		t.Fatalf("GitHub all-authorized scope status=%d scope=%q body=%s", response.Code, recording.installationInput.RepositoryScope, response.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/v1/tenants/acme/installations",
		strings.NewReader(`{"provider":"gitlab","external_id":"123","repository_scope":"*/*","credential_ref":"gitlab-token"}`),
	)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("GitLab installation-wide scope status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProviderProfilesExposeOnlyDeploymentOwnedEndpoints(t *testing.T) {
	server := NewWithProviderAPIURLs(
		&recordingStore{},
		fixedAuthenticator{},
		"",
		"",
		"https://api.github.com",
		"https://gitlab.internal.example/api/v4",
	)
	server.SetGitLabDeploymentTokenAvailable(true)
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/provider-profiles", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "credential_ref") {
		t.Fatalf("provider profiles status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Profiles []domain.ProviderProfile `json:"profiles"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode provider profiles: %v", err)
	}
	if len(body.Profiles) != 2 {
		t.Fatalf("profiles=%#v", body.Profiles)
	}
	profiles := make(map[domain.Provider]domain.ProviderProfile, len(body.Profiles))
	for _, profile := range body.Profiles {
		profiles[profile.Provider] = profile
	}
	github, githubOK := profiles[domain.ProviderGitHub]
	gitlab, gitlabOK := profiles[domain.ProviderGitLab]
	if !githubOK || github.DeploymentTokenAvailable || github.APIBaseURL != "https://api.github.com" {
		t.Fatalf("github profile=%#v", github)
	}
	if !gitlabOK || !gitlab.DeploymentTokenAvailable || gitlab.Mode != "self_managed" || gitlab.APIBaseURL != "https://gitlab.internal.example/api/v4" {
		t.Fatalf("gitlab profile=%#v", gitlab)
	}

	server = NewWithProviderAPIURLs(
		&recordingStore{listInstallationsErr: store.ErrForbidden},
		fixedAuthenticator{},
		"",
		"",
		"https://api.github.com",
		"https://gitlab.com/api/v4",
	)
	mux = http.NewServeMux()
	server.Register(mux)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("hidden provider profile status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstallationVerificationEndpointQueuesProviderProbe(t *testing.T) {
	installationID := uuid.New()
	recording := &recordingStore{}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations/"+installationID.String()+"/verification", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || recording.installationVerificationID != installationID || !strings.Contains(response.Body.String(), `"verification_state":"pending"`) {
		t.Fatalf("status=%d installation=%s body=%s", response.Code, recording.installationVerificationID, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations/not-a-uuid/verification", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid installation id status=%d body=%s", response.Code, response.Body.String())
	}

	recording.installationVerificationErr = store.ErrConflict
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations/"+installationID.String()+"/verification", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("active verification status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstallationRepositoriesEndpointReturnsOnlyNormalizedInventory(t *testing.T) {
	installationID := uuid.New()
	backend := &recordingStore{installationRepositories: []domain.ProviderRepository{{
		ExternalID: "42", Name: "RainLib/open-review-platform", DefaultBranch: "main", Visibility: "private",
	}}}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/"+installationID.String()+"/repositories?limit=25&query=%20%20open-review%20%20", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || backend.installationRepositoryID != installationID || backend.installationRepositoryQuery != "open-review" || !strings.Contains(response.Body.String(), "RainLib/open-review-platform") || strings.Contains(response.Body.String(), "credential_ref") {
		t.Fatalf("status=%d installation=%s body=%s", response.Code, backend.installationRepositoryID, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/"+installationID.String()+"/repositories?query="+strings.Repeat("x", 513), nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized inventory query status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/not-a-uuid/repositories", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid installation id status=%d body=%s", response.Code, response.Body.String())
	}

	backend.installationRepositoryErr = store.ErrForbidden
	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/"+installationID.String()+"/repositories", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("hidden unauthorized installation status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstallationRepositoryScopeEndpointUsesCompareAndSet(t *testing.T) {
	installationID := uuid.New()
	backend := &recordingStore{}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/installations/"+installationID.String(), strings.NewReader(`{"repository_scope":"RainLib/open-review-platform, RainLib/platform-api","expected_repository_scope":"RainLib/*"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || backend.installationScopeID != installationID || backend.installationScopeInput.RepositoryScope != "RainLib/open-review-platform,RainLib/platform-api" || backend.installationScopeInput.ExpectedRepositoryScope != "RainLib/*" {
		t.Fatalf("status=%d scope=%#v installation=%s body=%s", response.Code, backend.installationScopeInput, backend.installationScopeID, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/installations/"+installationID.String(), strings.NewReader(`{"repository_scope":"invalid","expected_repository_scope":"RainLib/*"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope status=%d body=%s", response.Code, response.Body.String())
	}

	backend.installationScopeErr = store.ErrConflict
	request = httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/installations/"+installationID.String(), strings.NewReader(`{"repository_scope":"RainLib/open-review-platform","expected_repository_scope":"RainLib/*"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("scope conflict status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstallationDeactivationStopsFutureAdmission(t *testing.T) {
	installationID := uuid.New()
	recording := &recordingStore{deactivatedInstallation: domain.InstallationSummary{
		ID: installationID, Provider: domain.ProviderGitLab, Active: false,
	}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodDelete, "/v1/tenants/acme/installations/"+installationID.String(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || recording.deactivatedInstallationID != installationID || !strings.Contains(response.Body.String(), `"active":false`) {
		t.Fatalf("status=%d installation=%s body=%s", response.Code, recording.deactivatedInstallationID, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/v1/tenants/acme/installations/not-a-uuid", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid installation id status=%d body=%s", response.Code, response.Body.String())
	}

	recording.deactivateInstallationErr = store.ErrConflict
	request = httptest.NewRequest(http.MethodDelete, "/v1/tenants/acme/installations/"+installationID.String(), nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("already inactive status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstallationWebhookReceiptsEndpointReturnsSafeDeliveryTrail(t *testing.T) {
	installationID, receiptID, runID := uuid.New(), uuid.New(), uuid.New()
	backend := &recordingStore{installationWebhookReceipts: []domain.InstallationWebhookReceipt{{
		ID: receiptID, EventName: "issues", ResourceKind: "issue", Action: "opened", Revision: 1,
		Repository: "RainLib/open-review-platform", ReviewNumber: 9,
		JobID: uuid.New(), JobState: domain.JobSucceeded, RunID: &runID,
	}}}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/"+installationID.String()+"/webhook-receipts?limit=10", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || backend.installationWebhookReceiptID != installationID || !strings.Contains(response.Body.String(), receiptID.String()) || !strings.Contains(response.Body.String(), `"resource_kind":"issue"`) || !strings.Contains(response.Body.String(), `"action":"opened"`) || !strings.Contains(response.Body.String(), `"revision":1`) || strings.Contains(response.Body.String(), "payload") {
		t.Fatalf("status=%d installation=%s body=%s", response.Code, backend.installationWebhookReceiptID, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/not-a-uuid/webhook-receipts", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid installation id status=%d body=%s", response.Code, response.Body.String())
	}

	backend.installationWebhookReceiptErr = store.ErrForbidden
	request = httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/installations/"+installationID.String()+"/webhook-receipts", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("hidden unauthorized installation status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNotificationDestinationResponseDoesNotExposeSecretReference(t *testing.T) {
	recording := &recordingStore{notificationDestination: domain.NotificationDestination{
		ID: uuid.New(), Name: "Release room", Provider: domain.NotificationFeishu, SecretRef: "env:FEISHU_RELEASE", Enabled: true,
	}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/notification-destinations", strings.NewReader(`{"name":"Release room","provider":"FEISHU","secret_ref":"env:FEISHU_RELEASE","enabled":true}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if recording.notificationDestinationInput.Provider != domain.NotificationFeishu {
		t.Fatalf("provider=%q, want feishu", recording.notificationDestinationInput.Provider)
	}
	if strings.Contains(response.Body.String(), "secret_ref") || strings.Contains(response.Body.String(), "FEISHU_RELEASE") {
		t.Fatalf("secret reference must not be serialized: %s", response.Body.String())
	}
}

func TestNotificationRouteAcceptsRepositoryScopedTerminalEvents(t *testing.T) {
	destinationID := uuid.New()
	recording := &recordingStore{notificationRoute: domain.NotificationRoute{ID: uuid.New(), DestinationID: destinationID}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	body := fmt.Sprintf(`{"destination_id":%q,"repository_glob":"RainLib/*","branch_glob":"release/*","event_types":["review.run.failed","review.run.needs_attention"],"min_severity":"high","enabled":true}`, destinationID.String())
	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/notification-routes", strings.NewReader(body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	if recording.notificationRouteInput.RepositoryGlob != "RainLib/*" || recording.notificationRouteInput.BranchGlob != "release/*" || len(recording.notificationRouteInput.EventTypes) != 2 {
		t.Fatalf("unexpected route input: %#v", recording.notificationRouteInput)
	}
}

func TestNotificationRoutePreviewReturnsDeterministicDecisions(t *testing.T) {
	destinationID, routeID := uuid.New(), uuid.New()
	recording := &recordingStore{notificationRoutePreview: domain.NotificationRoutePreview{Matches: []domain.NotificationRoutePreviewMatch{{
		RouteID: routeID, DestinationID: destinationID, DestinationName: "Platform alerts", Disposition: "selected", Reason: "event, repository, branch, and severity match",
	}}}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/notification-routes/preview", strings.NewReader(`{"repository":"RainLib/open-review-platform","target_branch":"main","event_type":"review.run.completed","highest_severity":"high","finding_count":1}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if recording.notificationRoutePreviewInput.Repository != "RainLib/open-review-platform" || recording.notificationRoutePreviewInput.EventType != "review.run.completed" || recording.notificationRoutePreviewInput.FindingCount != 1 {
		t.Fatalf("unexpected preview input: %#v", recording.notificationRoutePreviewInput)
	}
	if !strings.Contains(response.Body.String(), routeID.String()) || !strings.Contains(response.Body.String(), "selected") {
		t.Fatalf("preview response missing decision: %s", response.Body.String())
	}
}

func TestNotificationRouteReorderPersistsTheCompleteOptimisticOrder(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	recording := &recordingStore{notificationRoutes: []domain.NotificationRoute{{ID: second, Priority: 1, Revision: 3}, {ID: first, Priority: 2, Revision: 7}}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	body := fmt.Sprintf(`{"routes":[{"id":%q,"expected_revision":7},{"id":%q,"expected_revision":3}]}`, first.String(), second.String())
	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/notification-routes/reorder", strings.NewReader(body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if len(recording.notificationRouteOrderInput.Routes) != 2 || recording.notificationRouteOrderInput.Routes[0].ID != first || recording.notificationRouteOrderInput.Routes[0].ExpectedRevision != 7 {
		t.Fatalf("unexpected reorder input: %#v", recording.notificationRouteOrderInput)
	}
}

func TestNotificationRouteReorderRejectsStaleRevision(t *testing.T) {
	recording := &recordingStore{reorderNotificationRoutesErr: store.ErrRevisionConflict}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/notification-routes/reorder", strings.NewReader(fmt.Sprintf(`{"routes":[{"id":%q,"expected_revision":1}]}`, uuid.NewString())))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "reload") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNotificationMutationRejectsStaleRevision(t *testing.T) {
	destinationID := uuid.New()
	recording := &recordingStore{updateNotificationDestinationErr: store.ErrRevisionConflict}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/notification-destinations/"+destinationID.String(), strings.NewReader(`{"enabled":false,"expected_revision":1}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "reload") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNotificationTestReturnsAnAsyncReceipt(t *testing.T) {
	destinationID := uuid.New()
	receiptID := uuid.New()
	recording := &recordingStore{notificationTestDelivery: domain.NotificationDeliverySummary{
		ID: receiptID, EventID: uuid.NewString(), EventType: "notification.destination.test", DestinationID: destinationID,
		DestinationName: "Platform alerts", State: "pending",
	}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/notification-destinations/"+destinationID.String()+"/test", strings.NewReader(`{"expected_revision":3}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || recording.notificationTestDestinationID != destinationID || recording.notificationTestInput.ExpectedRevision != 3 {
		t.Fatalf("status=%d destination=%s input=%#v body=%s", response.Code, recording.notificationTestDestinationID, recording.notificationTestInput, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), receiptID.String()) || !strings.Contains(response.Body.String(), "notification.destination.test") {
		t.Fatalf("test receipt missing from response: %s", response.Body.String())
	}
}

func TestFailedNotificationDeliveryCanBeQueuedForRetry(t *testing.T) {
	deliveryID := uuid.New()
	recording := &recordingStore{}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/notification-deliveries/"+deliveryID.String()+"/retry", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), "queued") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAuditEndpointPassesTenantScopedFilters(t *testing.T) {
	before := uuid.New()
	first := uuid.New()
	second := uuid.New()
	recording := &recordingStore{auditEvents: []domain.AuditEvent{{ID: first, Action: "notification_route.updated", ActorSubject: "operator"}, {ID: second}}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/audit-events?actor=operator&action=notification_&target=repo&from=2026-09-26T00:00:00Z&until=2026-09-27T00:00:00Z&limit=1&before="+before.String(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if recording.auditFilter.Actor != "operator" || recording.auditFilter.Action != "notification_" || recording.auditFilter.Target != "repo" || recording.auditFilter.From == nil || !recording.auditFilter.From.Equal(time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)) || recording.auditFilter.Until == nil || !recording.auditFilter.Until.Equal(time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)) || recording.auditFilter.Limit != 1 || recording.auditFilter.Before == nil || *recording.auditFilter.Before != before {
		t.Fatalf("unexpected audit filter: %#v", recording.auditFilter)
	}
	if !strings.Contains(response.Body.String(), `"next_cursor":"`+first.String()+`"`) || strings.Contains(response.Body.String(), second.String()) {
		t.Fatalf("audit page did not trim lookahead or return cursor: %s", response.Body.String())
	}

	bad := httptest.NewRecorder()
	mux.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/audit-events?before=invalid", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid audit cursor status=%d body=%s", bad.Code, bad.Body.String())
	}
	for _, query := range []string{"from=invalid", "until=2026-09-26", "from=2026-09-27T00:00:00Z&until=2026-09-26T00:00:00Z"} {
		invalid := httptest.NewRecorder()
		mux.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/audit-events?"+query, nil))
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("invalid audit query %q status=%d body=%s", query, invalid.Code, invalid.Body.String())
		}
	}
}

func TestAuditEventDetailUsesExactEventIdentity(t *testing.T) {
	id := uuid.New()
	recording := &recordingStore{auditEvent: domain.AuditEvent{ID: id, Action: "review.completed"}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/audit-events/"+id.String(), nil))
	if response.Code != http.StatusOK || recording.auditEventID != id || !strings.Contains(response.Body.String(), "review.completed") {
		t.Fatalf("audit detail status=%d id=%s body=%s", response.Code, recording.auditEventID, response.Body.String())
	}

	recording.getAuditEventErr = store.ErrNotFound
	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/audit-events/"+id.String(), nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing audit detail status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestWorkspaceCannotDemoteItsLastOwner(t *testing.T) {
	recording := &recordingStore{upsertMembershipErr: store.ErrConflict}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/members/operator", strings.NewReader(`{"role":"admin"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "at least one owner") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRunEventStreamReplaysOnlyEventsAfterRequestedRevision(t *testing.T) {
	runID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &recordingStore{runEvents: []domain.RunEvent{
		{ID: uuid.New(), RunID: runID, Revision: 2, EventType: "run.admitted", ActorKind: "worker", Payload: map[string]any{}},
		{ID: uuid.New(), RunID: runID, Revision: 3, EventType: "run.preparing", ActorKind: "worker", Payload: map[string]any{}},
	}}
	backend.listRunEventsHook = func(_ context.Context, after int) {
		if after == 2 {
			cancel()
		}
	}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/runs/"+runID.String()+"/events?after_revision=2", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK || backend.listRunEventsAfter != 2 {
		t.Fatalf("status=%d after=%d body=%s", response.Code, backend.listRunEventsAfter, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "id: 2\n") || !strings.Contains(body, "id: 3\nevent: transition") || !strings.Contains(body, `"event_type":"run.preparing"`) {
		t.Fatalf("expected only revision 3 replay, body=%q", body)
	}
}

func TestRunEventStreamReportsTerminalReadErrors(t *testing.T) {
	runID := uuid.New()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "not found", err: store.ErrNotFound, code: "not_found"},
		{name: "store failure", err: errors.New("database unavailable"), code: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recording := &recordingStore{listRunEventsErr: test.err}
			server := New(recording, fixedAuthenticator{}, "", "")
			mux := http.NewServeMux()
			server.Register(mux)

			request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/runs/"+runID.String()+"/events", nil)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
			}
			if !response.Flushed || !strings.Contains(response.Body.String(), "event: error\ndata: {\"code\":\""+test.code+"\"}") {
				t.Fatalf("expected flushed terminal event %q, got flushed=%v body=%q", test.code, response.Flushed, response.Body.String())
			}
		})
	}
}

func TestWorkspaceInvitationEndpointsKeepTokensOneTimeAndRoleBound(t *testing.T) {
	invitationID := uuid.New()
	backend := &recordingStore{
		workspaceInvitation: domain.WorkspaceInvitationCreation{
			Invitation: domain.WorkspaceInvitation{ID: invitationID, Subject: "invited", Role: "reviewer", Status: "pending"},
			Token:      strings.Repeat("a", 64),
		},
		workspaceInvitations:        []domain.WorkspaceInvitation{{ID: invitationID, Subject: "invited", Role: "reviewer", Status: "pending"}},
		acceptedWorkspaceInvitation: domain.Membership{Subject: "invited", Role: "reviewer"},
	}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	create := httptest.NewRecorder()
	mux.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/invitations", strings.NewReader(`{"subject":"invited","role":"reviewer","expires_in_hours":72}`)))
	if create.Code != http.StatusCreated || backend.workspaceInvitationInput.Subject != "invited" || backend.workspaceInvitationInput.Role != "reviewer" || backend.workspaceInvitationInput.ExpiresInHours != 72 || !strings.Contains(create.Body.String(), `"token"`) {
		t.Fatalf("create status=%d input=%#v body=%s", create.Code, backend.workspaceInvitationInput, create.Body.String())
	}

	list := httptest.NewRecorder()
	mux.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/invitations", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), invitationID.String()) || strings.Contains(list.Body.String(), backend.workspaceInvitation.Token) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}

	accept := httptest.NewRecorder()
	mux.ServeHTTP(accept, httptest.NewRequest(http.MethodPost, "/v1/invitations/accept", strings.NewReader(`{"token":"`+backend.workspaceInvitation.Token+`"}`)))
	if accept.Code != http.StatusOK || backend.acceptedWorkspaceInvitationToken != backend.workspaceInvitation.Token || !strings.Contains(accept.Body.String(), `"reviewer"`) {
		t.Fatalf("accept status=%d token=%q body=%s", accept.Code, backend.acceptedWorkspaceInvitationToken, accept.Body.String())
	}

	revoke := httptest.NewRecorder()
	mux.ServeHTTP(revoke, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/invitations/"+invitationID.String()+"/revoke", nil))
	if revoke.Code != http.StatusOK || backend.revokedWorkspaceInvitationID != invitationID {
		t.Fatalf("revoke status=%d id=%s body=%s", revoke.Code, backend.revokedWorkspaceInvitationID, revoke.Body.String())
	}

	backend.workspaceInvitationErr = store.ErrInvalidInvitation
	invalid := httptest.NewRecorder()
	mux.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/invitations", strings.NewReader(`{}`)))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invitation") {
		t.Fatalf("invalid create status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	backend.workspaceInvitationErr = store.ErrNotFound
	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/tenants/missing/invitations", nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "workspace") {
		t.Fatalf("missing workspace status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestWorkspaceAccessRequestEndpointsDoNotDiscloseWorkspaceAndFenceDecisions(t *testing.T) {
	requestID := uuid.New()
	backend := &recordingStore{
		workspaceAccessRequests: []domain.WorkspaceAccessRequest{{ID: requestID, Subject: "requester", Status: "pending", Revision: 1}},
	}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRecorder()
	mux.ServeHTTP(request, httptest.NewRequest(http.MethodPost, "/v1/workspace-access-requests", strings.NewReader(`{"slug":"acme","note":"Please add me"}`)))
	if request.Code != http.StatusAccepted || request.Body.String() != "{\"status\":\"received\"}\n" || backend.workspaceAccessSlug != "acme" || backend.workspaceAccessNote != "Please add me" {
		t.Fatalf("request status=%d slug=%q note=%q body=%q", request.Code, backend.workspaceAccessSlug, backend.workspaceAccessNote, request.Body.String())
	}
	backend.workspaceAccessErr = store.ErrInvalidAccessRequest
	invalid := httptest.NewRecorder()
	mux.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/v1/workspace-access-requests", strings.NewReader(`{"slug":"invalid"}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid request status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	backend.workspaceAccessErr = nil

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/access-requests", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), requestID.String()) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	backend.workspaceAccessErr = store.ErrForbidden
	hidden := httptest.NewRecorder()
	mux.ServeHTTP(hidden, httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/access-requests", nil))
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("hidden list status=%d body=%s", hidden.Code, hidden.Body.String())
	}
	backend.workspaceAccessErr = nil

	decide := httptest.NewRecorder()
	mux.ServeHTTP(decide, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/access-requests/"+requestID.String()+"/decision", strings.NewReader(`{"decision":"approve","revision":1}`)))
	if decide.Code != http.StatusOK || backend.workspaceAccessDecision != "approve" || backend.workspaceAccessRevision != 1 {
		t.Fatalf("decision status=%d decision=%q revision=%d body=%s", decide.Code, backend.workspaceAccessDecision, backend.workspaceAccessRevision, decide.Body.String())
	}
	backend.workspaceAccessErr = store.ErrRevisionConflict
	conflict := httptest.NewRecorder()
	mux.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/access-requests/"+requestID.String()+"/decision", strings.NewReader(`{"decision":"reject","revision":1}`)))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestMembershipActivationEndpointRequiresExplicitState(t *testing.T) {
	backend := &recordingStore{}
	server := New(backend, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	missingState := httptest.NewRecorder()
	mux.ServeHTTP(missingState, httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/members/teammate/activation", strings.NewReader(`{}`)))
	if missingState.Code != http.StatusBadRequest || !strings.Contains(missingState.Body.String(), "activation") {
		t.Fatalf("missing activation status=%d body=%s", missingState.Code, missingState.Body.String())
	}

	deactivate := httptest.NewRecorder()
	mux.ServeHTTP(deactivate, httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/members/teammate/activation", strings.NewReader(`{"active":false}`)))
	if deactivate.Code != http.StatusOK || backend.membershipActivationSubject != "teammate" || backend.membershipActivationState {
		t.Fatalf("deactivate status=%d subject=%q active=%v body=%s", deactivate.Code, backend.membershipActivationSubject, backend.membershipActivationState, deactivate.Body.String())
	}

	backend.membershipActivationErr = store.ErrConflict
	protected := httptest.NewRecorder()
	mux.ServeHTTP(protected, httptest.NewRequest(http.MethodPatch, "/v1/tenants/acme/members/owner/activation", strings.NewReader(`{"active":false}`)))
	if protected.Code != http.StatusConflict || !strings.Contains(protected.Body.String(), "active owner") {
		t.Fatalf("protected owner status=%d body=%s", protected.Code, protected.Body.String())
	}
}

func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return fmt.Sprintf("sha256=%x", mac.Sum(nil))
}
