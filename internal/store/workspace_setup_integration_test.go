package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestWorkspaceSetupCheckpointRequiresOrderedDurableSteps(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "setup-checkpoint-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Setup Checkpoint')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupReviewScope, ExpectedRevision: 0}); !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("advance without connection error=%v, want ErrInvalidReviewConfig", err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','secret://setup/test')`, installationID, tenantID, "setup-"+tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupComplete, ExpectedRevision: 0}); !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("skipped setup error=%v, want ErrInvalidReviewConfig", err)
	}
	checkpoint, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupReviewScope, ExpectedRevision: 0})
	if err != nil || checkpoint.Revision != 1 || checkpoint.CurrentStep != domain.WorkspaceSetupReviewScope {
		t.Fatalf("first setup checkpoint=%#v error=%v", checkpoint, err)
	}
	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupRules, ExpectedRevision: 1}); !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("jumped setup error=%v, want ErrInvalidReviewConfig", err)
	}
	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupLearning, ExpectedRevision: 1}); !errors.Is(err, ErrSetupRepositoryScopeIncomplete) {
		t.Fatalf("advance without synchronized repository error=%v, want ErrSetupRepositoryScopeIncomplete", err)
	}
	observedAt := time.Now().UTC()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_health_probes (installation_id,state,health_state,observed_at,receipt)
		VALUES ($1,'completed','live',$2,'{"inventory_state":"synchronized"}'::jsonb)
		ON CONFLICT (installation_id) DO UPDATE SET
		state='completed',health_state='live',observed_at=EXCLUDED.observed_at,receipt=EXCLUDED.receipt`, installationID, observedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_repository_inventory (installation_id,external_id,name,last_seen_at) VALUES ($1,'setup-repository','RainLib/open-review-platform',$2)`, installationID, observedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupLearning, ExpectedRevision: 1}); !errors.Is(err, ErrSetupRepositoryScopeIncomplete) {
		t.Fatalf("stale repository advanced setup: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_repository_inventory SET last_seen_at=$2 WHERE installation_id=$1`, installationID, observedAt); err != nil {
		t.Fatal(err)
	}
	checkpoint, err = postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupLearning, ExpectedRevision: 1})
	if err != nil || checkpoint.Revision != 2 {
		t.Fatalf("learning checkpoint=%#v error=%v", checkpoint, err)
	}
	retried, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupLearning, ExpectedRevision: 1})
	if err != nil || retried.Revision != 2 || retried.CurrentStep != domain.WorkspaceSetupLearning {
		t.Fatalf("idempotent retry=%#v error=%v", retried, err)
	}
	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupSeverity, ExpectedRevision: checkpoint.Revision}); !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("advance without learning boundary error=%v, want ErrInvalidReviewConfig", err)
	}
	checkpoint, err = postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{
		CurrentStep:      domain.WorkspaceSetupSeverity,
		ExpectedRevision: checkpoint.Revision,
		LearningBoundary: &domain.WorkspaceLearningBoundary{Mode: domain.WorkspaceLearningGovernedPolicy, ReviewerExclusions: []string{"reviewer-b", "reviewer-a", "reviewer-a"}},
	})
	if err != nil {
		t.Fatalf("advance to severity: %v", err)
	}
	if checkpoint.LearningBoundary == nil || checkpoint.LearningBoundary.Mode != domain.WorkspaceLearningGovernedPolicy || len(checkpoint.LearningBoundary.ReviewerExclusions) != 2 || checkpoint.LearningBoundary.ReviewerExclusions[0] != "reviewer-a" {
		t.Fatalf("persisted learning boundary=%#v", checkpoint.LearningBoundary)
	}
	var learningAudit string
	if err := postgres.pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE tenant_id=$1 AND action='workspace.setup_checkpoint.saved' ORDER BY created_at DESC LIMIT 1`, tenantID).Scan(&learningAudit); err != nil {
		t.Fatalf("load learning boundary audit: %v", err)
	}
	if !strings.Contains(learningAudit, `"learning_mode": "governed_policy"`) || !strings.Contains(learningAudit, `"reviewer_exclusion_count": 2`) || strings.Contains(learningAudit, "reviewer-a") {
		t.Fatalf("learning audit=%s", learningAudit)
	}
	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupRules, ExpectedRevision: checkpoint.Revision}); !errors.Is(err, ErrSetupReviewPolicyIncomplete) {
		t.Fatalf("advance without merge policy error=%v, want ErrSetupReviewPolicyIncomplete", err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: domain.DefaultReviewConfig(domain.ReviewConfigGeneral),
	}); err != nil {
		t.Fatalf("save setup merge policy: %v", err)
	}
	for _, next := range []domain.WorkspaceSetupStep{domain.WorkspaceSetupRules, domain.WorkspaceSetupComplete} {
		checkpoint, err = postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: next, ExpectedRevision: checkpoint.Revision})
		if err != nil {
			t.Fatalf("advance to %s: %v", next, err)
		}
	}
	if checkpoint.CurrentStep != domain.WorkspaceSetupComplete || checkpoint.CompletedAt == nil {
		t.Fatalf("completed checkpoint=%#v", checkpoint)
	}
	if checkpoint.Readiness == nil || checkpoint.Readiness.CheckpointRevision != checkpoint.Revision || checkpoint.Readiness.InstallationID != installationID.String() || checkpoint.Readiness.Provider != domain.ProviderGitHub || checkpoint.Readiness.GeneralConfigRevision != 1 || checkpoint.Readiness.GeneralConfigContentSHA256 == "" || checkpoint.Readiness.LearningMode != domain.WorkspaceLearningGovernedPolicy || checkpoint.Readiness.RuleSetCount != 0 {
		t.Fatalf("completion readiness=%#v", checkpoint.Readiness)
	}
	loaded, err := postgres.GetWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug)
	if err != nil || loaded.Readiness == nil || loaded.Readiness.GeneralConfigContentSHA256 != checkpoint.Readiness.GeneralConfigContentSHA256 {
		t.Fatalf("loaded completion readiness=%#v error=%v", loaded.Readiness, err)
	}
}

func TestFirstInstallationAtomicallyStartsWorkspaceSetup(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	tenantID := uuid.New()
	tenantSlug := "first-installation-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'First Installation')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	automatic := true
	installationInput := domain.InstallationInput{Provider: domain.ProviderGitHub, ExternalID: "first-" + tenantID.String(), RepositoryScope: "RainLib/*", AutomaticReviews: &automatic, MinimumSeverity: "medium", APIBaseURL: "https://api.github.com", CredentialRef: "github-app"}
	installation, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, installationInput)
	if err != nil || !installation.Active || installation.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("first installation=%#v error=%v", installation, err)
	}
	replayedInstallation, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, installationInput)
	if err != nil || replayedInstallation.ID != installation.ID || replayedInstallation.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("replayed installation=%#v error=%v, want the original pending installation", replayedInstallation, err)
	}
	conflictingInstallation := installationInput
	conflictingInstallation.MinimumSeverity = "high"
	if _, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, conflictingInstallation); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed installation replay error=%v, want ErrConflict", err)
	}
	var installationCreatedEvents int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='installation.created' AND target=$2`, tenantID, installation.ID.String()).Scan(&installationCreatedEvents); err != nil || installationCreatedEvents != 1 {
		t.Fatalf("installation creation audit count=%d error=%v, want one", installationCreatedEvents, err)
	}
	checkpoint, err := postgres.GetWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug)
	if err != nil || checkpoint.CurrentStep != domain.WorkspaceSetupReviewScope || checkpoint.Revision != 1 {
		t.Fatalf("checkpoint=%#v error=%v", checkpoint, err)
	}
	retried, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupReviewScope, ExpectedRevision: 0})
	if err != nil || retried.Revision != 1 || retried.CurrentStep != domain.WorkspaceSetupReviewScope {
		t.Fatalf("first-installation checkpoint retry=%#v error=%v", retried, err)
	}
	if _, err := postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupLearning, ExpectedRevision: 1}); !errors.Is(err, ErrInvalidReviewConfig) {
		t.Fatalf("unverified installation advanced setup error=%v, want ErrInvalidReviewConfig", err)
	}
	target, err := postgres.ClaimProviderHealthProbe(ctx, "provider-prober-test", time.Minute, &installation.ID)
	if err != nil {
		t.Fatalf("claim initial verification: %v", err)
	}
	if target.Installation.VerificationState != domain.InstallationVerificationChecking {
		t.Fatalf("claimed verification state=%q, want checking", target.Installation.VerificationState)
	}
	now := time.Now().UTC()
	if err := postgres.CompleteProviderHealthProbe(ctx, *target, domain.ProviderProbeResult{HealthState: domain.HealthLive, ObservedAt: now, Permissions: []string{"repository_inventory:read"}, Receipt: map[string]any{"schema": "open-review.provider-health-probe.v1", "scope_verification": "inventory_backed"}, Repositories: []domain.ProviderRepository{{ExternalID: "first-repository", Name: "RainLib/open-review-platform"}}}, now.Add(time.Minute)); err != nil {
		t.Fatalf("complete initial verification: %v", err)
	}
	var verificationState domain.InstallationVerificationState
	if err := postgres.pool.QueryRow(ctx, `SELECT verification_state FROM provider_installations WHERE id=$1`, installation.ID).Scan(&verificationState); err != nil || verificationState != domain.InstallationVerificationVerified {
		t.Fatalf("completed verification state=%q error=%v", verificationState, err)
	}
	checkpoint, err = postgres.UpdateWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug, domain.WorkspaceSetupCheckpointInput{CurrentStep: domain.WorkspaceSetupLearning, ExpectedRevision: 1})
	if err != nil || checkpoint.CurrentStep != domain.WorkspaceSetupLearning || checkpoint.Revision != 2 {
		t.Fatalf("verified installation did not advance setup checkpoint=%#v error=%v", checkpoint, err)
	}
	gitLabInstallationInput := domain.InstallationInput{Provider: domain.ProviderGitLab, ExternalID: "second-" + tenantID.String(), RepositoryScope: "RainLib/*", AutomaticReviews: &automatic, MinimumSeverity: "medium", APIBaseURL: "https://gitlab.com/api/v4", CredentialRef: "gitlab-token"}
	gitLabInstallation, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, gitLabInstallationInput)
	if err != nil {
		t.Fatal(err)
	}
	replayedGitLabInstallation, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, gitLabInstallationInput)
	if err != nil || replayedGitLabInstallation.ID != gitLabInstallation.ID {
		t.Fatalf("replayed GitLab installation=%#v error=%v, want the original installation", replayedGitLabInstallation, err)
	}
	checkpoint, err = postgres.GetWorkspaceSetupCheckpoint(ctx, "owner", tenantSlug)
	if err != nil || checkpoint.CurrentStep != domain.WorkspaceSetupLearning || checkpoint.Revision != 2 {
		t.Fatalf("second installation regressed checkpoint=%#v error=%v", checkpoint, err)
	}
}

