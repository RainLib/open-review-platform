package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestCLIReviewAdmissionIsScopedIdempotentAndEvidenceReadable(t *testing.T) {
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
	tenantSlug := "cli-review-" + tenantID.String()[:8]
	externalID := "cli-installation-" + installationID.String()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'CLI Review Integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref, verification_state)
		VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'github-app', 'pending')`, installationID, tenantID, externalID); err != nil {
		t.Fatal(err)
	}
	var runIDs []uuid.UUID
	defer func() {
		if len(runIDs) > 0 {
			_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE aggregate_id = ANY($1::uuid[])`, runIDs)
		}
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	}()

	creation, err := postgres.CreateAPIKey(ctx, "owner", tenantSlug, domain.APIKeyInput{
		Name: "CLI review", Scopes: []string{domain.APIKeyScopeReviewsCreate, domain.APIKeyScopeReviewsRead, domain.APIKeyScopeRunsCancel},
		Repositories: []string{"RainLib/open-review-platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := postgres.AuthenticateAPIKey(ctx, creation.Secret)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.CLIReviewInput{
		InstallationID: installationID, Repository: "RainLib/open-review-platform", ReviewNumber: 3,
		BaseRef: "main", HeadRef: "feature/reliable-review-workflow",
		BaseSHA: "0123456789abcdef0123456789abcdef01234567",
		HeadSHA: "89abcdef0123456789abcdef0123456789abcdef",
		Mode:    domain.ReviewModeSecurity,
	}
	if _, err := postgres.SubmitCLIReview(ctx, principal, tenantSlug, "integration:cli-review:unverified", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unverified installation admission error=%v, want ErrNotFound", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET verification_state='verified' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by) VALUES ($1,'review_scope',1,'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SubmitCLIReview(ctx, principal, tenantSlug, "integration:cli-review:setup-incomplete", input); !errors.Is(err, ErrWorkspaceSetupIncomplete) {
		t.Fatalf("incomplete setup admission error=%v, want ErrWorkspaceSetupIncomplete", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE workspace_setup_checkpoints SET current_step='complete',revision=2,completed_at=now() WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatal(err)
	}
	submission, err := postgres.SubmitCLIReview(ctx, principal, tenantSlug, "integration:cli-review:0001", input)
	if err != nil {
		t.Fatal(err)
	}
	runIDs = append(runIDs, submission.Run.ID)
	if submission.Replayed || submission.Coalesced || submission.Run.TriggerKind != "cli" || submission.Run.ReviewMode != domain.ReviewModeSecurity || submission.Run.CallerSubject != principal.Subject {
		t.Fatalf("unexpected first submission: %#v", submission)
	}
	var cloneURL string
	if err := postgres.pool.QueryRow(ctx, `SELECT clone_url FROM review_jobs WHERE id = $1`, *submission.Run.LegacyJobID).Scan(&cloneURL); err != nil {
		t.Fatal(err)
	}
	if cloneURL != "https://github.com/RainLib/open-review-platform.git" {
		t.Fatalf("clone URL=%q", cloneURL)
	}

	replay, err := postgres.SubmitCLIReview(ctx, principal, tenantSlug, "integration:cli-review:0001", input)
	if err != nil || !replay.Replayed || replay.Run.ID != submission.Run.ID {
		t.Fatalf("replay=%#v error=%v", replay, err)
	}
	coalesced, err := postgres.SubmitCLIReview(ctx, principal, tenantSlug, "integration:cli-review:0002", input)
	if err != nil || !coalesced.Coalesced || coalesced.Run.ID != submission.Run.ID {
		t.Fatalf("coalesced=%#v error=%v", coalesced, err)
	}
	readerCreation, err := postgres.CreateAPIKey(ctx, "owner", tenantSlug, domain.APIKeyInput{
		Name: "CLI reader", Scopes: []string{domain.APIKeyScopeReviewsRead, domain.APIKeyScopeRunsCancel},
		Repositories: []string{"RainLib/open-review-platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	readerPrincipal, err := postgres.AuthenticateAPIKey(ctx, readerCreation.Secret)
	if err != nil {
		t.Fatal(err)
	}

	runs, err := postgres.ListCLIReviewRuns(ctx, "owner", tenantSlug, 25)
	if err != nil || len(runs) != 1 || runs[0].ID != submission.Run.ID {
		t.Fatalf("runs=%#v error=%v", runs, err)
	}
	evidence, err := postgres.GetCLIReviewEvidence(ctx, readerPrincipal, submission.Run.ID)
	if err != nil || evidence.Run.ID != submission.Run.ID || len(evidence.Events) != 1 || evidence.Events[0].ActorSubject != principal.Subject {
		t.Fatalf("evidence=%#v error=%v", evidence, err)
	}

	invalidRepo := input
	invalidRepo.Repository = "RainLib/private"
	if _, err := postgres.SubmitCLIReview(ctx, principal, tenantSlug, "integration:cli-review:0003", invalidRepo); !errors.Is(err, ErrInvalidCLIReview) {
		t.Fatalf("repository restriction error=%v, want ErrInvalidCLIReview", err)
	}

	cancelled, err := postgres.RequestCLIReviewCancellation(ctx, readerPrincipal, submission.Run.ID, submission.Run.Revision)
	if err != nil || cancelled.CancelRequestedAt == nil || cancelled.Revision != submission.Run.Revision+1 {
		t.Fatalf("cancelled=%#v error=%v", cancelled, err)
	}
}
