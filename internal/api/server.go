package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/governance"
	"github.com/RainLib/open-review-platform/internal/identity"
	"github.com/RainLib/open-review-platform/internal/interaction"
	"github.com/RainLib/open-review-platform/internal/providercredentials"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/RainLib/open-review-platform/internal/webhook"
	"github.com/google/uuid"
)

const maxWebhookBytes = 2 << 20

var repositoryScopeToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)*/(?:[A-Za-z0-9][A-Za-z0-9._-]*|\*)$`)

const (
	defaultGitHubAPIURL = "https://api.github.com"
	defaultGitLabAPIURL = "https://gitlab.com/api/v4"
)

type Server struct {
	store                            store.Store
	auth                             identity.Authenticator
	githubSecret                     string
	gitlabSecret                     string
	providerAuthorizationSecret      string
	gitLabInstallationIdentitySecret string
	gitLabDeploymentTokenAvailable   bool
	providerAPIBaseURLs              map[domain.Provider]string
	now                              func() time.Time
	lookupTXT                        func(context.Context, string) ([]string, error)
	artifactCipher                   *governance.ArtifactCipher
	providerOAuthCipher              *providercredentials.Cipher
	agentTaskAdapterSecret           string
	agentTaskLeaseDuration           time.Duration
}

// SetProviderOAuthCipher enables encrypted GitLab OAuth credential storage.
// It is supplied by the deployment entry point, never by an HTTP request.
func (s *Server) SetProviderOAuthCipher(cipher providercredentials.Cipher) {
	if cipher.Ready() {
		s.providerOAuthCipher = &cipher
	}
}

// SetGitLabDeploymentTokenAvailable marks the deployment-owned GitLab token
// slot as configured without placing that secret in the control API process.
// The flag only enables the Console's server-side handoff; the provider worker
// still resolves the token and a probe must succeed before admission is live.
func (s *Server) SetGitLabDeploymentTokenAvailable(available bool) {
	s.gitLabDeploymentTokenAvailable = available
}

func New(store store.Store, auth identity.Authenticator, githubSecret, gitlabSecret string) *Server {
	return NewWithProviderAPIURLs(store, auth, githubSecret, gitlabSecret, defaultGitHubAPIURL, defaultGitLabAPIURL)
}

// NewWithProviderAPIURLs pins provider API destinations to deployment
// configuration. Installation requests may identify a provider, but may never
// select the endpoint to which runner-side credentials are sent.
func NewWithProviderAPIURLs(store store.Store, auth identity.Authenticator, githubSecret, gitlabSecret, githubAPIURL, gitlabAPIURL string) *Server {
	return NewWithProviderAPIURLsAndProviderAuthorization(
		store,
		auth,
		githubSecret,
		gitlabSecret,
		githubAPIURL,
		gitlabAPIURL,
		"",
		"",
	)
}

// NewWithProviderAPIURLsAndProviderAuthorization adds a control-plane check
// for the short-lived signed callback receipt. Production callers must supply
// the same secret as the Console BFF; the legacy constructor remains available
// to keep isolated API tests and pre-onboarding migrations deterministic.
func NewWithProviderAPIURLsAndProviderAuthorization(store store.Store, auth identity.Authenticator, githubSecret, gitlabSecret, githubAPIURL, gitlabAPIURL, providerAuthorizationSecret, gitLabInstallationIdentitySecret string) *Server {
	return newWithProviderAPIURLsAndProviderAuthorization(store, auth, githubSecret, gitlabSecret, githubAPIURL, gitlabAPIURL, providerAuthorizationSecret, gitLabInstallationIdentitySecret, false)
}

// NewWithProviderAPIURLsAndProviderAuthorizationAndGitLabHTTP permits a
// deployment-owned plaintext GitLab endpoint only for an already validated
// development configuration. Browser or API callers never choose this value.
func NewWithProviderAPIURLsAndProviderAuthorizationAndGitLabHTTP(store store.Store, auth identity.Authenticator, githubSecret, gitlabSecret, githubAPIURL, gitlabAPIURL, providerAuthorizationSecret, gitLabInstallationIdentitySecret string, allowGitLabHTTP bool) *Server {
	return newWithProviderAPIURLsAndProviderAuthorization(store, auth, githubSecret, gitlabSecret, githubAPIURL, gitlabAPIURL, providerAuthorizationSecret, gitLabInstallationIdentitySecret, allowGitLabHTTP)
}

func newWithProviderAPIURLsAndProviderAuthorization(store store.Store, auth identity.Authenticator, githubSecret, gitlabSecret, githubAPIURL, gitlabAPIURL, providerAuthorizationSecret, gitLabInstallationIdentitySecret string, allowGitLabHTTP bool) *Server {
	providerAuthorizationSecret = strings.TrimSpace(providerAuthorizationSecret)
	gitLabInstallationIdentitySecret = strings.TrimSpace(gitLabInstallationIdentitySecret)
	if gitLabInstallationIdentitySecret == "" {
		gitLabInstallationIdentitySecret = providerAuthorizationSecret
	}
	server := &Server{
		store:                            store,
		auth:                             auth,
		githubSecret:                     githubSecret,
		gitlabSecret:                     gitlabSecret,
		providerAuthorizationSecret:      providerAuthorizationSecret,
		gitLabInstallationIdentitySecret: gitLabInstallationIdentitySecret,
		providerAPIBaseURLs: map[domain.Provider]string{
			domain.ProviderGitHub: trustedProviderAPIBaseURL(githubAPIURL),
			domain.ProviderGitLab: trustedGitLabProviderAPIBaseURL(gitlabAPIURL, allowGitLabHTTP),
		},
		now:                    time.Now,
		lookupTXT:              net.DefaultResolver.LookupTXT,
		agentTaskLeaseDuration: 2 * time.Minute,
	}
	if artifactCipher, err := governance.NewArtifactCipher(os.Getenv("DATA_GOVERNANCE_ARTIFACT_KEY")); err == nil {
		server.artifactCipher = &artifactCipher
	}
	return server
}

// SetAgentTaskAdapterCallback enables the HMAC-protected callback endpoint
// used only by a deployment-owned isolated adapter. An empty secret leaves the
// endpoint unavailable; browser/API credentials cannot substitute for it.
func (s *Server) SetAgentTaskAdapterCallback(secret string, lease time.Duration) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 32 || lease < 15*time.Second {
		return
	}
	s.agentTaskAdapterSecret = secret
	s.agentTaskLeaseDuration = lease
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("POST /v1/tenants", s.createTenant)
	mux.HandleFunc("GET /v1/tenants", s.listTenants)
	mux.HandleFunc("PUT /v1/tenants/{slug}/members/{subject}", s.upsertMembership)
	mux.HandleFunc("PATCH /v1/tenants/{slug}/members/{subject}/activation", s.setMembershipActivation)
	mux.HandleFunc("GET /v1/tenants/{slug}/members", s.listMemberships)
	mux.HandleFunc("POST /v1/tenants/{slug}/invitations", s.createWorkspaceInvitation)
	mux.HandleFunc("GET /v1/tenants/{slug}/invitations", s.listWorkspaceInvitations)
	mux.HandleFunc("POST /v1/tenants/{slug}/invitations/{invitationID}/revoke", s.revokeWorkspaceInvitation)
	mux.HandleFunc("POST /v1/invitations/accept", s.acceptWorkspaceInvitation)
	mux.HandleFunc("POST /v1/workspace-access-requests", s.requestWorkspaceAccess)
	mux.HandleFunc("GET /v1/tenants/{slug}/access-requests", s.listWorkspaceAccessRequests)
	mux.HandleFunc("POST /v1/tenants/{slug}/access-requests/{requestID}/decision", s.decideWorkspaceAccessRequest)
	mux.HandleFunc("POST /v1/tenants/{slug}/api-keys", s.createAPIKey)
	mux.HandleFunc("GET /v1/tenants/{slug}/api-keys", s.listAPIKeys)
	mux.HandleFunc("POST /v1/tenants/{slug}/api-keys/{keyID}/revoke", s.revokeAPIKey)
	mux.HandleFunc("POST /v1/tenants/{slug}/cli-reviews", s.createCLIReview)
	mux.HandleFunc("GET /v1/tenants/{slug}/cli-reviews", s.listCLIReviews)
	mux.HandleFunc("GET /v1/tenants/{slug}/cli-reviews/{runID}", s.getCLIReview)
	mux.HandleFunc("GET /v1/tenants/{slug}/cli-reviews/{runID}/evidence", s.getCLIReviewEvidence)
	mux.HandleFunc("POST /v1/tenants/{slug}/cli-reviews/{runID}/cancel", s.cancelCLIReview)
	mux.HandleFunc("GET /v1/tenants/{slug}/sso", s.getSSOOverview)
	mux.HandleFunc("PUT /v1/tenants/{slug}/sso/configuration", s.saveSSOConfiguration)
	mux.HandleFunc("POST /v1/tenants/{slug}/sso/probes", s.requestSSOProbe)
	mux.HandleFunc("POST /v1/tenants/{slug}/sso/domains", s.addSSODomain)
	mux.HandleFunc("POST /v1/tenants/{slug}/sso/domains/{domainID}/verify", s.verifySSODomain)
	mux.HandleFunc("POST /v1/tenants/{slug}/sso/mappings", s.createSSORoleMapping)
	mux.HandleFunc("DELETE /v1/tenants/{slug}/sso/mappings/{mappingID}", s.deleteSSORoleMapping)
	mux.HandleFunc("POST /v1/tenants/{slug}/sso/enforce", s.enforceSSO)
	mux.HandleFunc("POST /v1/tenants/{slug}/sso/suspend", s.suspendSSO)
	mux.HandleFunc("GET /v1/tenants/{slug}/data-governance", s.getDataGovernanceOverview)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/retention-policies", s.createRetentionPolicy)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/retention-policies/{policyID}/decisions", s.decideRetentionPolicy)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/legal-holds", s.createDataLegalHold)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/legal-holds/{holdID}/release", s.releaseDataLegalHold)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/jobs", s.createDataGovernanceJob)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/jobs/{jobID}/decisions", s.decideDataGovernanceJob)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/jobs/{jobID}/cancel", s.cancelDataGovernanceJob)
	mux.HandleFunc("POST /v1/tenants/{slug}/data-governance/jobs/{jobID}/retry", s.retryDataGovernanceJob)
	mux.HandleFunc("GET /v1/tenants/{slug}/data-governance/jobs/{jobID}/artifact", s.downloadDataGovernanceArtifact)
	mux.HandleFunc("GET /v1/tenants/{slug}/platform-health", s.getPlatformHealthOverview)
	mux.HandleFunc("POST /v1/tenants/{slug}/platform-incidents", s.createPlatformIncident)
	mux.HandleFunc("POST /v1/tenants/{slug}/platform-incidents/{incidentID}/resolve", s.resolvePlatformIncident)
	mux.HandleFunc("GET /v1/tenants/{slug}/usage", s.getUsage)
	mux.HandleFunc("GET /v1/tenants/{slug}/usage/export", s.exportUsage)
	mux.HandleFunc("PUT /v1/tenants/{slug}/usage/entitlement", s.updateUsageEntitlement)
	mux.HandleFunc("POST /v1/tenants/{slug}/usage/reconcile", s.reconcileUsage)
	mux.HandleFunc("POST /v1/tenants/{slug}/installations", s.createInstallation)
	mux.HandleFunc("POST /v1/tenants/{slug}/provider-credentials/gitlab-oauth", s.storeGitLabOAuthCredential)
	mux.HandleFunc("GET /v1/tenants/{slug}/installations", s.listInstallations)
	mux.HandleFunc("GET /v1/tenants/{slug}/installations/{installationID}", s.getInstallation)
	mux.HandleFunc("GET /v1/tenants/{slug}/installations/{installationID}/repositories", s.listInstallationRepositories)
	mux.HandleFunc("PATCH /v1/tenants/{slug}/installations/{installationID}", s.updateInstallationRepositoryScope)
	mux.HandleFunc("DELETE /v1/tenants/{slug}/installations/{installationID}", s.deactivateInstallation)
	mux.HandleFunc("GET /v1/tenants/{slug}/provider-profiles", s.listProviderProfiles)
	mux.HandleFunc("GET /v1/tenants/{slug}/installations/{installationID}/webhook-receipts", s.listInstallationWebhookReceipts)
	mux.HandleFunc("POST /v1/tenants/{slug}/installations/{installationID}/verification", s.requestInstallationVerification)
	mux.HandleFunc("GET /v1/tenants/{slug}/setup-checkpoint", s.getWorkspaceSetupCheckpoint)
	mux.HandleFunc("PUT /v1/tenants/{slug}/setup-checkpoint", s.updateWorkspaceSetupCheckpoint)
	mux.HandleFunc("GET /v1/tenants/{slug}/audit-events", s.listAuditEvents)
	mux.HandleFunc("GET /v1/tenants/{slug}/audit-events/{eventID}", s.getAuditEvent)
	mux.HandleFunc("GET /v1/tenants/{slug}/finding-feedback", s.findingFeedbackDashboard)
	mux.HandleFunc("GET /v1/tenants/{slug}/findings", s.listFindings)
	mux.HandleFunc("POST /v1/tenants/{slug}/findings/{findingID}/disposition", s.setFindingDisposition)
	mux.HandleFunc("GET /v1/tenants/{slug}/issues", s.listIssues)
	mux.HandleFunc("GET /v1/tenants/{slug}/issue-views", s.listIssueViews)
	mux.HandleFunc("POST /v1/tenants/{slug}/issue-views", s.createIssueView)
	mux.HandleFunc("PUT /v1/tenants/{slug}/issue-views/{viewID}", s.updateIssueView)
	mux.HandleFunc("DELETE /v1/tenants/{slug}/issue-views/{viewID}", s.deleteIssueView)
	mux.HandleFunc("GET /v1/tenants/{slug}/issues/auto-create-policy", s.getIssueAutoCreatePolicy)
	mux.HandleFunc("PUT /v1/tenants/{slug}/issues/auto-create-policy", s.saveIssueAutoCreatePolicy)
	mux.HandleFunc("POST /v1/tenants/{slug}/issues/auto-create-policy/preview", s.previewIssueAutoCreatePolicy)
	mux.HandleFunc("GET /v1/tenants/{slug}/issues/{issueID}", s.getIssue)
	mux.HandleFunc("PATCH /v1/tenants/{slug}/issues/{issueID}", s.mutateIssue)
	mux.HandleFunc("GET /v1/tenants/{slug}/provider-issues", s.listProviderIssueAnalyses)
	mux.HandleFunc("GET /v1/tenants/{slug}/provider-issues/{analysisID}", s.getProviderIssueAnalysis)
	mux.HandleFunc("POST /v1/tenants/{slug}/provider-issues/{analysisID}/agent-task", s.createAgentTaskFromProviderIssue)
	mux.HandleFunc("POST /v1/tenants/{slug}/provider-issues/{analysisID}/retry", s.retryProviderIssueAnalysis)
	mux.HandleFunc("GET /v1/tenants/{slug}/review-config/{section}", s.getReviewConfig)
	mux.HandleFunc("GET /v1/tenants/{slug}/review-config/{section}/history", s.listReviewConfigVersions)
	mux.HandleFunc("PUT /v1/tenants/{slug}/review-config/{section}", s.saveReviewConfig)
	mux.HandleFunc("DELETE /v1/tenants/{slug}/review-config/{section}", s.restoreInheritedReviewConfig)
	mux.HandleFunc("GET /v1/tenants/{slug}/issue-format-templates", s.listIssueFormatTemplates)
	mux.HandleFunc("POST /v1/tenants/{slug}/issue-format-templates", s.createIssueFormatTemplate)
	mux.HandleFunc("PUT /v1/tenants/{slug}/issue-format-templates/{templateID}", s.updateIssueFormatTemplate)
	mux.HandleFunc("DELETE /v1/tenants/{slug}/issue-format-templates/{templateID}", s.archiveIssueFormatTemplate)
	mux.HandleFunc("GET /v1/tenants/{slug}/review-config-change-requests", s.listReviewConfigChangeRequests)
	mux.HandleFunc("POST /v1/tenants/{slug}/review-config-change-requests", s.requestReviewConfigChange)
	mux.HandleFunc("POST /v1/tenants/{slug}/review-config-change-requests/{requestID}/decision", s.decideReviewConfigChange)
	mux.HandleFunc("GET /v1/tenants/{slug}/model-probes", s.listModelProbes)
	mux.HandleFunc("POST /v1/tenants/{slug}/model-probes", s.requestModelProbe)
	mux.HandleFunc("POST /v1/tenants/{slug}/notification-destinations", s.createNotificationDestination)
	mux.HandleFunc("GET /v1/tenants/{slug}/notification-destinations", s.listNotificationDestinations)
	mux.HandleFunc("PATCH /v1/tenants/{slug}/notification-destinations/{destinationID}", s.updateNotificationDestination)
	mux.HandleFunc("POST /v1/tenants/{slug}/notification-destinations/{destinationID}/test", s.requestNotificationTest)
	mux.HandleFunc("POST /v1/tenants/{slug}/notification-routes", s.createNotificationRoute)
	mux.HandleFunc("GET /v1/tenants/{slug}/notification-routes", s.listNotificationRoutes)
	mux.HandleFunc("POST /v1/tenants/{slug}/notification-routes/reorder", s.reorderNotificationRoutes)
	mux.HandleFunc("POST /v1/tenants/{slug}/notification-routes/preview", s.previewNotificationRoutes)
	mux.HandleFunc("PATCH /v1/tenants/{slug}/notification-routes/{routeID}", s.updateNotificationRoute)
	mux.HandleFunc("GET /v1/tenants/{slug}/notification-deliveries", s.listNotificationDeliveries)
	mux.HandleFunc("POST /v1/tenants/{slug}/notification-deliveries/{deliveryID}/retry", s.retryNotificationDelivery)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-sets", s.createRuleSet)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-sets", s.listRuleSets)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-catalog", s.listRuleCatalog)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-catalog/{catalogID}/installations", s.installRuleCatalogEntry)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-approval-requests", s.listRuleApprovalRequests)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-sets/{ruleSetID}/versions/{version}/impact-preview", s.previewRuleVersionImpact)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-sets/{ruleSetID}/versions/{version}/test-runs", s.createRuleTestRun)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-test-runs", s.listRuleTestRuns)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-sets/{ruleSetID}/versions/{version}/approval-requests", s.requestRuleApproval)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-approval-requests/{requestID}/decisions", s.decideRuleApproval)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-sets/{ruleSetID}/versions/{version}/publish", s.publishRuleVersion)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-bindings", s.createRuleBinding)
	mux.HandleFunc("PATCH /v1/tenants/{slug}/rule-bindings/{bindingID}", s.updateRuleBinding)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-bindings", s.listRuleBindings)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-rollouts", s.createRuleRollout)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-rollouts", s.listRuleRollouts)
	mux.HandleFunc("PATCH /v1/tenants/{slug}/rule-rollouts/{rolloutID}", s.updateRuleRollout)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-rollouts/{rolloutID}/comparisons", s.listRuleRolloutComparisons)
	mux.HandleFunc("GET /v1/tenants/{slug}/agent-task-policies", s.listAgentTaskPolicies)
	mux.HandleFunc("PUT /v1/tenants/{slug}/agent-task-policies", s.saveAgentTaskPolicy)
	mux.HandleFunc("POST /v1/tenants/{slug}/agent-tasks", s.createAgentTask)
	mux.HandleFunc("GET /v1/tenants/{slug}/agent-tasks", s.listAgentTasks)
	mux.HandleFunc("GET /v1/tenants/{slug}/agent-tasks/{taskID}", s.getAgentTask)
	mux.HandleFunc("POST /v1/tenants/{slug}/agent-tasks/{taskID}/retry-source", s.retryAgentTaskSource)
	mux.HandleFunc("POST /v1/tenants/{slug}/agent-tasks/{taskID}/plans", s.createAgentTaskPlan)
	mux.HandleFunc("POST /v1/tenants/{slug}/agent-tasks/{taskID}/plans/{planID}/approve", s.approveAgentTaskPlan)
	mux.HandleFunc("POST /v1/tenants/{slug}/agent-tasks/{taskID}/cancel", s.cancelAgentTask)
	mux.HandleFunc("POST /v1/tenants/{slug}/agent-tasks/{taskID}/acceptance", s.decideAgentTaskAcceptance)
	mux.HandleFunc("POST /v1/tenants/{slug}/agent-tasks/{taskID}/retry-checks", s.retryAgentTaskChecks)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-exceptions", s.createRuleException)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-exceptions", s.listRuleExceptions)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-exceptions/{exceptionID}/decisions", s.decideRuleException)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-exceptions/{exceptionID}/revoke", s.revokeRuleException)
	mux.HandleFunc("PUT /v1/tenants/{slug}/provider-identities/{provider}/{externalID}", s.upsertProviderIdentity)
	mux.HandleFunc("PUT /v1/tenants/{slug}/provider-identities/{provider}/{externalID}/self", s.upsertOwnProviderIdentity)
	mux.HandleFunc("GET /v1/tenants/{slug}/pull-requests", s.listPullRequests)
	mux.HandleFunc("GET /v1/tenants/{slug}/work-queue", s.listWorkQueue)
	mux.HandleFunc("GET /v1/tenants/{slug}/review-schedules", s.listReviewSchedules)
	mux.HandleFunc("GET /v1/tenants/{slug}/review-interventions", s.listReviewInterventions)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs", s.listRuns)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs/{runID}", s.getRun)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs/{runID}/evidence", s.getReviewEvidence)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs/{runID}/rule-snapshot", s.getRuleSnapshot)
	mux.HandleFunc("POST /v1/tenants/{slug}/runs/{runID}/cancel", s.cancelRun)
	mux.HandleFunc("POST /v1/tenants/{slug}/runs/{runID}/retry", s.retryRun)
	mux.HandleFunc("POST /v1/tenants/{slug}/runs/{runID}/acknowledgement/retry", s.retryInteractionResponse)
	mux.HandleFunc("POST /v1/tenants/{slug}/runs/{runID}/intervention/claim", s.claimReviewIntervention)
	mux.HandleFunc("POST /v1/tenants/{slug}/runs/{runID}/intervention/resolve", s.resolveReviewIntervention)
	mux.HandleFunc("POST /v1/tenants/{slug}/runs/{runID}/schedules", s.createReviewSchedule)
	mux.HandleFunc("POST /v1/tenants/{slug}/review-schedules/{scheduleID}/cancel", s.cancelReviewSchedule)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs/{runID}/events", s.streamRunEvents)
	mux.HandleFunc("POST /v1/webhooks/github", s.githubWebhook)
	mux.HandleFunc("POST /v1/webhooks/gitlab", s.gitlabWebhook)
	mux.HandleFunc("POST /v1/agent-adapter/events", s.agentTaskAdapterEvent)
	mux.HandleFunc("POST /v1/agent-adapter/starts", s.agentTaskAdapterStart)
}

type agentTaskAdapterEventStore interface {
	RecordAgentTaskAdapterEvent(context.Context, domain.AgentTaskAdapterEvent, time.Duration) (domain.AgentTaskAttempt, bool, error)
}

type agentTaskAdapterStartStore interface {
	ClaimAgentTaskAdapterStart(context.Context, uuid.UUID, string) error
}

type agentTaskCancellationStore interface {
	CancelAgentTask(context.Context, string, string, uuid.UUID, domain.AgentTaskCancellationInput) (domain.AgentTask, error)
}

type agentTaskAutomaticAdmissionStore interface {
	ProcessAutomaticAgentTask(context.Context, domain.ProviderIssueEvent) (domain.AgentTaskCommandOutcome, error)
}

func (s *Server) agentTaskAdapterStart(w http.ResponseWriter, r *http.Request) {
	if s.agentTaskAdapterSecret == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent adapter start gate is not configured"})
		return
	}
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if !agentadapter.Verify(s.agentTaskAdapterSecret, r.Header.Get(agentadapter.HeaderTimestamp), r.Header.Get(agentadapter.HeaderSignature), body, s.now(), 5*time.Minute) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid agent adapter signature"})
		return
	}
	var input struct {
		AttemptID    uuid.UUID `json:"attempt_id"`
		AdapterJobID string    `json:"adapter_job_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || input.AttemptID == uuid.Nil || strings.TrimSpace(input.AdapterJobID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent adapter start request"})
		return
	}
	backend, ok := s.store.(agentTaskAdapterStartStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent adapter start store is unavailable"})
		return
	}
	err = backend.ClaimAgentTaskAdapterStart(r.Context(), input.AttemptID, input.AdapterJobID)
	if errors.Is(err, store.ErrInvalidAgentTask) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent adapter start request"})
		return
	}
	if errors.Is(err, store.ErrAgentTaskClaimLost) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent adapter start gate is no longer available"})
		return
	}
	if err != nil {
		slog.Error("claim agent adapter start failed", "attempt_id", input.AttemptID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not claim agent adapter start"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentTaskAdapterEvent(w http.ResponseWriter, r *http.Request) {
	if s.agentTaskAdapterSecret == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent adapter callback is not configured"})
		return
	}
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if !agentadapter.Verify(s.agentTaskAdapterSecret, r.Header.Get(agentadapter.HeaderTimestamp), r.Header.Get(agentadapter.HeaderSignature), body, s.now(), 5*time.Minute) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid agent adapter signature"})
		return
	}
	var event domain.AgentTaskAdapterEvent
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !event.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent adapter event"})
		return
	}
	backend, ok := s.store.(agentTaskAdapterEventStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent adapter execution store is unavailable"})
		return
	}
	attempt, duplicate, err := backend.RecordAgentTaskAdapterEvent(r.Context(), event, s.agentTaskLeaseDuration)
	if errors.Is(err, store.ErrInvalidAgentTask) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent adapter event"})
		return
	}
	if errors.Is(err, store.ErrAgentTaskClaimLost) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent attempt lease is no longer active"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent attempt not found"})
		return
	}
	if err != nil {
		slog.Error("record agent adapter event failed", "attempt_id", event.AttemptID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record agent adapter event"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "duplicate": duplicate, "attempt": attempt})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) getSSOOverview(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	overview, err := s.store.GetSSOOverview(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("get SSO overview failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load SSO configuration"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) saveSSOConfiguration(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.SSOConfigurationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	overview, err := s.store.SaveSSOConfiguration(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrInvalidSSO) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "SSO configuration is invalid"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "SSO configuration revision changed"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "suspend enforced SSO before changing its identity provider"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("save SSO configuration failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save SSO configuration"})
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) requestSSOProbe(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	receipt, err := s.store.RequestSSOProbe(r.Context(), principal.Subject, r.PathValue("slug"), request.ExpectedRevision)
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "SSO configuration revision changed"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a probe is already active for this revision"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("request SSO probe failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not request SSO probe"})
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/tenants/%s/sso", url.PathEscape(r.PathValue("slug"))))
	writeJSON(w, http.StatusAccepted, receipt)
}

func (s *Server) addSSODomain(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var request struct {
		Domain string `json:"domain"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	item, err := s.store.AddSSODomain(r.Context(), principal.Subject, r.PathValue("slug"), request.Domain)
	if errors.Is(err, store.ErrInvalidSSO) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "domain is invalid"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "domain is already claimed by a workspace"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("add SSO domain failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not add SSO domain"})
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) verifySSODomain(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	domainID, err := uuid.Parse(r.PathValue("domainID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "domain id is invalid"})
		return
	}
	overview, err := s.store.GetSSOOverview(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load SSO domain"})
		return
	}
	var domainName string
	for _, item := range overview.Domains {
		if item.ID == domainID {
			domainName = item.Domain
			break
		}
	}
	if domainName == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "SSO domain not found"})
		return
	}
	lookupContext, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	observed, lookupErr := s.lookupTXT(lookupContext, "_open-review."+domainName)
	if lookupErr != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "DNS challenge record was not found"})
		return
	}
	item, err := s.store.VerifySSODomain(r.Context(), principal.Subject, r.PathValue("slug"), domainID, observed)
	if errors.Is(err, store.ErrSSODomainUnverified) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "DNS challenge record does not match"})
		return
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "SSO domain not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not verify SSO domain"})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createSSORoleMapping(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.SSORoleMappingInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.CreateSSORoleMapping(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrInvalidSSO) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "SSO role mapping is invalid"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "SSO role mapping already exists"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create SSO role mapping"})
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) deleteSSORoleMapping(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	mappingID, err := uuid.Parse(r.PathValue("mappingID"))
	revision, revisionErr := strconv.Atoi(r.URL.Query().Get("revision"))
	if err != nil || revisionErr != nil || revision < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mapping id or revision is invalid"})
		return
	}
	err = s.store.DeleteSSORoleMapping(r.Context(), principal.Subject, r.PathValue("slug"), mappingID, revision)
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "SSO role mapping revision changed"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete SSO role mapping"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enforceSSO(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.SSOEnforcementInput
	if !decodeJSON(w, r, &input) {
		return
	}
	overview, err := s.store.EnforceSSO(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrInvalidSSO) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "an owner break-glass subject is required"})
		return
	}
	if errors.Is(err, store.ErrSSONotReady) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "SSO configuration is not ready for enforcement"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not enforce SSO"})
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) suspendSSO(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	overview, err := s.store.SuspendSSO(r.Context(), principal.Subject, r.PathValue("slug"), request.ExpectedRevision)
	if errors.Is(err, store.ErrRevisionConflict) || errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "SSO enforcement state or revision changed"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not suspend SSO"})
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) authenticateUser(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return identity.Principal{}, false
	}
	return principal, true
}

func (s *Server) getDataGovernanceOverview(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	overview, err := s.store.GetDataGovernanceOverview(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("get data governance overview failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load data governance"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) createRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.RetentionPolicyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.CreateRetentionPolicy(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if writeDataGovernanceError(w, err) {
		return
	}
	status := http.StatusCreated
	if item.State == "awaiting_approval" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, item)
}

func (s *Server) decideRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	policyID, err := uuid.Parse(r.PathValue("policyID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "retention policy id is invalid"})
		return
	}
	var input domain.GovernanceDecisionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.DecideRetentionPolicy(r.Context(), principal.Subject, r.PathValue("slug"), policyID, input)
	if writeDataGovernanceError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createDataLegalHold(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.DataLegalHoldInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.CreateDataLegalHold(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if writeDataGovernanceError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) releaseDataLegalHold(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	holdID, err := uuid.Parse(r.PathValue("holdID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "legal hold id is invalid"})
		return
	}
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	item, err := s.store.ReleaseDataLegalHold(r.Context(), principal.Subject, r.PathValue("slug"), holdID, request.ExpectedRevision)
	if writeDataGovernanceError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createDataGovernanceJob(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.DataGovernanceJobInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.CreateDataGovernanceJob(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if writeDataGovernanceError(w, err) {
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/tenants/%s/data-governance", url.PathEscape(r.PathValue("slug"))))
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) decideDataGovernanceJob(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	jobID, err := uuid.Parse(r.PathValue("jobID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data governance job id is invalid"})
		return
	}
	var input domain.GovernanceDecisionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.DecideDataGovernanceJob(r.Context(), principal.Subject, r.PathValue("slug"), jobID, input)
	if writeDataGovernanceError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) cancelDataGovernanceJob(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	jobID, err := uuid.Parse(r.PathValue("jobID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data governance job id is invalid"})
		return
	}
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	item, err := s.store.CancelDataGovernanceJob(r.Context(), principal.Subject, r.PathValue("slug"), jobID, request.ExpectedRevision)
	if writeDataGovernanceError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) retryDataGovernanceJob(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	jobID, err := uuid.Parse(r.PathValue("jobID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data governance job id is invalid"})
		return
	}
	var request struct {
		ExpectedRevision int    `json:"expected_revision"`
		IdempotencyKey   string `json:"idempotency_key"`
		Reason           string `json:"reason"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	item, err := s.store.RetryDataGovernanceJob(r.Context(), principal.Subject, r.PathValue("slug"), jobID, request.ExpectedRevision, request.IdempotencyKey, request.Reason)
	if writeDataGovernanceError(w, err) {
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) downloadDataGovernanceArtifact(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	jobID, err := uuid.Parse(r.PathValue("jobID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data governance job id is invalid"})
		return
	}
	if s.artifactCipher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "artifact decryption is not configured on this control-plane replica"})
		return
	}
	artifact, err := s.store.GetDataGovernanceArtifact(r.Context(), principal.Subject, r.PathValue("slug"), jobID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "data governance artifact not found"})
		return
	}
	if errors.Is(err, store.ErrGovernanceArtifactGone) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "data governance artifact has expired or is unavailable"})
		return
	}
	if err != nil {
		slog.Error("load data governance artifact failed", "tenant", r.PathValue("slug"), "job_id", jobID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load data governance artifact"})
		return
	}
	payload, err := s.artifactCipher.Decrypt(artifact)
	if err != nil {
		slog.Error("decrypt data governance artifact failed", "tenant", r.PathValue("slug"), "job_id", jobID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "artifact integrity verification failed"})
		return
	}
	if err := s.store.RecordDataGovernanceArtifactDownload(r.Context(), principal.Subject, r.PathValue("slug"), jobID, artifact.PlaintextSHA256); err != nil {
		slog.Error("audit data governance artifact download failed", "tenant", r.PathValue("slug"), "job_id", jobID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "artifact access could not be audited"})
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", artifact.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func writeDataGovernanceError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrInvalidDataGovernance):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data governance request is invalid"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "data governance revision changed"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a conflicting data governance request already exists"})
	case errors.Is(err, store.ErrLegalHold):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "data is protected by an active legal hold"})
	case errors.Is(err, store.ErrSeparationOfDuties):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "requester cannot approve this operation"})
	case errors.Is(err, store.ErrGovernanceNotCancellable):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "data governance job is no longer cancellable"})
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "data governance resource not found"})
	default:
		slog.Error("data governance mutation failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "data governance operation failed"})
	}
	return true
}