func TestIncompleteWorkspaceSetupSkipsInboundReviewAdmission(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "setup-admission-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Setup Admission')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,minimum_severity,api_base_url,credential_ref,verification_state)
		VALUES ($1,$2,'github',$3,'RainLib/*',TRUE,'medium','https://api.github.com','github-app','verified')`, installationID, tenantID, "setup-admission-"+installationID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by)
		VALUES ($1,'review_scope',1,'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}

	job, duplicate, err := postgres.Enqueue(ctx, domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com",
		DeliveryID: "setup-incomplete-" + tenantID.String(), EventName: "pull_request",
		InstallationExternalID: "setup-admission-" + installationID.String(),
		Repository:             "RainLib/open-review-platform", CloneURL: "https://github.com/RainLib/open-review-platform.git",
		ReviewNumber: 7, BaseRef: "main", BaseSHA: "base", HeadRef: "feature/setup", HeadSHA: "head",
		Payload: []byte(`{}`), ReceivedAt: time.Now().UTC(), Action: "opened",
	})
	if err != nil || duplicate || job.ID != uuid.Nil || job.State != domain.JobCancelled || job.ErrorMessage != "workspace setup is incomplete; complete the recorded review baseline before reviews can run" {
		t.Fatalf("incomplete setup admission job=%#v duplicate=%t error=%v", job, duplicate, err)
	}
	var jobs, skippedAudits int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_jobs WHERE tenant_id=$1`, tenantID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='review.admission_skipped'`, tenantID).Scan(&skippedAudits); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || skippedAudits != 1 {
		t.Fatalf("incomplete setup must retain only the delivery/audit ledger, jobs=%d skippedAudits=%d", jobs, skippedAudits)
	}
}

func TestInstallationAssignedToAnotherTenantReturnsConflict(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	firstTenantID, secondTenantID := uuid.New(), uuid.New()
	firstSlug := "installation-owner-" + firstTenantID.String()[:8]
	secondSlug := "installation-contender-" + secondTenantID.String()[:8]
	for _, tenant := range []struct {
		id   uuid.UUID
		slug string
	}{
		{id: firstTenantID, slug: firstSlug},
		{id: secondTenantID, slug: secondSlug},
	} {
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,$2)`, tenant.id, tenant.slug); err != nil {
			t.Fatal(err)
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenant.id); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = ANY($1)`, []uuid.UUID{firstTenantID, secondTenantID})
	}()

	automatic := true
	input := domain.InstallationInput{
		Provider:         domain.ProviderGitHub,
		ExternalID:       "shared-installation-" + firstTenantID.String(),
		RepositoryScope:  "RainLib/open-review-platform",
		AutomaticReviews: &automatic,
		MinimumSeverity:  "medium",
		APIBaseURL:       "https://api.github.com",
		CredentialRef:    "github-app",
	}
	firstInstallation, err := postgres.CreateInstallation(ctx, "owner", firstSlug, input)
	if err != nil {
		t.Fatalf("create first installation: %v", err)
	}
	if _, err := postgres.CreateInstallation(ctx, "owner", secondSlug, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("second tenant shared installation error=%v, want ErrConflict", err)
	}
	installations, err := postgres.ListInstallations(ctx, "owner", secondSlug, 10)
	if err != nil || len(installations) != 0 {
		t.Fatalf("second tenant installations=%#v error=%v, want no partial installation", installations, err)
	}
	if _, err := postgres.DeactivateInstallation(ctx, "owner", firstSlug, firstInstallation.ID); err != nil {
		t.Fatalf("deactivate source installation: %v", err)
	}
	secondInstallation, err := postgres.CreateInstallation(ctx, "owner", secondSlug, input)
	if err != nil {
		t.Fatalf("create installation after source deactivation: %v", err)
	}
	if secondInstallation.ID == firstInstallation.ID || !secondInstallation.Active || secondInstallation.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("rebound installation=%#v, want fresh active pending record distinct from %s", secondInstallation, firstInstallation.ID)
	}
	firstInstallations, err := postgres.ListInstallations(ctx, "owner", firstSlug, 10)
	if err != nil || len(firstInstallations) != 1 || firstInstallations[0].Active {
		t.Fatalf("source installation history=%#v error=%v, want one inactive record", firstInstallations, err)
	}
}

func TestIncompleteWorkspaceSetupRejectsMentionWithoutCreatingRun(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "setup-interaction-" + tenantID.String()[:8]
	actorExternalID := "setup-actor-" + tenantID.String()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Setup Interaction')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'reviewer','reviewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,minimum_severity,api_base_url,credential_ref,verification_state)
		VALUES ($1,$2,'github',$3,'RainLib/*',TRUE,'medium','https://api.github.com','github-app','verified')`, installationID, tenantID, "setup-interaction-"+installationID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_actor_mappings (tenant_id,provider,external_id,subject) VALUES ($1,'github',$2,'reviewer')`, tenantID, actorExternalID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by)
		VALUES ($1,'severity',3,'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}

	outcome, err := postgres.ProcessInteraction(ctx, domain.InteractionCommand{
		Event: domain.CommentEvent{
			Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com",
			DeliveryID:             "setup-mention-" + tenantID.String(),
			InstallationExternalID: "setup-interaction-" + installationID.String(),
			Repository:             "RainLib/open-review-platform", ReviewNumber: 9,
			CommentExternalID: "99", ActorExternalID: actorExternalID, Body: "@openreview review",
		},
		Command: "review", Normalized: "@openreview review",
	})
	if err != nil || outcome.Accepted || outcome.RunID != nil || outcome.Reason != "workspace setup is incomplete; complete setup in Open Review before requesting a review" {
		t.Fatalf("incomplete setup interaction outcome=%#v error=%v", outcome, err)
	}
	var requests, runs, rejected, responses int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_requests WHERE tenant_id=$1`, tenantID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_runs run JOIN review_requests request ON request.id=run.request_id WHERE request.tenant_id=$1`, tenantID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_interactions interaction JOIN review_requests request ON request.id=interaction.request_id WHERE request.tenant_id=$1 AND interaction.result='rejected'`, tenantID).Scan(&rejected); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM outbox_messages message
		JOIN review_interactions interaction
		  ON message.aggregate_type='review_interaction' AND message.aggregate_id=interaction.id
		JOIN review_requests request ON request.id=interaction.request_id
		WHERE request.tenant_id=$1 AND message.topic='review.interaction.response'`, tenantID).Scan(&responses); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || runs != 0 || rejected != 1 || responses < 1 {
		t.Fatalf("rejected mention must retain request/response evidence without a run, requests=%d runs=%d rejected=%d responses=%d", requests, runs, rejected, responses)
	}
}