func (s *Server) getPlatformHealthOverview(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	overview, err := s.store.GetPlatformHealthOverview(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("get platform health failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not sample platform health"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) platformIncidentStore() (store.PlatformIncidentStore, bool) {
	incidents, ok := s.store.(store.PlatformIncidentStore)
	return incidents, ok
}

func (s *Server) createPlatformIncident(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	incidents, ok := s.platformIncidentStore()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "incident management is unavailable on this control plane"})
		return
	}
	var input domain.PlatformIncidentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	incident, err := incidents.CreatePlatformIncident(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrInvalidPlatformIncident) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "incident title, scope, affected area, or start time is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	if err != nil {
		slog.Error("create platform incident failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not open platform incident"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, incident)
}

func (s *Server) resolvePlatformIncident(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	incidents, ok := s.platformIncidentStore()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "incident management is unavailable on this control plane"})
		return
	}
	incidentID, err := uuid.Parse(r.PathValue("incidentID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "incident id is invalid"})
		return
	}
	var input domain.PlatformIncidentResolutionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	incident, err := incidents.ResolvePlatformIncident(r.Context(), principal.Subject, r.PathValue("slug"), incidentID, input)
	if errors.Is(err, store.ErrInvalidPlatformIncident) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a current incident revision and a resolution of 3 to 2000 characters are required"})
		return
	}
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "platform incident not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "incident revision is stale; refresh before resolving"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "incident is already resolved"})
		return
	}
	if err != nil {
		slog.Error("resolve platform incident failed", "tenant", r.PathValue("slug"), "incident_id", incidentID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve platform incident"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, incident)
}

func (s *Server) findingFeedbackDashboard(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	dashboard, err := s.store.GetFindingFeedbackDashboard(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("finding feedback dashboard failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load finding feedback"})
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (s *Server) listFindings(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	filter := domain.FindingFilter{
		View:            domain.FindingView(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))),
		Repository:      strings.TrimSpace(r.URL.Query().Get("repository")),
		Query:           strings.TrimSpace(r.URL.Query().Get("q")),
		Cursor:          strings.TrimSpace(r.URL.Query().Get("cursor")),
		CursorDirection: domain.WorkQueueCursorDirection(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("cursor_direction")))),
		Limit:           25,
	}
	if filter.View == "" {
		filter.View = domain.FindingActive
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		filter.Limit = parsed
	}
	if !filter.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "finding filter is invalid"})
		return
	}
	page, err := s.store.ListFindings(r.Context(), principal.Subject, r.PathValue("slug"), filter)
	if errors.Is(err, store.ErrInvalidFindingFilter) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "finding filter is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list findings failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list findings"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getReviewConfig(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	section := domain.ReviewConfigSection(strings.ToLower(strings.TrimSpace(r.PathValue("section"))))
	scopeKind := domain.ReviewConfigScopeKind(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope_kind"))))
	if scopeKind == "" {
		scopeKind = domain.ReviewConfigTenantScope
	}
	scope := domain.ReviewConfigScope{
		Kind: scopeKind, Ref: r.URL.Query().Get("scope_ref"),
		Provider:   domain.Provider(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope_provider")))),
		APIBaseURL: r.URL.Query().Get("scope_api_base_url"),
	}
	view, err := s.store.GetReviewConfig(r.Context(), principal.Subject, r.PathValue("slug"), section, scope)
	if errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration scope or section is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("get review configuration failed", "tenant", r.PathValue("slug"), "section", section, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load review configuration"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listReviewConfigVersions(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	scopeKind := domain.ReviewConfigScopeKind(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope_kind"))))
	if scopeKind == "" {
		scopeKind = domain.ReviewConfigTenantScope
	}
	scope := domain.ReviewConfigScope{
		Kind: scopeKind, Ref: r.URL.Query().Get("scope_ref"),
		Provider:   domain.Provider(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope_provider")))),
		APIBaseURL: r.URL.Query().Get("scope_api_base_url"),
	}
	history, err := s.store.ListReviewConfigVersions(r.Context(), principal.Subject, r.PathValue("slug"), domain.ReviewConfigSection(strings.ToLower(strings.TrimSpace(r.PathValue("section")))), scope, 50)
	if errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration scope or section is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list review configuration versions failed", "tenant", r.PathValue("slug"), "section", r.PathValue("section"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load review configuration history"})
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *Server) saveReviewConfig(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		ScopeKind        domain.ReviewConfigScopeKind `json:"scope_kind"`
		ScopeRef         string                       `json:"scope_ref"`
		ScopeProvider    domain.Provider              `json:"scope_provider"`
		ScopeAPIBaseURL  string                       `json:"scope_api_base_url"`
		ExpectedRevision int                          `json:"expected_revision"`
		Content          json.RawMessage              `json:"content"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	view, err := s.store.SaveReviewConfig(r.Context(), principal.Subject, r.PathValue("slug"), domain.ReviewConfigInput{
		Section:          domain.ReviewConfigSection(strings.ToLower(strings.TrimSpace(r.PathValue("section")))),
		ScopeKind:        request.ScopeKind,
		ScopeRef:         request.ScopeRef,
		ScopeProvider:    request.ScopeProvider,
		ScopeAPIBaseURL:  request.ScopeAPIBaseURL,
		ExpectedRevision: request.ExpectedRevision,
		Content:          request.Content,
	})
	if errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration is invalid"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review configuration revision is stale"})
		return
	}
	if errors.Is(err, store.ErrReviewConfigApprovalRequired) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an existing model route requires an independent approval request"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("save review configuration failed", "tenant", r.PathValue("slug"), "section", r.PathValue("section"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save review configuration"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) restoreInheritedReviewConfig(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	expectedRevision, err := strconv.Atoi(r.URL.Query().Get("expected_revision"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected_revision is invalid"})
		return
	}
	view, err := s.store.RestoreInheritedReviewConfig(r.Context(), principal.Subject, r.PathValue("slug"), domain.ReviewConfigInput{
		Section:          domain.ReviewConfigSection(strings.ToLower(strings.TrimSpace(r.PathValue("section")))),
		ScopeKind:        domain.ReviewConfigScopeKind(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope_kind")))),
		ScopeRef:         r.URL.Query().Get("scope_ref"),
		ScopeProvider:    domain.Provider(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope_provider")))),
		ScopeAPIBaseURL:  r.URL.Query().Get("scope_api_base_url"),
		ExpectedRevision: expectedRevision,
	})
	if errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration restore is invalid"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review configuration revision is stale"})
		return
	}
	if errors.Is(err, store.ErrReviewConfigApprovalRequired) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "restoring an active model override requires an independent approval request"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "repository override was not found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("restore review configuration failed", "tenant", r.PathValue("slug"), "section", r.PathValue("section"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not restore inherited review configuration"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listIssueFormatTemplates(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	items, err := s.store.ListIssueFormatTemplates(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list issue format templates failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load issue format templates"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": items})
}

func (s *Server) createIssueFormatTemplate(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.IssueFormatTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.CreateIssueFormatTemplate(r.Context(), principal.Subject, r.PathValue("slug"), input)
	writeIssueFormatTemplateMutation(w, item, err, http.StatusCreated)
}

func (s *Server) updateIssueFormatTemplate(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	templateID, err := uuid.Parse(r.PathValue("templateID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue format template id is invalid"})
		return
	}
	var input domain.IssueFormatTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.UpdateIssueFormatTemplate(r.Context(), principal.Subject, r.PathValue("slug"), templateID, input)
	writeIssueFormatTemplateMutation(w, item, err, http.StatusOK)
}

func (s *Server) archiveIssueFormatTemplate(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	templateID, err := uuid.Parse(r.PathValue("templateID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue format template id is invalid"})
		return
	}
	expectedRevision, err := strconv.Atoi(r.URL.Query().Get("expected_revision"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected_revision is invalid"})
		return
	}
	err = s.store.ArchiveIssueFormatTemplate(r.Context(), principal.Subject, r.PathValue("slug"), templateID, expectedRevision)
	switch {
	case errors.Is(err, store.ErrInvalidIssueFormatTemplate):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue format template archive is invalid"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "issue format template revision is stale"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "issue format template was not found"})
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule administrator role is required"})
	case err != nil:
		slog.Error("archive issue format template failed", "tenant", r.PathValue("slug"), "template_id", templateID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not archive issue format template"})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeIssueFormatTemplateMutation(w http.ResponseWriter, item domain.IssueFormatTemplate, err error, successStatus int) {
	switch {
	case errors.Is(err, store.ErrInvalidIssueFormatTemplate):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue format template is invalid"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "issue format template revision is stale"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an active issue format template already uses that name"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "issue format template was not found"})
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule administrator role is required"})
	case err != nil:
		slog.Error("issue format template mutation failed", "template_id", item.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save issue format template"})
	default:
		writeJSON(w, successStatus, item)
	}
}

func (s *Server) listReviewConfigChangeRequests(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	items, err := s.store.ListReviewConfigChangeRequests(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrInvalidReviewConfigApproval) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration approval query is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "workspace membership is required"})
		return
	}
	if err != nil {
		slog.Error("list review configuration change requests failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list review configuration change requests"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": items})
}

func (s *Server) requestReviewConfigChange(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.ReviewConfigChangeRequestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.store.RequestReviewConfigChange(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrInvalidReviewConfigApproval) || errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration change request is invalid"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "model route changed; refresh before proposing"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an equivalent or pending model route change already exists"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "active model route was not found; create the initial route directly"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("request review configuration change failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not request review configuration change"})
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) decideReviewConfigChange(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	requestID, err := uuid.Parse(r.PathValue("requestID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration change request id is invalid"})
		return
	}
	var input domain.ReviewConfigChangeDecisionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.store.DecideReviewConfigChange(r.Context(), principal.Subject, r.PathValue("slug"), requestID, input)
	if errors.Is(err, store.ErrInvalidReviewConfigApproval) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "review configuration approval decision is invalid"})
		return
	}
	if errors.Is(err, store.ErrSeparationOfDuties) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "the requester cannot approve this model route change"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "owner or administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review configuration change can no longer be decided"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review configuration change request was not found"})
		return
	}
	if err != nil {
		slog.Error("decide review configuration change failed", "tenant", r.PathValue("slug"), "request", requestID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not decide review configuration change"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listModelProbes(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	scopeKind := domain.ReviewConfigScopeKind(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope_kind"))))
	if scopeKind == "" {
		scopeKind = domain.ReviewConfigTenantScope
	}
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	scope := domain.ReviewConfigScope{Kind: scopeKind, Ref: r.URL.Query().Get("scope_ref"), Provider: domain.Provider(r.URL.Query().Get("scope_provider")), APIBaseURL: r.URL.Query().Get("scope_api_base_url")}
	receipts, err := s.store.ListModelProbes(r.Context(), principal.Subject, r.PathValue("slug"), scope, limit)
	if errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model probe scope is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list model probes failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load model probe history"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"probes": receipts})
}

func (s *Server) requestModelProbe(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.ModelProbeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	receipt, err := s.store.RequestModelProbe(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a saved, enabled model route is required before probing"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "model route changed or is disabled; refresh before probing"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a model probe is already active for this route revision"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "workspace administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("request model probe failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not request model probe"})
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/tenants/%s/model-probes", url.PathEscape(r.PathValue("slug"))))
	writeJSON(w, http.StatusAccepted, receipt)
}

func (s *Server) getIssueAutoCreatePolicy(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	policy, err := s.store.GetIssueAutoCreatePolicy(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "issue auto-create policy not found"})
		return
	}
	if err != nil {
		slog.Error("load issue auto-create policy failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load issue auto-create policy"})
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) saveIssueAutoCreatePolicy(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.IssueAutoCreatePolicy
	if !decodeJSON(w, r, &input) {
		return
	}
	policy, err := s.store.SaveIssueAutoCreatePolicy(r.Context(), principal.Subject, r.PathValue("slug"), input)
	switch {
	case errors.Is(err, store.ErrInvalidIssueAction):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue auto-create policy is invalid"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "issue auto-create policy changed; refresh before saving"})
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "owner or admin role is required"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
	case err != nil:
		slog.Error("save issue auto-create policy failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save issue auto-create policy"})
	default:
		writeJSON(w, http.StatusOK, policy)
	}
}

func (s *Server) previewIssueAutoCreatePolicy(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.IssueAutoCreatePolicy
	if !decodeJSON(w, r, &input) {
		return
	}
	preview, err := s.store.PreviewIssueAutoCreatePolicy(r.Context(), principal.Subject, r.PathValue("slug"), input)
	switch {
	case errors.Is(err, store.ErrInvalidIssueAction):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue auto-create policy is invalid"})
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
	case err != nil:
		slog.Error("preview issue auto-create policy failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not preview issue auto-create policy"})
	default:
		writeJSON(w, http.StatusOK, preview)
	}
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	filter := domain.IssueFilter{
		View:            strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view"))),
		Status:          domain.IssueStatus(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))),
		Severity:        strings.ToLower(strings.TrimSpace(r.URL.Query().Get("severity"))),
		Repository:      strings.TrimSpace(r.URL.Query().Get("repository")),
		Category:        strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category"))),
		Query:           strings.TrimSpace(r.URL.Query().Get("q")),
		Cursor:          strings.TrimSpace(r.URL.Query().Get("cursor")),
		CursorDirection: domain.IssueCursorDirection(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("cursor_direction")))),
		AssigneeSubject: "",
		Limit:           25,
	}
	if value := strings.TrimSpace(r.URL.Query().Get("selected")); value != "" {
		selectedID, parseErr := uuid.Parse(value)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "selected must be an issue ID"})
			return
		}
		filter.SelectedIssueID = &selectedID
	}
	if r.URL.Query().Has("filters") {
		filter.Filters, err = domain.ParseIssueFilterExpression(r.URL.Query().Get("filters"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue filters must be a valid bounded expression"})
			return
		}
	}
	if r.URL.Query().Has("filter_time") {
		parsed, parseErr := time.Parse(time.RFC3339Nano, r.URL.Query().Get("filter_time"))
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filter_time must be an RFC3339 timestamp"})
			return
		}
		filter.FilterTime = &parsed
	}
	if assignee := strings.TrimSpace(r.URL.Query().Get("assignee")); assignee != "" {
		if assignee != "me" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "assignee filter must be me"})
			return
		}
		filter.AssigneeSubject = principal.Subject
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		filter.Limit = parsed
	}
	if value := strings.TrimSpace(r.URL.Query().Get("active")); value != "" {
		parsed, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "active must be true or false"})
			return
		}
		filter.ActiveOnly = parsed
	}
	if value := strings.TrimSpace(r.URL.Query().Get("seen_after")); value != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, value)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "seen_after must be an RFC3339 timestamp"})
			return
		}
		filter.SeenAfter = &parsed
	}
	if !filter.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue filter is invalid"})
		return
	}
	page, err := s.store.ListIssues(r.Context(), principal.Subject, r.PathValue("slug"), filter)
	if errors.Is(err, store.ErrInvalidIssueFilter) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue filter is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list issues failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list issues"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	issueID, err := uuid.Parse(r.PathValue("issueID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue id is invalid"})
		return
	}
	issue, err := s.store.GetIssue(r.Context(), principal.Subject, r.PathValue("slug"), issueID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "issue not found"})
		return
	}
	if err != nil {
		slog.Error("get issue failed", "tenant", r.PathValue("slug"), "issue_id", issueID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load issue"})
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

func (s *Server) listProviderIssueAnalyses(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	filter := domain.ProviderIssueAnalysisFilter{
		State: domain.ProviderIssueAnalysisState(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("state")))),
		Query: strings.TrimSpace(r.URL.Query().Get("q")),
		Limit: 50,
	}
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		filter.Limit, err = strconv.Atoi(value)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	if !filter.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider issue filter is invalid"})
		return
	}
	page, err := s.store.ListProviderIssueAnalyses(r.Context(), principal.Subject, r.PathValue("slug"), filter)
	switch {
	case errors.Is(err, store.ErrInvalidIssueFilter):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider issue filter is invalid"})
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
	case err != nil:
		slog.Error("list provider issue analyses failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list provider issue analyses"})
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, page)
	}
}

func (s *Server) getProviderIssueAnalysis(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	analysisID, err := uuid.Parse(r.PathValue("analysisID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider issue analysis id is invalid"})
		return
	}
	detail, err := s.store.GetProviderIssueAnalysis(r.Context(), principal.Subject, r.PathValue("slug"), analysisID)
	switch {
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "provider issue analysis not found"})
	case err != nil:
		slog.Error("get provider issue analysis failed", "tenant", r.PathValue("slug"), "analysis_id", analysisID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load provider issue analysis"})
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, detail)
	}
}

func (s *Server) createAgentTaskFromProviderIssue(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	analysisID, err := uuid.Parse(r.PathValue("analysisID"))
	if err != nil || analysisID == uuid.Nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider issue analysis id is invalid"})
		return
	}
	var input struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ExpectedRevision < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider issue revision is required"})
		return
	}
	task, err := s.store.CreateAgentTaskFromProviderIssue(r.Context(), principal.Subject, r.PathValue("slug"), analysisID, input.ExpectedRevision)
	switch {
	case errors.Is(err, store.ErrInvalidAgentTask):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task request is invalid"})
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "an authorized agent task role is required"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "provider issue analysis not found"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider issue revision changed; refresh before requesting agent work"})
	case errors.Is(err, store.ErrAgentTaskDisabled):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "manual agent policy is not enabled for this repository"})
	case errors.Is(err, store.ErrUnknownInstallation):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a verified provider installation is required"})
	case errors.Is(err, store.ErrAmbiguousInstallation):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "multiple verified installations cover this repository"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an agent task already exists for this Issue revision"})
	case err != nil:
		slog.Error("request agent task from provider issue failed", "tenant", r.PathValue("slug"), "analysis_id", analysisID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not request agent task"})
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Location", fmt.Sprintf("/v1/tenants/%s/agent-tasks/%s", r.PathValue("slug"), task.ID))
		writeJSON(w, http.StatusCreated, task)
	}
}

func (s *Server) retryProviderIssueAnalysis(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	analysisID, err := uuid.Parse(r.PathValue("analysisID"))
	if err != nil || analysisID == uuid.Nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider issue analysis id is invalid"})
		return
	}
	var input domain.ProviderIssueAnalysisRetryInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.store.RetryProviderIssueAnalysis(r.Context(), principal.Subject, r.PathValue("slug"), analysisID, input)
	switch {
	case errors.Is(err, store.ErrInvalidProviderIssueRetry):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider issue retry request is invalid"})
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "provider issue analysis not found"})
	case errors.Is(err, store.ErrWorkspaceSetupIncomplete):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "workspace setup is incomplete"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider issue revision changed; refresh before retrying"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider issue analysis is not retryable"})
	case err != nil:
		slog.Error("retry provider issue analysis failed", "tenant", r.PathValue("slug"), "analysis_id", analysisID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not retry provider issue analysis"})
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Location", fmt.Sprintf("/v1/tenants/%s/provider-issues/%s", r.PathValue("slug"), analysisID))
		writeJSON(w, http.StatusAccepted, result)
	}
}

func (s *Server) mutateIssue(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	issueID, err := uuid.Parse(r.PathValue("issueID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue id is invalid"})
		return
	}
	var input domain.IssueActionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	issue, err := s.store.MutateIssue(r.Context(), principal.Subject, r.PathValue("slug"), issueID, input)
	switch {
	case errors.Is(err, store.ErrInvalidIssueAction):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "issue action, expected revision, or reason is invalid"})
	case errors.Is(err, store.ErrInvalidIssueAssignee):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "assignee must be a non-billing workspace member"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "issue revision changed; refresh before retrying"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "issue is already in the requested state"})
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "reviewer role is required"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "issue not found"})
	case err != nil:
		slog.Error("mutate issue failed", "tenant", r.PathValue("slug"), "issue_id", issueID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update issue"})
	default:
		writeJSON(w, http.StatusOK, issue)
	}
}

func (s *Server) setFindingDisposition(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	findingID, err := uuid.Parse(r.PathValue("findingID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "finding id is invalid"})
		return
	}
	var input domain.FindingDispositionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	err = s.store.SetFindingDisposition(r.Context(), principal.Subject, r.PathValue("slug"), findingID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "reviewer role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "finding was not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidFindingFeedback) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "disposition must be resolved, wont_fix, or clear"})
		return
	}
	if err != nil {
		slog.Error("set finding disposition failed", "tenant", r.PathValue("slug"), "finding_id", findingID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update finding disposition"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, principal)
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.APIKeyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	creation, err := s.store.CreateAPIKey(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrInvalidAPIKey) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "API key name, scope, repository, or expiry is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("create API key failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create API key"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, creation)
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	keys, err := s.store.ListAPIKeys(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("list API keys failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list API keys"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": keys})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	keyID, err := uuid.Parse(r.PathValue("keyID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "API key id is invalid"})
		return
	}
	key, err := s.store.RevokeAPIKey(r.Context(), principal.Subject, r.PathValue("slug"), keyID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "API key was not found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("revoke API key failed", "tenant", r.PathValue("slug"), "key_id", keyID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not revoke API key"})
		return
	}
	writeJSON(w, http.StatusOK, key)
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

func (s *Server) createTenant(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Slug = strings.ToLower(strings.TrimSpace(request.Slug))
	request.Name = strings.TrimSpace(request.Name)
	if !slugPattern.MatchString(request.Slug) || request.Name == "" || len(request.Name) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug or name is invalid"})
		return
	}
	tenant, err := s.store.CreateTenant(r.Context(), principal.Subject, request.Slug, request.Name)
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "tenant slug already exists"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create tenant"})
		return
	}
	writeJSON(w, http.StatusCreated, tenant)
}

func (s *Server) listTenants(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	tenants, err := s.store.ListTenants(r.Context(), principal.Subject, limit)
	if err != nil {
		slog.Error("list accessible tenants failed", "subject", principal.Subject, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list workspaces"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": tenants})
}

func (s *Server) getUsage(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	dashboard, err := s.store.GetUsageDashboard(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "billing viewer role is required"})
		return
	}
	if err != nil {
		slog.Error("get usage dashboard failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load usage"})
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (s *Server) exportUsage(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	periodStart, ok := domain.NormalizeUsageExportPeriod(r.URL.Query().Get("period_start"), s.now())
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "period_start must be a non-future UTC month in YYYY-MM-01 format"})
		return
	}
	export, err := s.store.ExportUsage(r.Context(), principal.Subject, r.PathValue("slug"), periodStart)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "billing viewer role is required"})
		return
	}
	if errors.Is(err, store.ErrInvalidUsageExport) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "usage export period is invalid"})
		return
	}
	if err != nil {
		slog.Error("export usage failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not export usage"})
		return
	}

	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{
		"row_type", "period_start", "period_end", "repository", "metric",
		"settled", "reserved", "released", "committed", "monthly_limit",
		"soft_warning_percent", "generated_at",
	})
	writeRow := func(rowType, repository string, settled, reserved, released int64) {
		_ = writer.Write([]string{
			rowType,
			export.PeriodStart.UTC().Format("2006-01-02"),
			export.PeriodEnd.UTC().Format("2006-01-02"),
			usageCSVCell(repository),
			"review_run",
			strconv.FormatInt(settled, 10),
			strconv.FormatInt(reserved, 10),
			strconv.FormatInt(released, 10),
			strconv.FormatInt(settled+reserved, 10),
			strconv.FormatInt(export.MonthlyReviewLimit, 10),
			strconv.Itoa(export.SoftWarningPercent),
			export.GeneratedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeRow("summary", "", export.Settled, export.Reserved, export.Released)
	for _, item := range export.Repositories {
		writeRow("repository", item.Repository, item.Settled, item.Reserved, item.Released)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		slog.Error("encode usage export failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not encode usage export"})
		return
	}

	filename := "open-review-usage-" + export.PeriodStart.UTC().Format("2006-01") + ".csv"
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}

func usageCSVCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func (s *Server) updateUsageEntitlement(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.UsageEntitlementInput
	if !decodeJSON(w, r, &input) {
		return
	}
	entitlement, err := s.store.UpdateUsageEntitlement(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrInvalidUsageEntitlement) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "usage entitlement is invalid"})
		return
	}
	if err != nil {
		slog.Error("update usage entitlement failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update usage entitlement"})
		return
	}
	writeJSON(w, http.StatusOK, entitlement)
}

func (s *Server) reconcileUsage(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.UsageReconciliationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	report, err := s.store.ReconcileUsage(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrInvalidUsageReconciliation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "usage reconciliation request is invalid"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "idempotency key was already used for another reconciliation request"})
		return
	}
	if err != nil {
		slog.Error("reconcile usage failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not reconcile usage"})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) createInstallation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.InstallationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Provider = domain.Provider(strings.ToLower(strings.TrimSpace(string(input.Provider))))
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.RepositoryScope = canonicalRepositoryScope(input.RepositoryScope)
	input.AuthorScope = strings.ToLower(strings.TrimSpace(input.AuthorScope))
	if input.AuthorScope == "" {
		input.AuthorScope = "all"
	}
	input.AuthorExternalID = strings.TrimSpace(input.AuthorExternalID)
	input.MinimumSeverity = strings.ToLower(strings.TrimSpace(input.MinimumSeverity))
	if input.AutomaticReviews == nil {
		enabled := true
		input.AutomaticReviews = &enabled
	}
	if input.MinimumSeverity == "" {
		input.MinimumSeverity = "medium"
	}
	input.CredentialRef = strings.TrimSpace(input.CredentialRef)
	input.APIBaseURL = s.providerAPIBaseURLs[input.Provider]
	if input.AuthorScope == "mine" && s.providerAuthorizationSecret == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "author-scoped reviews require signed provider authorization"})
		return
	}
	if s.providerAuthorizationSecret != "" {
		receipt, verified := verifyProviderAuthorizationReceipt(
			r.Header.Get(providerAuthorizationHeader),
			s.providerAuthorizationSecret,
			s.now(),
		)
		if !verified || receipt.Tenant != r.PathValue("slug") || receipt.Provider != input.Provider ||
			(input.AuthorScope == "mine" && (receipt.ActorExternalID == "" || receipt.ActorExternalID != input.AuthorExternalID)) ||
			(input.Provider == domain.ProviderGitHub && receipt.ExternalID != input.ExternalID) ||
			(input.Provider == domain.ProviderGitLab && (input.ExternalID != gitLabInstallationIdentity(s.gitLabInstallationIdentitySecret, receipt.Tenant, input.RepositoryScope) || (receipt.CredentialRef != "" && input.CredentialRef != receipt.CredentialRef) || (receipt.CredentialRef == "" && input.CredentialRef != "gitlab-token"))) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "provider authorization is missing, expired, or does not match this workspace"})
			return
		}
	}
	if !validInstallation(input) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider installation is invalid"})
		return
	}
	installation, err := s.store.CreateInstallation(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		// A provider installation is the durable routing key for inbound
		// webhooks. It may belong to exactly one workspace; do not disclose
		// which workspace owns it, because that would cross a tenant boundary.
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This provider installation is already connected to an Open Review workspace. A source workspace owner must deactivate that connection first; then return here and authorize this workspace again. Historical evidence stays with the source workspace."})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create provider installation"})
		return
	}
	writeJSON(w, http.StatusCreated, installation)
}

type gitLabOAuthCredentialRequest struct {
	AccessToken  string     `json:"access_token"`
	ExpiresAt    *time.Time `json:"expires_at"`
	RefreshToken string     `json:"refresh_token"`
}

func (s *Server) storeGitLabOAuthCredential(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if s.providerOAuthCipher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GitLab OAuth credential storage is not configured for this deployment"})
		return
	}
	credentials, ok := s.store.(store.ProviderOAuthCredentialStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "provider credential storage is unavailable"})
		return
	}
	var request gitLabOAuthCredentialRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.AccessToken = strings.TrimSpace(request.AccessToken)
	request.RefreshToken = strings.TrimSpace(request.RefreshToken)
	if len(request.AccessToken) < 8 || len(request.AccessToken) > 16<<10 || len(request.RefreshToken) > 16<<10 || (request.ExpiresAt != nil && !request.ExpiresAt.After(s.now())) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "GitLab OAuth credential is invalid or expired"})
		return
	}
	reference := "secret://provider/gitlab-oauth/" + uuid.NewString()
	access, err := s.providerOAuthCipher.Seal(reference, request.AccessToken)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not protect GitLab OAuth credential"})
		return
	}
	var refresh []byte
	if request.RefreshToken != "" {
		refresh, err = s.providerOAuthCipher.Seal(reference, request.RefreshToken)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not protect GitLab OAuth credential"})
			return
		}
	}
	_, err = credentials.CreateProviderOAuthCredential(r.Context(), principal.Subject, r.PathValue("slug"), domain.ProviderOAuthCredentialInput{
		CredentialRef: reference, Provider: domain.ProviderGitLab, AccessTokenCiphertext: access, RefreshTokenCiphertext: refresh, ExpiresAt: request.ExpiresAt,
	})
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("store GitLab OAuth credential failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store GitLab OAuth credential"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{"credential_ref": reference})
}

func (s *Server) listInstallations(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	installations, err := s.store.ListInstallations(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list provider installations failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list provider installations"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installations": installations})
}

func (s *Server) getInstallation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	installationID, err := uuid.Parse(r.PathValue("installationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation id is invalid"})
		return
	}
	installation, err := s.store.GetInstallation(r.Context(), principal.Subject, r.PathValue("slug"), installationID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "installation not found"})
		return
	}
	if err != nil {
		slog.Error("get provider installation failed", "tenant", r.PathValue("slug"), "installation_id", installationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not get provider installation"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, installation)
}

func (s *Server) listInstallationRepositories(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	installationID, err := uuid.Parse(r.PathValue("installationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation id is invalid"})
		return
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 500 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 500"})
			return
		}
		limit = parsed
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if len(query) > 512 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is too long"})
		return
	}
	repositories, err := s.store.ListInstallationRepositories(r.Context(), principal.Subject, r.PathValue("slug"), installationID, query, limit)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "installation not found"})
		return
	}
	if err != nil {
		slog.Error("list provider repository inventory failed", "tenant", r.PathValue("slug"), "installation_id", installationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list provider repository inventory"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *Server) updateInstallationRepositoryScope(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	installationID, err := uuid.Parse(r.PathValue("installationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation id is invalid"})
		return
	}
	var input domain.InstallationRepositoryScopeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.RepositoryScope = canonicalRepositoryScope(input.RepositoryScope)
	input.ExpectedRepositoryScope = canonicalRepositoryScope(input.ExpectedRepositoryScope)
	if !validRepositoryScope(input.RepositoryScope) || !validRepositoryScope(input.ExpectedRepositoryScope) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repository scope is invalid"})
		return
	}
	installation, err := s.store.UpdateInstallationRepositoryScope(r.Context(), principal.Subject, r.PathValue("slug"), installationID, input)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "installation not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "repository scope has changed, conflicts with another active installation, or provider verification is running; refresh and retry"})
		return
	}
	if errors.Is(err, store.ErrInvalidProviderIdentity) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repository scope is invalid"})
		return
	}
	if err != nil {
		slog.Error("update installation repository scope failed", "tenant", r.PathValue("slug"), "installation_id", installationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update repository scope"})
		return
	}
	writeJSON(w, http.StatusOK, installation)
}

// deactivateInstallation removes an installation from future admission without
// deleting historical delivery, review, or probe evidence. Only an owner or
// administrator may perform this destructive-to-admission state transition.
func (s *Server) deactivateInstallation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	installationID, err := uuid.Parse(r.PathValue("installationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation id is invalid"})
		return
	}
	installation, err := s.store.DeactivateInstallation(r.Context(), principal.Subject, r.PathValue("slug"), installationID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "installation not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "installation is already inactive"})
		return
	}
	if err != nil {
		slog.Error("deactivate provider installation failed", "tenant", r.PathValue("slug"), "installation_id", installationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not deactivate provider installation"})
		return
	}
	writeJSON(w, http.StatusOK, installation)
}

// listProviderProfiles lets an authenticated workspace member see which
// provider endpoints this deployment has intentionally configured. The
// membership check is separate from the configuration itself so a public page
// cannot enumerate private self-managed GitLab hosts.
func (s *Server) listProviderProfiles(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if _, err := s.store.ListInstallations(r.Context(), principal.Subject, r.PathValue("slug"), 1); err != nil {
		if errors.Is(err, store.ErrForbidden) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}
		slog.Error("authorize provider profile read failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load provider profiles"})
		return
	}
	profiles := []domain.ProviderProfile{
		providerProfile(domain.ProviderGitHub, s.providerAPIBaseURLs[domain.ProviderGitHub], false),
		providerProfile(domain.ProviderGitLab, s.providerAPIBaseURLs[domain.ProviderGitLab], s.gitLabDeploymentTokenAvailable),
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) requestInstallationVerification(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	installationID, err := uuid.Parse(r.PathValue("installationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation id is invalid"})
		return
	}
	installation, err := s.store.RequestInstallationVerification(r.Context(), principal.Subject, r.PathValue("slug"), installationID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "installation not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "installation verification is already running or unavailable"})
		return
	}
	if err != nil {
		slog.Error("request installation verification failed", "tenant", r.PathValue("slug"), "installation_id", installationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue installation verification"})
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/tenants/%s/installations/%s", url.PathEscape(r.PathValue("slug")), installation.ID))
	writeJSON(w, http.StatusAccepted, installation)
}

func (s *Server) listInstallationWebhookReceipts(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	installationID, err := uuid.Parse(r.PathValue("installationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation id is invalid"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	receipts, err := s.store.ListInstallationWebhookReceipts(r.Context(), principal.Subject, r.PathValue("slug"), installationID, limit)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "installation not found"})
		return
	}
	if err != nil {
		slog.Error("list installation webhook receipts failed", "tenant", r.PathValue("slug"), "installation_id", installationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list installation webhook receipts"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipts": receipts})
}

func (s *Server) getWorkspaceSetupCheckpoint(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	checkpoint, err := s.store.GetWorkspaceSetupCheckpoint(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("get workspace setup checkpoint failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load setup checkpoint"})
		return
	}
	writeJSON(w, http.StatusOK, checkpoint)
}

func (s *Server) updateWorkspaceSetupCheckpoint(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		CurrentStep      domain.WorkspaceSetupStep         `json:"current_step"`
		ExpectedRevision int                               `json:"expected_revision"`
		LearningBoundary *domain.WorkspaceLearningBoundary `json:"learning_boundary"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	checkpoint, err := s.store.UpdateWorkspaceSetupCheckpoint(r.Context(), principal.Subject, r.PathValue("slug"), domain.WorkspaceSetupCheckpointInput{
		CurrentStep:      request.CurrentStep,
		ExpectedRevision: request.ExpectedRevision,
		LearningBoundary: request.LearningBoundary,
	})
	if errors.Is(err, store.ErrSetupRepositoryScopeIncomplete) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "select at least one synchronized repository before continuing setup"})
		return
	}
	if errors.Is(err, store.ErrSetupReviewPolicyIncomplete) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "save a versioned merge-blocking threshold before continuing setup"})
		return
	}
	if errors.Is(err, store.ErrInvalidReviewConfig) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "setup step is invalid or requires an active installation"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "setup checkpoint is stale; refresh before continuing"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "workspace owner or admin role is required"})
		return
	}
	if err != nil {
		slog.Error("update workspace setup checkpoint failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save setup checkpoint"})
		return
	}
	writeJSON(w, http.StatusOK, checkpoint)
}

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	filter := domain.AuditFilter{
		Actor:  r.URL.Query().Get("actor"),
		Action: r.URL.Query().Get("action"),
		Target: r.URL.Query().Get("target"),
		Limit:  50,
	}
	if len(filter.Actor) > 240 || len(filter.Action) > 120 || len(filter.Target) > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "audit filter is too long"})
		return
	}
	for _, bound := range []struct {
		name string
		into **time.Time
	}{{"from", &filter.From}, {"until", &filter.Until}} {
		if value := r.URL.Query().Get(bound.name); value != "" {
			parsed, parseErr := time.Parse(time.RFC3339, value)
			if parseErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": bound.name + " must be an RFC3339 timestamp"})
				return
			}
			utc := parsed.UTC()
			*bound.into = &utc
		}
	}
	if filter.From != nil && filter.Until != nil && !filter.From.Before(*filter.Until) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must precede until"})
		return
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		filter.Limit, err = strconv.Atoi(value)
		if err != nil || filter.Limit < 1 || filter.Limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	if value := r.URL.Query().Get("before"); value != "" {
		before, parseErr := uuid.Parse(value)
		if parseErr != nil || before == uuid.Nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before must be an audit event ID"})
			return
		}
		filter.Before = &before
	}
	events, err := s.store.ListAuditEvents(r.Context(), principal.Subject, r.PathValue("slug"), filter)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "audit viewer role is required"})
		return
	}
	if errors.Is(err, store.ErrInvalidAuditFilter) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "audit filter is invalid"})
		return
	}
	if err != nil {
		slog.Error("list audit events failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list audit events"})
		return
	}
	var nextCursor string
	if len(events) > filter.Limit {
		events = events[:filter.Limit]
		nextCursor = events[len(events)-1].ID.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_events": events, "next_cursor": nextCursor})
}

func (s *Server) getAuditEvent(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	eventID, err := uuid.Parse(r.PathValue("eventID"))
	if err != nil || eventID == uuid.Nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event ID is invalid"})
		return
	}
	event, err := s.store.GetAuditEvent(r.Context(), principal.Subject, r.PathValue("slug"), eventID)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "audit viewer role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "audit event was not found"})
		return
	}
	if err != nil {
		slog.Error("get audit event failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not get audit event"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_event": event})
}

func (s *Server) createNotificationDestination(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.NotificationDestinationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Provider = domain.NotificationProvider(strings.ToLower(strings.TrimSpace(string(input.Provider))))
	destination, err := s.store.CreateNotificationDestination(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "notification destination already exists"})
		return
	}
	if errors.Is(err, store.ErrInvalidNotification) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification destination is invalid"})
		return
	}
	if err != nil {
		slog.Error("create notification destination failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create notification destination"})
		return
	}
	writeJSON(w, http.StatusCreated, destination)
}

func (s *Server) listNotificationDestinations(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	destinations, err := s.store.ListNotificationDestinations(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list notification destinations"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification_destinations": destinations})
}

func (s *Server) createNotificationRoute(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.NotificationRouteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	route, err := s.store.CreateNotificationRoute(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification destination was not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidNotification) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification route is invalid"})
		return
	}
	if err != nil {
		slog.Error("create notification route failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create notification route"})
		return
	}
	writeJSON(w, http.StatusCreated, route)
}

func (s *Server) listNotificationRoutes(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	routes, err := s.store.ListNotificationRoutes(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list notification routes"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification_routes": routes})
}

func (s *Server) reorderNotificationRoutes(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.NotificationRouteReorderInput
	if !decodeJSON(w, r, &input) {
		return
	}
	routes, err := s.store.ReorderNotificationRoutes(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "notification routes changed; reload before reordering"})
		return
	}
	if errors.Is(err, store.ErrInvalidNotification) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification route order is invalid"})
		return
	}
	if err != nil {
		slog.Error("reorder notification routes failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not reorder notification routes"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification_routes": routes})
}

func (s *Server) previewNotificationRoutes(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.NotificationRoutePreviewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	preview, err := s.store.PreviewNotificationRoutes(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidNotification) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification route preview is invalid"})
		return
	}
	if err != nil {
		slog.Error("preview notification routes failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not preview notification routing"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification_route_preview": preview})
}

func (s *Server) updateNotificationDestination(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	destinationID, err := uuid.Parse(r.PathValue("destinationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification destination id is invalid"})
		return
	}
	var input domain.NotificationDestinationUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	destination, err := s.store.UpdateNotificationDestination(r.Context(), principal.Subject, r.PathValue("slug"), destinationID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification destination was not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) || errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "notification destination changed; reload before updating"})
		return
	}
	if errors.Is(err, store.ErrInvalidNotification) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification destination update is invalid"})
		return
	}
	if err != nil {
		slog.Error("update notification destination failed", "tenant", r.PathValue("slug"), "destination_id", destinationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update notification destination"})
		return
	}
	writeJSON(w, http.StatusOK, destination)
}

func (s *Server) requestNotificationTest(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	destinationID, err := uuid.Parse(r.PathValue("destinationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification destination id is invalid"})
		return
	}
	var input domain.NotificationTestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	receipt, err := s.store.RequestNotificationTest(r.Context(), principal.Subject, r.PathValue("slug"), destinationID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification destination was not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "notification destination changed; reload before testing"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only an enabled destination can be tested"})
		return
	}
	if errors.Is(err, store.ErrInvalidNotification) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification test request is invalid"})
		return
	}
	if err != nil {
		slog.Error("request notification test failed", "tenant", r.PathValue("slug"), "destination_id", destinationID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue notification test"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"notification_delivery": receipt})
}

func (s *Server) updateNotificationRoute(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	routeID, err := uuid.Parse(r.PathValue("routeID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification route id is invalid"})
		return
	}
	var input domain.NotificationRouteUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	route, err := s.store.UpdateNotificationRoute(r.Context(), principal.Subject, r.PathValue("slug"), routeID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "notification route was not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "notification route changed; reload before updating"})
		return
	}
	if errors.Is(err, store.ErrInvalidNotification) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification route update is invalid"})
		return
	}
	if err != nil {
		slog.Error("update notification route failed", "tenant", r.PathValue("slug"), "route_id", routeID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update notification route"})
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	deliveries, err := s.store.ListNotificationDeliveries(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list notification deliveries failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list notification deliveries"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification_deliveries": deliveries})
}

func (s *Server) retryNotificationDelivery(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	deliveryID, err := uuid.Parse(r.PathValue("deliveryID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notification delivery id is invalid"})
		return
	}
	err = s.store.RetryNotificationDelivery(r.Context(), principal.Subject, r.PathValue("slug"), deliveryID)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only a failed delivery to an enabled destination can be retried"})
		return
	}
	if err != nil {
		slog.Error("retry notification delivery failed", "tenant", r.PathValue("slug"), "delivery_id", deliveryID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not retry notification delivery"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (s *Server) createRuleSet(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.RuleSetInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.store.CreateRuleSet(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "rule set name already exists"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleSet) {
		slog.Warn("rejecting invalid rule set", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule set is invalid"})
		return
	}
	if err != nil {
		slog.Error("create rule set failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create rule set"})
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) listRuleSets(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	sets, err := s.store.ListRuleSets(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule sets"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule_sets": sets})
}

func (s *Server) listRuleCatalog(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	entries, err := s.store.ListRuleCatalog(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list rule catalog failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule catalog"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) installRuleCatalogEntry(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.RuleCatalogInstallInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.store.InstallRuleCatalogEntry(r.Context(), principal.Subject, r.PathValue("slug"), r.PathValue("catalogID"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a rule set already uses this catalog title"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleCatalog) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "catalog entry version or content digest is unavailable"})
		return
	}
	if err != nil {
		slog.Error("install rule catalog entry failed", "tenant", r.PathValue("slug"), "catalog_id", r.PathValue("catalogID"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not install rule catalog entry"})
		return
	}
	if result.Replayed {
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) publishRuleVersion(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ruleSetID, err := uuid.Parse(r.PathValue("ruleSetID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule set id is invalid"})
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule version is invalid"})
		return
	}
	published, err := s.store.PublishRuleVersion(r.Context(), principal.Subject, r.PathValue("slug"), ruleSetID, version)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "draft rule version was not found"})
		return
	}
	if err != nil {
		slog.Error("publish rule version failed", "tenant", r.PathValue("slug"), "rule_set_id", ruleSetID, "version", version, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not publish rule version"})
		return
	}
	writeJSON(w, http.StatusOK, published)
}

func (s *Server) listRuleApprovalRequests(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	requests, err := s.store.ListRuleApprovalRequests(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list rule approval requests failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule approval requests"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval_requests": requests})
}

func (s *Server) previewRuleVersionImpact(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ruleSetID, err := uuid.Parse(r.PathValue("ruleSetID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule set id is invalid"})
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule version is invalid"})
		return
	}
	var input domain.RuleImpactPreviewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	preview, err := s.store.PreviewRuleVersionImpact(r.Context(), principal.Subject, r.PathValue("slug"), ruleSetID, version, input)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule version was not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleImpact) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repository, target branch, or precedence is invalid"})
		return
	}
	if err != nil {
		slog.Error("preview rule version impact failed", "tenant", r.PathValue("slug"), "rule_set_id", ruleSetID, "version", version, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not preview rule impact"})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) createRuleException(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.RuleExceptionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	exception, err := s.store.CreateRuleException(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if !handleRuleExceptionError(w, r, err, "create") {
		return
	}
	writeJSON(w, http.StatusCreated, exception)
}

func (s *Server) listRuleExceptions(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	exceptions, err := s.store.ListRuleExceptions(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list rule exceptions failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule exceptions"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exceptions": exceptions})
}

func (s *Server) decideRuleException(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	exceptionID, err := uuid.Parse(r.PathValue("exceptionID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule exception id is invalid"})
		return
	}
	var input domain.RuleExceptionDecisionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	exception, err := s.store.DecideRuleException(r.Context(), principal.Subject, r.PathValue("slug"), exceptionID, input)
	if !handleRuleExceptionError(w, r, err, "decide") {
		return
	}
	writeJSON(w, http.StatusOK, exception)
}

func (s *Server) revokeRuleException(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	exceptionID, err := uuid.Parse(r.PathValue("exceptionID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule exception id is invalid"})
		return
	}
	exception, err := s.store.RevokeRuleException(r.Context(), principal.Subject, r.PathValue("slug"), exceptionID)
	if !handleRuleExceptionError(w, r, err, "revoke") {
		return
	}
	writeJSON(w, http.StatusOK, exception)
}

func handleRuleExceptionError(w http.ResponseWriter, r *http.Request, err error, operation string) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule management permission is required"})
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule exception or version was not found"})
		return false
	}
	if errors.Is(err, store.ErrInvalidRuleException) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule exception request or transition is invalid"})
		return false
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the source issue changed after the exception was requested; create a new request from the current revision"})
		return false
	}
	slog.Error(operation+" rule exception failed", "tenant", r.PathValue("slug"), "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not " + operation + " rule exception"})
	return false
}

func (s *Server) createRuleTestRun(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ruleSetID, err := uuid.Parse(r.PathValue("ruleSetID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule set id is invalid"})
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule version is invalid"})
		return
	}
	var input domain.RuleTestRunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	testRun, err := s.store.CreateRuleTestRun(r.Context(), principal.Subject, r.PathValue("slug"), ruleSetID, version, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule management permission is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule version or eligible source run was not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleImpact) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the candidate could not be compiled for an isolated test"})
		return
	}
	if err != nil {
		slog.Error("create rule test run failed", "tenant", r.PathValue("slug"), "rule_set_id", ruleSetID, "version", version, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create rule test run"})
		return
	}
	writeJSON(w, http.StatusAccepted, testRun)
}

func (s *Server) listRuleTestRuns(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	runs, err := s.store.ListRuleTestRuns(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list rule test runs failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule test runs"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"test_runs": runs})
}

func (s *Server) requestRuleApproval(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ruleSetID, err := uuid.Parse(r.PathValue("ruleSetID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule set id is invalid"})
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule version is invalid"})
		return
	}
	var input domain.RuleApprovalRequestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	request, err := s.store.RequestRuleApproval(r.Context(), principal.Subject, r.PathValue("slug"), ruleSetID, version, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "draft rule version was not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an approval request already exists for this rule version"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleApproval) {
		slog.Warn("rejecting rule approval request", "tenant", r.PathValue("slug"), "rule_set_id", ruleSetID, "version", version, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule approval request is invalid"})
		return
	}
	if err != nil {
		slog.Error("request rule approval failed", "tenant", r.PathValue("slug"), "rule_set_id", ruleSetID, "version", version, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not request rule approval"})
		return
	}
	writeJSON(w, http.StatusCreated, request)
}

func (s *Server) decideRuleApproval(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	requestID, err := uuid.Parse(r.PathValue("requestID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "approval request id is invalid"})
		return
	}
	var input domain.RuleApprovalDecisionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	request, err := s.store.DecideRuleApproval(r.Context(), principal.Subject, r.PathValue("slug"), requestID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "an independent rule administrator is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "approval request was not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "approval request is no longer pending or was already decided"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleApproval) {
		slog.Warn("rejecting rule approval decision", "tenant", r.PathValue("slug"), "request_id", requestID, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule approval decision is invalid"})
		return
	}
	if err != nil {
		slog.Error("decide rule approval failed", "tenant", r.PathValue("slug"), "request_id", requestID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not decide rule approval"})
		return
	}
	writeJSON(w, http.StatusOK, request)
}

func (s *Server) createRuleBinding(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.RuleBindingInput
	if !decodeJSON(w, r, &input) {
		return
	}
	binding, err := s.store.CreateRuleBinding(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "published rule version was not found"})
		return
	}
	if errors.Is(err, store.ErrRuleRolloutRequired) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an active baseline already uses this scope; create a matching Shadow candidate and use the governed rollout"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleBinding) {
		slog.Warn("rejecting invalid rule binding", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule binding is invalid"})
		return
	}
	if err != nil {
		slog.Error("create rule binding failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create rule binding"})
		return
	}
	writeJSON(w, http.StatusCreated, binding)
}

func (s *Server) updateRuleBinding(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	bindingID, err := uuid.Parse(r.PathValue("bindingID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule binding id is invalid"})
		return
	}
	var input domain.RuleBindingUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	binding, err := s.store.UpdateRuleBinding(r.Context(), principal.Subject, r.PathValue("slug"), bindingID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "rule administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule binding was not found"})
		return
	}
	if errors.Is(err, store.ErrRuleRolloutRequired) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this candidate requires a governed Shadow and Canary rollout; direct activation is unavailable"})
		return
	}
	if errors.Is(err, store.ErrActiveRuleRollout) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pause and roll back the active Canary before changing either binding"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleBinding) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule binding state is invalid"})
		return
	}
	if err != nil {
		slog.Error("update rule binding failed", "tenant", r.PathValue("slug"), "binding_id", bindingID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update rule binding"})
		return
	}
	writeJSON(w, http.StatusOK, binding)
}

func (s *Server) listRuleBindings(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	bindings, err := s.store.ListRuleBindings(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule bindings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule_bindings": bindings})
}

func (s *Server) listAgentTaskPolicies(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	provider, apiBaseURL, repository := query.Get("provider"), query.Get("api_base_url"), query.Get("repository")
	if provider != "" || apiBaseURL != "" || repository != "" {
		if !domain.Provider(provider).Valid() || apiBaseURL == "" || repository == "" || len(apiBaseURL) > 2048 || len(repository) > 512 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exact policy lookup requires provider, api_base_url and repository"})
			return
		}
		item, err := s.store.GetAgentTaskPolicy(r.Context(), principal.Subject, r.PathValue("slug"), domain.Provider(provider), apiBaseURL, repository)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"agent_task_policies": []domain.AgentTaskPolicy{}})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}
		if errors.Is(err, store.ErrInvalidAgentTask) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exact policy identity is invalid"})
			return
		}
		if err != nil {
			slog.Error("get agent task policy failed", "tenant", r.PathValue("slug"), "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not get agent task policy"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent_task_policies": []domain.AgentTaskPolicy{item}})
		return
	}
	limit, ok := parseAgentTaskLimit(w, r)
	if !ok {
		return
	}
	items, err := s.store.ListAgentTaskPolicies(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list agent task policies failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list agent task policies"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_task_policies": items})
}

func (s *Server) saveAgentTaskPolicy(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.AgentTaskPolicyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.SaveAgentTaskPolicy(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role and an authorized repository are required"})
		return
	}
	if errors.Is(err, store.ErrUnknownInstallation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a verified provider installation is required"})
		return
	}
	if errors.Is(err, store.ErrAmbiguousInstallation) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "multiple verified provider installations cover this repository; resolve their overlapping scopes first"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent task policy revision changed"})
		return
	}
	if errors.Is(err, store.ErrInvalidAgentTask) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task policy is invalid"})
		return
	}
	if err != nil {
		slog.Error("save agent task policy failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save agent task policy"})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createAgentTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.AgentTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	task, err := s.store.CreateAgentTask(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "an authorized repository and agent task role are required"})
		return
	}
	if errors.Is(err, store.ErrUnknownInstallation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a verified provider installation is required"})
		return
	}
	if errors.Is(err, store.ErrAmbiguousInstallation) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "multiple verified provider installations cover this repository; resolve their overlapping scopes first"})
		return
	}
	if errors.Is(err, store.ErrAgentTaskDisabled) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent task automation is disabled; enable manual mode for this repository first"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a task already exists for this exact source revision"})
		return
	}
	if errors.Is(err, store.ErrInvalidAgentTask) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task request is invalid"})
		return
	}
	if err != nil {
		slog.Error("create agent task failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create agent task"})
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) listAgentTasks(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	limit, ok := parseAgentTaskLimit(w, r)
	if !ok {
		return
	}
	before := uuid.Nil
	if values, present := r.URL.Query()["cursor"]; present {
		if len(values) != 1 || values[0] == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task cursor is invalid"})
			return
		}
		var err error
		before, err = uuid.Parse(values[0])
		if err != nil || before == uuid.Nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task cursor is invalid"})
			return
		}
	}
	page, err := s.store.ListAgentTasks(r.Context(), principal.Subject, r.PathValue("slug"), limit, before)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent task page was not found"})
		return
	}
	if err != nil {
		slog.Error("list agent tasks failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list agent tasks"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getAgentTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(r.PathValue("taskID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task id is invalid"})
		return
	}
	detail, err := s.store.GetAgentTask(r.Context(), principal.Subject, r.PathValue("slug"), taskID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent task was not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidAgentTask) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task id is invalid"})
		return
	}
	if err != nil {
		slog.Error("get agent task failed", "tenant", r.PathValue("slug"), "task_id", taskID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load agent task"})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) retryAgentTaskSource(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(r.PathValue("taskID"))
	if err != nil || taskID == uuid.Nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task id is invalid"})
		return
	}
	var input struct {
		Revision int `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	task, err := s.store.RetryAgentTaskSource(r.Context(), principal.Subject, r.PathValue("slug"), taskID, input.Revision)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "agent task role and authorized repository are required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent task was not found"})
		return
	}
	if errors.Is(err, store.ErrUnknownInstallation) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider installation is no longer verified and active"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) || errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "source retry requires the current revision of a failed, never-planned task"})
		return
	}
	if errors.Is(err, store.ErrInvalidAgentTask) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source retry request is invalid"})
		return
	}
	if err != nil {
		slog.Error("retry agent task source failed", "tenant", r.PathValue("slug"), "task_id", taskID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not retry agent task source"})
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) createAgentTaskPlan(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(r.PathValue("taskID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task id is invalid"})
		return
	}
	var input domain.AgentTaskPlanInput
	if !decodeJSONLimit(w, r, &input, 1<<20) {
		return
	}
	plan, err := s.store.CreateAgentTaskPlan(r.Context(), principal.Subject, r.PathValue("slug"), taskID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "agent task role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent task was not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidAgentTaskPlan) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task plan is invalid or task is no longer plannable"})
		return
	}
	if err != nil {
		slog.Error("create agent task plan failed", "tenant", r.PathValue("slug"), "task_id", taskID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create agent task plan"})
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (s *Server) approveAgentTaskPlan(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(r.PathValue("taskID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task id is invalid"})
		return
	}
	planID, err := uuid.Parse(r.PathValue("planID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task plan id is invalid"})
		return
	}
	var input domain.AgentTaskPlanApprovalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	plan, err := s.store.ApproveAgentTaskPlan(r.Context(), principal.Subject, r.PathValue("slug"), taskID, planID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent task was not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent task plan revision or state changed"})
		return
	}
	if errors.Is(err, store.ErrInvalidAgentTaskPlan) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task plan approval is invalid"})
		return
	}
	if err != nil {
		slog.Error("approve agent task plan failed", "tenant", r.PathValue("slug"), "task_id", taskID, "plan_id", planID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not approve agent task plan"})
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) cancelAgentTask(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	taskID, err := uuid.Parse(r.PathValue("taskID"))
	if err != nil || taskID == uuid.Nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task id is invalid"})
		return
	}
	var input domain.AgentTaskCancellationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	backend, ok := s.store.(agentTaskCancellationStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent task cancellation is unavailable"})
		return
	}
	task, err := backend.CancelAgentTask(r.Context(), principal.Subject, r.PathValue("slug"), taskID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "agent task role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent task was not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent task state or revision changed"})
		return
	}
	if errors.Is(err, store.ErrInvalidAgentTask) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent task cancellation is invalid"})
		return
	}
	if err != nil {
		slog.Error("cancel agent task failed", "tenant", r.PathValue("slug"), "task_id", taskID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not cancel agent task"})
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func parseAgentTaskLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func (s *Server) createRuleRollout(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.RuleRolloutInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rollout, err := s.store.CreateRuleRollout(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule binding was not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an active rollout already uses this candidate binding"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleRollout) {
		message := "rule rollout input is invalid"
		if input.Mode == "canary" {
			message = "Canary requires a completed Shadow comparison, a different owner or administrator, and an initial 1% or 5% cohort"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleBinding) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule rollout must pair an active baseline with a matching shadow candidate"})
		return
	}
	if err == nil {
		writeJSON(w, http.StatusCreated, rollout)
		return
	}
	slog.Error("create rule rollout failed", "tenant", r.PathValue("slug"), "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create rule rollout"})
}

func (s *Server) listRuleRollouts(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	rollouts, err := s.store.ListRuleRollouts(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list rule rollouts failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule rollouts"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule_rollouts": rollouts})
}

func (s *Server) updateRuleRollout(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	rolloutID, err := uuid.Parse(r.PathValue("rolloutID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule rollout id is invalid"})
		return
	}
	var input domain.RuleRolloutUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rollout, err := s.store.UpdateRuleRollout(r.Context(), principal.Subject, r.PathValue("slug"), rolloutID, input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule rollout was not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "rule rollout revision or state is no longer current"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleRollout) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule rollout transition is invalid; Canary advances through 1%, 5%, 25% and 100% before independent promotion"})
		return
	}
	if errors.Is(err, store.ErrRuleRolloutEvidence) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Canary observation is incomplete: wait for the configured window, a candidate review with a passing merge gate, and zero failures or in-flight candidate reviews"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleBinding) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the baseline and candidate bindings no longer match the approved rollout"})
		return
	}
	if err == nil {
		writeJSON(w, http.StatusOK, rollout)
		return
	}
	slog.Error("update rule rollout failed", "tenant", r.PathValue("slug"), "rollout_id", rolloutID, "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update rule rollout"})
}

func (s *Server) listRuleRolloutComparisons(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	rolloutID, err := uuid.Parse(r.PathValue("rolloutID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule rollout id is invalid"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	comparisons, err := s.store.ListRuleRolloutComparisons(r.Context(), principal.Subject, r.PathValue("slug"), rolloutID, limit)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule rollout was not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidRuleRollout) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule rollout comparison request is invalid"})
		return
	}
	if err != nil {
		slog.Error("list rule rollout comparisons failed", "tenant", r.PathValue("slug"), "rollout_id", rolloutID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule rollout comparisons"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comparisons": comparisons})
}

func (s *Server) upsertMembership(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Role = strings.TrimSpace(request.Role)
	subject := strings.TrimSpace(r.PathValue("subject"))
	if subject == "" || !domain.ValidRole(request.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "subject or role is invalid"})
		return
	}
	membership, err := s.store.UpsertMembership(r.Context(), principal.Subject, r.PathValue("slug"), subject, request.Role)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant owner role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a workspace must retain at least one owner"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update membership"})
		return
	}
	writeJSON(w, http.StatusOK, membership)
}

func (s *Server) listMemberships(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	members, err := s.store.ListMemberships(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if err != nil {
		slog.Error("list memberships failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list members"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (s *Server) setMembershipActivation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Active *bool `json:"active"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Active == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "membership activation state is required"})
		return
	}
	membership, err := s.store.SetMembershipActive(r.Context(), principal.Subject, r.PathValue("slug"), r.PathValue("subject"), *request.Active)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace member was not found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant owner role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "membership cannot be changed; keep one active owner and do not deactivate your own session"})
		return
	}
	if err != nil {
		slog.Error("set membership activation failed", "tenant", r.PathValue("slug"), "subject", r.PathValue("subject"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update workspace member"})
		return
	}
	writeJSON(w, http.StatusOK, membership)
}

func (s *Server) createWorkspaceInvitation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.WorkspaceInvitationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	creation, err := s.store.CreateWorkspaceInvitation(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace was not found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant owner role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an active member or invitation already exists for this subject"})
		return
	}
	if errors.Is(err, store.ErrInvalidInvitation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation subject, role, or expiry is invalid"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create workspace invitation"})
		return
	}
	writeJSON(w, http.StatusCreated, creation)
}

func (s *Server) listWorkspaceInvitations(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	invitations, err := s.store.ListWorkspaceInvitations(r.Context(), principal.Subject, r.PathValue("slug"), 100)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace was not found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list workspace invitations"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": invitations})
}

func (s *Server) acceptWorkspaceInvitation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	membership, err := s.store.AcceptWorkspaceInvitation(r.Context(), principal.Subject, request.Token)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation is unavailable or belongs to another identity"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this identity is already a workspace member"})
		return
	}
	if errors.Is(err, store.ErrInvalidInvitation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation token is invalid"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not accept workspace invitation"})
		return
	}
	writeJSON(w, http.StatusOK, membership)
}

func (s *Server) revokeWorkspaceInvitation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(r.PathValue("invitationID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation id is invalid"})
		return
	}
	invitation, err := s.store.RevokeWorkspaceInvitation(r.Context(), principal.Subject, r.PathValue("slug"), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pending invitation or workspace was not found"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant owner role is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not revoke workspace invitation"})
		return
	}
	writeJSON(w, http.StatusOK, invitation)
}

func (s *Server) requestWorkspaceAccess(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Slug string `json:"slug"`
		Note string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.RequestWorkspaceAccess(r.Context(), principal.Subject, input.Slug, input.Note); err != nil {
		if errors.Is(err, store.ErrInvalidAccessRequest) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace slug or note is invalid"})
			return
		}
		slog.Error("workspace access request failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "workspace access request could not be recorded"})
		return
	}
	// Never confirm whether the slug exists, has an owner, or already has a
	// request from this identity. Approval is an independent owner decision.
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "received"})
}

func (s *Server) listWorkspaceAccessRequests(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	items, err := s.store.ListWorkspaceAccessRequests(r.Context(), principal.Subject, r.PathValue("slug"), 100)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace access requests were not found"})
		return
	}
	if err != nil {
		slog.Error("list workspace access requests failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "workspace access requests could not be loaded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": items})
}

func (s *Server) decideWorkspaceAccessRequest(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	requestID, err := uuid.Parse(r.PathValue("requestID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "access request id is invalid"})
		return
	}
	var input struct {
		Decision string `json:"decision"`
		Revision int    `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.DecideWorkspaceAccessRequest(r.Context(), principal.Subject, r.PathValue("slug"), requestID, input.Revision, input.Decision)
	switch {
	case errors.Is(err, store.ErrInvalidAccessRequest):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "access decision or revision is invalid"})
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "access request was not found"})
	case errors.Is(err, store.ErrRevisionConflict), errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "access request changed or membership must be managed separately"})
	case err != nil:
		slog.Error("decide workspace access request failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "access request could not be decided"})
	default:
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *Server) upsertProviderIdentity(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Subject string `json:"subject"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	input := domain.ProviderIdentity{Provider: domain.Provider(strings.TrimSpace(r.PathValue("provider"))), ExternalID: strings.TrimSpace(r.PathValue("externalID")), Subject: strings.TrimSpace(request.Subject)}
	identity, err := s.store.UpsertProviderIdentity(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider identity is already mapped"})
		return
	}
	if errors.Is(err, store.ErrInvalidProviderIdentity) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider identity is invalid"})
		return
	}
	if err != nil {
		slog.Error("upsert provider identity failed", "tenant", r.PathValue("slug"), "provider", r.PathValue("provider"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not upsert provider identity"})
		return
	}
	writeJSON(w, http.StatusOK, identity)
}

// upsertOwnProviderIdentity binds a provider actor to the same authenticated
// principal that completed the provider OAuth flow. Unlike the administrator
// endpoint above, it accepts no subject from the request body, so a browser
// callback cannot map a GitHub or GitLab identity to a different member.
func (s *Server) upsertOwnProviderIdentity(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	input := domain.ProviderIdentity{
		Provider:   domain.Provider(strings.TrimSpace(r.PathValue("provider"))),
		ExternalID: strings.TrimSpace(r.PathValue("externalID")),
		Subject:    principal.Subject,
	}
	identity, err := s.store.UpsertProviderIdentity(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider identity is already mapped"})
		return
	}
	if errors.Is(err, store.ErrInvalidProviderIdentity) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider identity is invalid"})
		return
	}
	if err != nil {
		slog.Error("upsert own provider identity failed", "tenant", r.PathValue("slug"), "provider", input.Provider, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not upsert provider identity"})
		return
	}
	writeJSON(w, http.StatusOK, identity)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	runs, err := s.store.ListReviewRuns(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list review runs"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) listWorkQueue(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	filter := domain.WorkQueueFilter{
		View:            domain.WorkQueueView(strings.TrimSpace(r.URL.Query().Get("view"))),
		Repository:      strings.TrimSpace(r.URL.Query().Get("repository")),
		Query:           strings.TrimSpace(r.URL.Query().Get("q")),
		Cursor:          strings.TrimSpace(r.URL.Query().Get("cursor")),
		CursorDirection: domain.WorkQueueCursorDirection(strings.TrimSpace(r.URL.Query().Get("cursor_direction"))),
		Limit:           limit,
	}
	if !filter.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "work queue view or cursor is invalid"})
		return
	}
	page, err := s.store.ListWorkQueue(r.Context(), principal.Subject, r.PathValue("slug"), filter)
	if errors.Is(err, store.ErrInvalidWorkQueueFilter) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "work queue view or cursor is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list work queue failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list work queue"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) listPullRequests(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	filter := domain.PullRequestFilter{
		View:            domain.PullRequestView(strings.TrimSpace(r.URL.Query().Get("view"))),
		Repository:      strings.TrimSpace(r.URL.Query().Get("repository")),
		Query:           strings.TrimSpace(r.URL.Query().Get("q")),
		Cursor:          strings.TrimSpace(r.URL.Query().Get("cursor")),
		CursorDirection: domain.WorkQueueCursorDirection(strings.TrimSpace(r.URL.Query().Get("cursor_direction"))),
		Limit:           limit,
	}
	if !filter.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pull request view or cursor is invalid"})
		return
	}
	page, err := s.store.ListPullRequests(r.Context(), principal.Subject, r.PathValue("slug"), filter)
	if errors.Is(err, store.ErrInvalidPullRequestFilter) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pull request view or cursor is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list pull requests failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list pull requests"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) createCLIReview(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateAPIKey(w, r, domain.APIKeyScopeReviewsCreate)
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	var input domain.CLIReviewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	submission, err := s.store.SubmitCLIReview(r.Context(), principal, r.PathValue("slug"), idempotencyKey, input)
	if errors.Is(err, store.ErrInvalidCLIReview) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "installation, repository, pull request, refs, revision, mode, or idempotency key is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "API key or installation does not allow this repository"})
		return
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrUnknownInstallation) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "active installation was not found"})
		return
	}
	if errors.Is(err, store.ErrWorkspaceSetupIncomplete) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "workspace setup is incomplete; finish setup before creating a CLI review"})
		return
	}
	if errors.Is(err, store.ErrQuotaExceeded) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "review quota is exhausted"})
		return
	}
	if err != nil {
		slog.Error("create CLI review failed", "tenant", r.PathValue("slug"), "key_id", principal.KeyID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create CLI review"})
		return
	}
	statusPath := fmt.Sprintf("/v1/tenants/%s/cli-reviews/%s", url.PathEscape(r.PathValue("slug")), submission.Run.ID)
	w.Header().Set("Location", statusPath)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true, "replayed": submission.Replayed, "coalesced": submission.Coalesced,
		"run": submission.Run, "status_url": statusPath, "evidence_url": statusPath + "/evidence",
	})
}

func (s *Server) listCLIReviews(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
	}
	runs, err := s.store.ListCLIReviewRuns(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		slog.Error("list CLI reviews failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list CLI reviews"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) getCLIReview(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateAPIKey(w, r, domain.APIKeyScopeReviewsRead)
	if !ok {
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	run, err := s.store.GetCLIReview(r.Context(), principal, runID)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CLI review not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load CLI review"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) getCLIReviewEvidence(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateAPIKey(w, r, domain.APIKeyScopeReviewsRead)
	if !ok {
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	evidence, err := s.store.GetCLIReviewEvidence(r.Context(), principal, runID)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CLI review evidence not found"})
		return
	}
	if err != nil {
		slog.Error("get CLI review evidence failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load CLI review evidence"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, evidence)
}

func (s *Server) cancelCLIReview(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateAPIKey(w, r, domain.APIKeyScopeRunsCancel)
	if !ok {
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	var request struct {
		Revision int `json:"revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	run, err := s.store.RequestCLIReviewCancellation(r.Context(), principal, runID, request.Revision)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CLI review not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "CLI review revision is stale"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "CLI review cannot be cancelled"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not cancel CLI review"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) authenticateAPIKey(w http.ResponseWriter, r *http.Request, requiredScope string) (domain.APIKeyPrincipal, bool) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "valid Bearer API key is required"})
		return domain.APIKeyPrincipal{}, false
	}
	principal, err := s.store.AuthenticateAPIKey(r.Context(), strings.TrimSpace(parts[1]))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "API key is invalid, expired, or revoked"})
		return domain.APIKeyPrincipal{}, false
	}
	if principal.TenantSlug != r.PathValue("slug") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return domain.APIKeyPrincipal{}, false
	}
	if !principal.HasScope(requiredScope) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "API key scope is insufficient"})
		return domain.APIKeyPrincipal{}, false
	}
	return principal, true
}

func parseRunID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return uuid.Nil, false
	}
	return runID, true
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	run, err := s.store.GetReviewRun(r.Context(), principal.Subject, r.PathValue("slug"), runID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review run not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load review run"})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) getReviewEvidence(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	evidence, err := s.store.GetReviewEvidence(r.Context(), principal.Subject, r.PathValue("slug"), runID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review evidence not found"})
		return
	}
	if err != nil {
		slog.Error("get review evidence failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load review evidence"})
		return
	}
	writeJSON(w, http.StatusOK, evidence)
}

func (s *Server) getRuleSnapshot(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	snapshot, err := s.store.GetRuleSnapshot(r.Context(), principal.Subject, r.PathValue("slug"), runID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rule snapshot was not found"})
		return
	}
	if err != nil {
		slog.Error("get rule snapshot failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load rule snapshot"})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	var request struct {
		Revision int `json:"revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	run, err := s.store.RequestRunCancellation(r.Context(), principal.Subject, r.PathValue("slug"), runID, request.Revision)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review run not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review run revision is stale"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review run cannot be cancelled"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not cancel review run"})
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

// retryRun is intentionally a new-run admission, not an attempt to resurrect
// a terminal execution. The response tells the console exactly which immutable
// evidence record is now progressing.
func (s *Server) retryRun(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	var request domain.RunRetryInput
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := s.store.RequestRunRetry(r.Context(), principal.Subject, r.PathValue("slug"), runID, request)
	if errors.Is(err, store.ErrInvalidRunRetry) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "retry revision or idempotency key is invalid"})
		return
	}
	if errors.Is(err, store.ErrWorkspaceSetupIncomplete) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "workspace setup is incomplete; finish setup before retrying a review"})
		return
	}
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review run not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review run revision is stale; refresh before retrying"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review run is not retryable or its connection is no longer eligible"})
		return
	}
	if err != nil {
		slog.Error("retry review run failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue review retry"})
		return
	}
	statusPath := fmt.Sprintf("/v1/tenants/%s/runs/%s", url.PathEscape(r.PathValue("slug")), result.Run.ID)
	w.Header().Set("Location", statusPath)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true, "replayed": result.Replayed, "run": result.Run,
		"status_url": statusPath, "evidence_url": statusPath + "/evidence",
	})
}

// retryInteractionResponse recovers only an exhausted provider progress reply.
// Its store transaction retains the original marker and keeps the run behind
// the provider-accepted acknowledgement barrier.
func (s *Server) retryInteractionResponse(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	var request domain.RunRetryInput
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := s.store.RetryInteractionResponse(r.Context(), principal.Subject, r.PathValue("slug"), runID, request)
	switch {
	case errors.Is(err, store.ErrInvalidRunRetry):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "revision or idempotency key is invalid"})
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review run not found"})
	case errors.Is(err, store.ErrWorkspaceSetupIncomplete):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "workspace setup is incomplete"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review run revision is stale"})
	case errors.Is(err, store.ErrInteractionResponseNotExhausted):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the provider acknowledgement is still being delivered; refresh its status before retrying"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the acknowledgement cannot be retried for this run or its connection"})
	case err != nil:
		slog.Error("retry interaction response failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue acknowledgement retry"})
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, result)
	}
}

// reviewInterventionStore stays additive for the same compatibility boundary
// as scheduled admission: evidence remains readable even while an older
// control plane has not yet migrated its operational queue.
func (s *Server) reviewInterventionStore() (store.ReviewInterventionStore, bool) {
	interventions, ok := s.store.(store.ReviewInterventionStore)
	return interventions, ok
}

func (s *Server) listReviewInterventions(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	interventions, ok := s.reviewInterventionStore()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "human intervention management is unavailable on this control plane"})
		return
	}
	filter := domain.ReviewInterventionFilter{ActiveOnly: true, Limit: 50}
	if raw := r.URL.Query().Get("active_only"); raw != "" {
		filter.ActiveOnly, err = strconv.ParseBool(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "active_only must be a boolean"})
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		filter.Limit, err = strconv.Atoi(raw)
		if err != nil || filter.Limit < 1 || filter.Limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intervention limit must be between 1 and 100"})
			return
		}
	}
	if raw := r.URL.Query().Get("run_id"); raw != "" {
		runID, err := uuid.Parse(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id is invalid"})
			return
		}
		filter.RunID = &runID
	}
	items, err := interventions.ListReviewInterventions(r.Context(), principal.Subject, r.PathValue("slug"), filter)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidReviewIntervention) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intervention filter is invalid"})
		return
	}
	if err != nil {
		slog.Error("list review interventions failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load review interventions"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interventions": items})
}

func (s *Server) claimReviewIntervention(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	interventions, supported := s.reviewInterventionStore()
	if !supported {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "human intervention management is unavailable on this control plane"})
		return
	}
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	intervention, err := interventions.ClaimReviewIntervention(r.Context(), principal.Subject, r.PathValue("slug"), runID, request.ExpectedRevision)
	if errors.Is(err, store.ErrInvalidReviewIntervention) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intervention revision is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review intervention not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "intervention revision is stale; refresh before claiming"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "intervention is already claimed or resolved"})
		return
	}
	if err != nil {
		slog.Error("claim review intervention failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not claim review intervention"})
		return
	}
	writeJSON(w, http.StatusAccepted, intervention)
}

func (s *Server) resolveReviewIntervention(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	interventions, supported := s.reviewInterventionStore()
	if !supported {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "human intervention management is unavailable on this control plane"})
		return
	}
	var request domain.ReviewInterventionResolutionInput
	if !decodeJSON(w, r, &request) {
		return
	}
	intervention, err := interventions.ResolveReviewIntervention(r.Context(), principal.Subject, r.PathValue("slug"), runID, request)
	if errors.Is(err, store.ErrInvalidReviewIntervention) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a current intervention revision and a reason of 3 to 2000 characters are required"})
		return
	}
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review intervention not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "intervention revision is stale; refresh before acknowledging"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "intervention is already resolved"})
		return
	}
	if err != nil {
		slog.Error("resolve review intervention failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not acknowledge review intervention"})
		return
	}
	writeJSON(w, http.StatusAccepted, intervention)
}

// reviewScheduleStore is kept as an additive capability so an older test or
// adapter that implements the core Store contract cannot accidentally claim it
// supports scheduled admission. The production PostgreSQL store is the
// authority for both interfaces.
func (s *Server) reviewScheduleStore() (store.ReviewScheduleStore, bool) {
	schedules, ok := s.store.(store.ReviewScheduleStore)
	return schedules, ok
}

func (s *Server) listReviewSchedules(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	schedules, ok := s.reviewScheduleStore()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduled admission is unavailable on this control plane"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "schedule limit must be between 1 and 100"})
			return
		}
	}
	items, err := schedules.ListReviewSchedules(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	if errors.Is(err, store.ErrInvalidReviewSchedule) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "schedule request is invalid"})
		return
	}
	if err != nil {
		slog.Error("list review schedules failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load review schedules"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": items})
}

func (s *Server) createReviewSchedule(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, ok := parseRunID(w, r)
	if !ok {
		return
	}
	schedules, supported := s.reviewScheduleStore()
	if !supported {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduled admission is unavailable on this control plane"})
		return
	}
	var input domain.ReviewScheduleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	schedule, err := schedules.CreateReviewSchedule(r.Context(), principal.Subject, r.PathValue("slug"), runID, input)
	if errors.Is(err, store.ErrInvalidReviewSchedule) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scheduled_for must be between one minute and ninety days in the future"})
		return
	}
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review run not found"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only a terminal run with an eligible provider connection can be scheduled"})
		return
	}
	if err != nil {
		slog.Error("create review schedule failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not schedule review admission"})
		return
	}
	location := fmt.Sprintf("/v1/tenants/%s/review-schedules/%s", url.PathEscape(r.PathValue("slug")), schedule.ID)
	w.Header().Set("Location", location)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "schedule": schedule, "status_url": location})
}

func (s *Server) cancelReviewSchedule(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	scheduleID, err := uuid.Parse(r.PathValue("scheduleID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "schedule id is invalid"})
		return
	}
	schedules, supported := s.reviewScheduleStore()
	if !supported {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scheduled admission is unavailable on this control plane"})
		return
	}
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	schedule, err := schedules.CancelReviewSchedule(r.Context(), principal.Subject, r.PathValue("slug"), scheduleID, request.ExpectedRevision)
	if errors.Is(err, store.ErrInvalidReviewSchedule) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "schedule revision is invalid"})
		return
	}
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review schedule not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review schedule revision is stale; refresh before cancelling"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review schedule cannot be cancelled after admission"})
		return
	}
	if err != nil {
		slog.Error("cancel review schedule failed", "tenant", r.PathValue("slug"), "schedule_id", scheduleID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not cancel review schedule"})
		return
	}
	writeJSON(w, http.StatusAccepted, schedule)
}

func (s *Server) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	after := 0
	if value := r.URL.Query().Get("after_revision"); value != "" {
		after, err = strconv.Atoi(value)
		if err != nil || after < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "after_revision is invalid"})
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, err := s.store.ListRunEvents(r.Context(), principal.Subject, r.PathValue("slug"), runID, after)
		if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
			slog.Warn("sse stream ended: run not accessible", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
			_, _ = fmt.Fprint(w, "event: error\ndata: {\"code\":\"not_found\"}\n\n")
			flusher.Flush()
			return
		}
		if err != nil {
			slog.Error("sse stream ended: list events failed", "tenant", r.PathValue("slug"), "run_id", runID, "error", err)
			_, _ = fmt.Fprint(w, "event: error\ndata: {\"code\":\"unavailable\"}\n\n")
			flusher.Flush()
			return
		}
		for _, event := range events {
			encoded, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: transition\ndata: %s\n\n", event.Revision, encoded)
			after = event.Revision
		}
		if len(events) == 0 {
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if err := webhook.VerifyGitHubSignature(s.githubSecret, body, r.Header.Get("X-Hub-Signature-256")); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook signature"})
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "pull_request":
		event, accepted, err := webhook.NormalizeGitHub(r.Header.Get("X-GitHub-Delivery"), "pull_request", body, s.now())
		s.enqueueNormalized(r.Context(), w, event, accepted, err)
	case "issues":
		s.providerIssue(r.Context(), w, func(deliveryID string, payload []byte) (domain.ProviderIssueEvent, bool, error) {
			return webhook.NormalizeGitHubIssue(deliveryID, payload, s.now())
		}, r.Header.Get("X-GitHub-Delivery"), body)
	case "issue_comment":
		if s.providerAgentTaskFeedbackComment(r.Context(), w, webhook.NormalizeGitHubAgentTaskPullRequestComment, r.Header.Get("X-GitHub-Delivery"), body) {
			return
		}
		if s.providerAgentTaskComment(r.Context(), w, webhook.NormalizeGitHubAgentTaskIssueComment, r.Header.Get("X-GitHub-Delivery"), body) {
			return
		}
		s.providerComment(r.Context(), w, webhook.NormalizeGitHubIssueComment, r.Header.Get("X-GitHub-Delivery"), body)
	case "reaction":
		reaction, accepted, normalizeErr := webhook.NormalizeGitHubReaction(r.Header.Get("X-GitHub-Delivery"), body)
		if normalizeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
			return
		}
		if accepted {
			if err := s.store.RecordFindingReaction(r.Context(), reaction); errors.Is(err, store.ErrNotFound) {
				w.WriteHeader(http.StatusNoContent)
				return
			} else if err != nil {
				slog.Error("record GitHub finding reaction failed", "delivery_id", reaction.DeliveryID, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record finding feedback"})
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		issueReaction, accepted, normalizeErr := webhook.NormalizeGitHubProviderIssueReaction(r.Header.Get("X-GitHub-Delivery"), body)
		if normalizeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
			return
		}
		if !accepted {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := s.store.RecordProviderIssueReaction(r.Context(), issueReaction); errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		} else if err != nil {
			slog.Error("record GitHub provider Issue reaction failed", "delivery_id", issueReaction.DeliveryID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record Issue analysis feedback"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type commentNormalizer func(string, []byte) (domain.CommentEvent, bool, error)

type agentTaskCommentNormalizer func(string, []byte) (domain.AgentTaskCommandEvent, bool, error)

type agentTaskFeedbackNormalizer func(string, []byte) (domain.AgentTaskFeedbackEvent, bool, error)

type agentTaskFeedbackStore interface {
	ProcessAgentTaskFeedback(context.Context, domain.AgentTaskFeedbackEvent) (domain.AgentTaskFeedbackOutcome, error)
}

type providerIssueNormalizer func(string, []byte) (domain.ProviderIssueEvent, bool, error)

func (s *Server) providerIssue(ctx context.Context, w http.ResponseWriter, normalize providerIssueNormalizer, deliveryID string, body []byte) {
	event, accepted, err := normalize(deliveryID, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	if !accepted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	outcome, err := s.store.EnqueueProviderIssueAnalysis(ctx, event)
	if errors.Is(err, store.ErrUnknownInstallation) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, store.ErrAmbiguousInstallation) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider installation routing is ambiguous"})
		return
	}
	if err != nil {
		slog.Error("enqueue provider issue analysis failed", "provider", event.Provider, "repository", event.Repository, "issue_number", event.IssueNumber, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not enqueue issue analysis"})
		return
	}
	// A repository can opt in to label-driven Agent candidates. This is a
	// separate, strict gate from Issue triage: it never starts a coding agent,
	// and absence of the optional capability preserves normal Issue handling.
	if autoStore, ok := s.store.(agentTaskAutomaticAdmissionStore); ok {
		if _, err := autoStore.ProcessAutomaticAgentTask(ctx, event); err != nil {
			if errors.Is(err, store.ErrUnknownInstallation) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			slog.Error("automatic Agent candidate admission failed", "provider", event.Provider, "repository", event.Repository, "issue_number", event.IssueNumber, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not admit automatic Agent candidate"})
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": !outcome.Skipped, "duplicate": outcome.Duplicate, "skipped": outcome.Skipped,
		"reason": outcome.Reason, "job_id": outcome.Job.ID,
	})
}

func (s *Server) providerComment(ctx context.Context, w http.ResponseWriter, normalize commentNormalizer, deliveryID string, body []byte) {
	event, accepted, err := normalize(deliveryID, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	if !accepted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	parsed, mentioned, parseErr := interaction.Parse(event.Body)
	if !mentioned {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	input := domain.InteractionCommand{Event: event, Command: string(parsed.Kind), Mode: parsed.Mode, RuleSetID: parsed.RuleSetID, Target: parsed.Target, Normalized: parsed.Normalized}
	if parseErr != nil || parsed.Kind == interaction.Approve {
		input.Command, input.Normalized = "invalid", strings.TrimSpace(event.Body)
	}
	outcome, err := s.store.ProcessInteraction(ctx, input)
	if errors.Is(err, store.ErrUnknownInstallation) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, store.ErrAmbiguousInstallation) {
		slog.Warn("rejecting ambiguous provider interaction", "provider", event.Provider, "repository", event.Repository)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider installation routing is ambiguous"})
		return
	}
	if err != nil {
		slog.Error("process provider interaction failed", "provider", event.Provider, "repository", event.Repository, "review_number", event.ReviewNumber, "command", input.Command, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not process interaction"})
		return
	}
	response := map[string]any{"accepted": outcome.Accepted, "duplicate": outcome.Duplicate, "reason": outcome.Reason}
	if outcome.RunID != nil {
		response["run_id"] = outcome.RunID.String()
	}
	writeJSON(w, http.StatusAccepted, response)
}

// providerAgentTaskComment routes only ordinary Issue comments. A mention is
// still required before we create an interaction receipt, so normal Issue
// conversation cannot grow control-plane state or trigger a provider reply.
// The boolean distinguishes an Issue note from a merge-request comment, which
// continues through the existing review command path.
func (s *Server) providerAgentTaskComment(ctx context.Context, w http.ResponseWriter, normalize agentTaskCommentNormalizer, deliveryID string, body []byte) bool {
	event, accepted, err := normalize(deliveryID, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return true
	}
	if !accepted {
		return false
	}
	parsed, mentioned, parseErr := interaction.Parse(event.Body)
	if !mentioned {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	command, normalized := string(parsed.Kind), parsed.Normalized
	if parsed.Kind == interaction.Stop {
		command = "cancel"
	}
	if parseErr != nil || (parsed.Kind != interaction.Implement && parsed.Kind != interaction.Approve && parsed.Kind != interaction.Cancel && parsed.Kind != interaction.Stop && parsed.Kind != interaction.Status) || ((parsed.Kind == interaction.Cancel || parsed.Kind == interaction.Stop || parsed.Kind == interaction.Status) && parsed.Target != "") {
		command, normalized = "invalid", strings.TrimSpace(event.Body)
	}
	outcome, err := s.store.ProcessAgentTaskCommand(ctx, event, command, normalized)
	if errors.Is(err, store.ErrUnknownInstallation) {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if errors.Is(err, store.ErrAmbiguousInstallation) {
		slog.Warn("rejecting ambiguous provider Agent task command", "provider", event.Provider, "repository", event.Repository)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider installation routing is ambiguous"})
		return true
	}
	if err != nil {
		slog.Error("process provider Agent task command failed", "provider", event.Provider, "repository", event.Repository, "issue_number", event.IssueNumber, "command", command, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not process Agent task command"})
		return true
	}
	response := map[string]any{"accepted": outcome.Accepted, "duplicate": outcome.Duplicate, "reason": outcome.Reason}
	if outcome.TaskID != nil {
		response["task_id"] = outcome.TaskID.String()
	}
	writeJSON(w, http.StatusAccepted, response)
	return true
}

// providerAgentTaskFeedbackComment accepts only an explicit revise command on
// a provider PR/MR comment. Everything else continues to the ordinary review
// interaction handler, so comments cannot accidentally create coding work.
func (s *Server) providerAgentTaskFeedbackComment(ctx context.Context, w http.ResponseWriter, normalize agentTaskFeedbackNormalizer, deliveryID string, body []byte) bool {
	event, accepted, err := normalize(deliveryID, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return true
	}
	if !accepted {
		return false
	}
	parsed, mentioned, parseErr := interaction.Parse(event.Instruction)
	if !mentioned || parseErr != nil || parsed.Kind != interaction.Revise {
		return false
	}
	event.Instruction = parsed.Instruction
	feedbackStore, ok := s.store.(agentTaskFeedbackStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent task feedback is not configured"})
		return true
	}
	outcome, err := feedbackStore.ProcessAgentTaskFeedback(ctx, event)
	if errors.Is(err, store.ErrUnknownInstallation) {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if errors.Is(err, store.ErrAmbiguousInstallation) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider installation routing is ambiguous"})
		return true
	}
	if err != nil {
		slog.Error("process provider Agent feedback", "provider", event.Provider, "repository", event.Repository, "pull_request_number", event.PullRequestNumber, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not process Agent feedback"})
		return true
	}
	response := map[string]any{"accepted": outcome.Accepted, "duplicate": outcome.Duplicate, "reason": outcome.Reason}
	if outcome.TaskID != nil {
		response["task_id"] = outcome.TaskID.String()
	}
	writeJSON(w, http.StatusAccepted, response)
	return true
}

func (s *Server) gitlabWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if err := webhook.VerifyGitLabToken(s.gitlabSecret, r.Header.Get("X-Gitlab-Token")); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook token"})
		return
	}
	deliveryID := r.Header.Get("X-Gitlab-Event-UUID")
	switch r.Header.Get("X-Gitlab-Event") {
	case "Merge Request Hook":
		event, accepted, err := webhook.NormalizeGitLab(deliveryID, "Merge Request Hook", body, s.now())
		s.enqueueNormalized(r.Context(), w, event, accepted, err)
	case "Issue Hook":
		s.providerIssue(r.Context(), w, func(deliveryID string, payload []byte) (domain.ProviderIssueEvent, bool, error) {
			return webhook.NormalizeGitLabIssue(deliveryID, payload, s.now())
		}, deliveryID, body)
	case "Note Hook":
		if s.providerAgentTaskFeedbackComment(r.Context(), w, webhook.NormalizeGitLabAgentTaskMergeRequestNote, deliveryID, body) {
			return
		}
		if s.providerAgentTaskComment(r.Context(), w, webhook.NormalizeGitLabAgentTaskIssueNote, deliveryID, body) {
			return
		}
		s.providerComment(r.Context(), w, webhook.NormalizeGitLabNoteComment, deliveryID, body)
	case "Emoji Hook":
		reaction, accepted, normalizeErr := webhook.NormalizeGitLabEmoji(deliveryID, body)
		if normalizeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
			return
		}
		if accepted {
			if err := s.store.RecordFindingReaction(r.Context(), reaction); errors.Is(err, store.ErrNotFound) {
				w.WriteHeader(http.StatusNoContent)
				return
			} else if err != nil {
				slog.Error("record GitLab finding reaction failed", "delivery_id", reaction.DeliveryID, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record finding feedback"})
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		issueReaction, accepted, normalizeErr := webhook.NormalizeGitLabProviderIssueEmoji(deliveryID, body)
		if normalizeErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
			return
		}
		if !accepted {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := s.store.RecordProviderIssueReaction(r.Context(), issueReaction); errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		} else if err != nil {
			slog.Error("record GitLab provider Issue reaction failed", "delivery_id", issueReaction.DeliveryID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record Issue analysis feedback"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) enqueueNormalized(ctx context.Context, w http.ResponseWriter, event domain.InboundEvent, accepted bool, normalizeErr error) {
	if normalizeErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	if !accepted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	job, duplicate, err := s.store.Enqueue(ctx, event)
	if errors.Is(err, store.ErrUnknownInstallation) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider installation"})
		return
	}
	if errors.Is(err, store.ErrAmbiguousInstallation) {
		slog.Warn("rejecting ambiguous provider installation route", "provider", event.Provider, "repository", event.Repository)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider installation routing is ambiguous"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not enqueue review"})
		return
	}
	if duplicate {
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "duplicate": true})
		return
	}
	if job.ID == uuid.Nil {
		if job.State == domain.JobQueued {
			writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "deferred": true, "reason": job.ErrorMessage})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "skipped": true, "reason": job.ErrorMessage})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "job_id": job.ID.String()})
}

func readWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "webhook payload too large"})
		return nil, err
	}
	return body, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	return decodeJSONLimit(w, r, destination, 64<<10)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, destination any, limit int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		return false
	}
	return true
}

func validInstallation(input domain.InstallationInput) bool {
	if !input.Provider.Valid() || input.ExternalID == "" || !validRepositoryScope(input.RepositoryScope) || input.APIBaseURL == "" || input.AutomaticReviews == nil || !validMinimumSeverity(input.MinimumSeverity) {
		return false
	}
	if (input.AuthorScope != "all" || input.AuthorExternalID != "") && (input.AuthorScope != "mine" || !validProviderActorID(input.AuthorExternalID)) {
		return false
	}
	switch input.Provider {
	case domain.ProviderGitHub:
		return input.CredentialRef == "github-app"
	case domain.ProviderGitLab:
		return (input.CredentialRef == "gitlab-token" || validGitLabOAuthCredentialRef(input.CredentialRef)) && input.RepositoryScope != domain.AllAuthorizedRepositoriesScope
	default:
		return false
	}
}

func validGitLabOAuthCredentialRef(value string) bool {
	if !strings.HasPrefix(value, "secret://provider/gitlab-oauth/") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(value, "secret://provider/gitlab-oauth/"))
	return err == nil
}

func validRepositoryScope(value string) bool {
	items := strings.Split(value, ",")
	if len(items) == 0 || len(items) > 100 {
		return false
	}
	seen := make(map[string]struct{}, len(items))
	for _, raw := range items {
		item := strings.TrimSpace(raw)
		if item != domain.AllAuthorizedRepositoriesScope && !repositoryScopeToken.MatchString(item) {
			return false
		}
		if item == domain.AllAuthorizedRepositoriesScope && len(items) != 1 {
			return false
		}
		if _, duplicate := seen[item]; duplicate {
			return false
		}
		seen[item] = struct{}{}
	}
	return true
}

func canonicalRepositoryScope(value string) string {
	items := strings.Split(value, ",")
	for index := range items {
		items[index] = strings.TrimSpace(items[index])
	}
	return strings.Join(items, ",")
}

func validMinimumSeverity(value string) bool {
	switch value {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func trustedProviderAPIBaseURL(value string) string {
	value = strings.TrimSuffix(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	return value
}

func trustedGitLabProviderAPIBaseURL(value string, allowHTTP bool) string {
	value = strings.TrimSuffix(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return ""
	}
	return value
}

func providerProfile(provider domain.Provider, apiBaseURL string, gitLabDeploymentTokenAvailable bool) domain.ProviderProfile {
	parsed, err := url.Parse(apiBaseURL)
	host := ""
	if err == nil && parsed != nil {
		host = parsed.Hostname()
	}
	publicHost := "gitlab.com"
	publicLabel := "GitLab.com"
	if provider == domain.ProviderGitHub {
		publicHost = "api.github.com"
		publicLabel = "GitHub.com"
	}
	deploymentTokenAvailable := provider == domain.ProviderGitLab && gitLabDeploymentTokenAvailable
	if host == publicHost {
		return domain.ProviderProfile{Provider: provider, APIBaseURL: apiBaseURL, Mode: "cloud", Label: publicLabel, DeploymentTokenAvailable: deploymentTokenAvailable}
	}
	product := "GitLab"
	if provider == domain.ProviderGitHub {
		product = "GitHub"
	}
	labelHost := host
	if labelHost == "" {
		labelHost = "deployment endpoint"
	}
	return domain.ProviderProfile{
		Provider: provider, APIBaseURL: apiBaseURL, Mode: "self_managed",
		Label: fmt.Sprintf("%s self-managed · %s", product, labelHost), DeploymentTokenAvailable: deploymentTokenAvailable,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
