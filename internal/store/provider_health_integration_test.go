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
	"github.com/jackc/pgx/v5"
)

func TestProviderHealthProbeFeedsTenantOverview(t *testing.T) {
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
	installationID := uuid.New()
	tenantSlug := "provider-health-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Provider Health Integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app')`, installationID, tenantID, "probe-"+installationID.String()); err != nil {
		t.Fatal(err)
	}
	target, err := postgres.ClaimProviderHealthProbe(ctx, "provider-prober-test", time.Minute, &installationID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	remaining := int64(4999)
	limit := int64(5000)
	reset := now.Add(time.Hour)
	if err := postgres.CompleteProviderHealthProbe(ctx, *target, domain.ProviderProbeResult{
		HealthState: domain.HealthLive, ObservedAt: now,
		Permissions:        []string{"repository_inventory:read"},
		RateLimitRemaining: &remaining, RateLimitLimit: &limit, RateLimitResetAt: &reset,
		LatencyMS: 42, Receipt: map[string]any{"schema": "open-review.provider-health-probe.v1", "scope_verification": "inventory_backed", "inventory_state": "partial"},
		Repositories: []domain.ProviderRepository{
			{ExternalID: "42", Name: "RainLib/open-review-platform", DefaultBranch: "main", Visibility: "private"},
			{ExternalID: "43", Name: "Other/hidden", DefaultBranch: "main", Visibility: "private"},
		},
	}, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	overview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Providers) != 1 {
		t.Fatalf("provider count=%d, want 1", len(overview.Providers))
	}
	provider := overview.Providers[0]
	if provider.State != domain.HealthLive || provider.LastProbeAt == nil || provider.ProbeLatencyMS != 42 || len(provider.Permissions) != 1 || provider.RateLimitRemaining == nil || *provider.RateLimitRemaining != remaining {
		t.Fatalf("unexpected provider health: %#v", provider)
	}
	foundObservedComponent := false
	for _, component := range overview.Components {
		if component.Key == "publisher" {
			foundObservedComponent = component.Observed && component.State == domain.HealthLive && component.SampledAt != nil
		}
	}
	if !foundObservedComponent {
		t.Fatalf("provider component did not reflect probe evidence: %#v", overview.Components)
	}
	installations, err := postgres.ListInstallations(ctx, "owner", tenantSlug, 10)
	if err != nil || len(installations) != 1 || installations[0].Verification == nil || installations[0].Verification.State != "completed" || installations[0].Verification.HealthState != domain.HealthLive || installations[0].Verification.Attempt != 1 || installations[0].Verification.InventoryCount != 1 || installations[0].Verification.InventoryState != "partial" || len(installations[0].Verification.Permissions) != 1 || installations[0].Verification.Permissions[0] != "repository_inventory:read" || installations[0].Verification.ObservedAt == nil {
		t.Fatalf("installation verification receipt=%#v error=%v", installations, err)
	}
	// The out-of-scope Other/hidden entry sorts first. Apply the result limit
	// after authorization filtering so it cannot hide the allowed repository.
	repositories, err := postgres.ListInstallationRepositories(ctx, "owner", tenantSlug, installationID, "", 1)
	if err != nil || len(repositories) != 1 || repositories[0].ExternalID != "42" || repositories[0].Name != "RainLib/open-review-platform" || repositories[0].LastSeenAt.IsZero() {
		t.Fatalf("scoped provider repository inventory=%#v error=%v", repositories, err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_repository_inventory (installation_id,external_id,name,last_seen_at)
		SELECT $1, 'bulk-' || n, 'RainLib/bulk-' || lpad(n::text, 4, '0'), $2
		FROM generate_series(1,501) AS n`, installationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_repository_inventory (installation_id,external_id,name,last_seen_at)
		VALUES ($1,'target','RainLib/z-target',$2),($1,'hidden-target','Other/z-target',$2),
		       ($1,'stale','RainLib/stale',now() - INTERVAL '1 day')`, installationID, now); err != nil {
		t.Fatal(err)
	}
	stale, err := postgres.ListInstallationRepositories(ctx, "owner", tenantSlug, installationID, "stale", 1)
	if err != nil || len(stale) != 0 {
		t.Fatalf("retained historical repository was selectable: %#v error=%v", stale, err)
	}
	installations, err = postgres.ListInstallations(ctx, "owner", tenantSlug, 10)
	if err != nil || len(installations) != 1 || installations[0].Verification == nil || installations[0].Verification.InventoryCount != 503 {
		t.Fatalf("current inventory count included stale or out-of-scope rows: %#v error=%v", installations, err)
	}
	firstPage, err := postgres.ListInstallationRepositories(ctx, "owner", tenantSlug, installationID, "", 500)
	if err != nil || len(firstPage) != 500 {
		t.Fatalf("first inventory page=%d error=%v", len(firstPage), err)
	}
	if firstPage[len(firstPage)-1].Name == "RainLib/z-target" {
		t.Fatal("target repository unexpectedly appeared in the first 500")
	}
	searched, err := postgres.ListInstallationRepositories(ctx, "owner", tenantSlug, installationID, "Z-TARGET", 1)
	if err != nil || len(searched) != 1 || searched[0].Name != "RainLib/z-target" {
		t.Fatalf("repository beyond first 500 or scope filter failed: %#v error=%v", searched, err)
	}
	for _, literal := range []string{"z%target", "z_target"} {
		matched, err := postgres.ListInstallationRepositories(ctx, "owner", tenantSlug, installationID, literal, 1)
		if err != nil || len(matched) != 0 {
			t.Fatalf("literal repository search %q matched %#v: %v", literal, matched, err)
		}
	}
	updated, err := postgres.UpdateInstallationRepositoryScope(ctx, "owner", tenantSlug, installationID, domain.InstallationRepositoryScopeInput{
		ExpectedRepositoryScope: "RainLib/*", RepositoryScope: "RainLib/open-review-platform",
	})
	if err != nil || updated.RepositoryScope != "RainLib/open-review-platform" || updated.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("update repository scope=%#v error=%v", updated, err)
	}
	var queuedProbeState, pendingInstallationState string
	if err := postgres.pool.QueryRow(ctx, `
		SELECT probe.state, installation.verification_state
		FROM provider_health_probes probe
		JOIN provider_installations installation ON installation.id=probe.installation_id
		WHERE installation.id=$1`, installationID).Scan(&queuedProbeState, &pendingInstallationState); err != nil || queuedProbeState != "queued" || pendingInstallationState != "pending" {
		t.Fatalf("scope change did not atomically suspend admission and queue verification: probe=%q installation=%q error=%v", queuedProbeState, pendingInstallationState, err)
	}
	if _, err := postgres.UpdateInstallationRepositoryScope(ctx, "owner", tenantSlug, installationID, domain.InstallationRepositoryScopeInput{
		ExpectedRepositoryScope: "RainLib/*", RepositoryScope: "RainLib/platform-api",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale repository scope update error=%v, want ErrConflict", err)
	}
	recheck, err := postgres.ClaimProviderHealthProbe(ctx, "provider-prober-scope-change", time.Minute, &installationID)
	if err != nil || recheck == nil || recheck.Installation.RepositoryScope != "RainLib/open-review-platform" {
		t.Fatalf("scope recheck target=%#v error=%v", recheck, err)
	}
	if _, err := postgres.UpdateInstallationRepositoryScope(ctx, "owner", tenantSlug, installationID, domain.InstallationRepositoryScopeInput{
		ExpectedRepositoryScope: "RainLib/open-review-platform", RepositoryScope: "RainLib/platform-api",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("in-flight scope change error=%v, want ErrConflict", err)
	}
	// An old worker can still report a healthy identity probe without proving
	// the newly selected scope. Previously retained inventory must not turn
	// that result into a verified installation.
	staleProbeAt := time.Now().UTC()
	if err := postgres.CompleteProviderHealthProbe(ctx, *recheck, domain.ProviderProbeResult{
		HealthState: domain.HealthLive, ObservedAt: staleProbeAt,
	}, staleProbeAt.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var probeErrorCode string
	if err := postgres.pool.QueryRow(ctx, `SELECT installation.verification_state, probe.error_code FROM provider_installations installation JOIN provider_health_probes probe ON probe.installation_id=installation.id WHERE installation.id=$1`, installationID).Scan(&pendingInstallationState, &probeErrorCode); err != nil || pendingInstallationState != "failed" || probeErrorCode != "repository_scope_unverified" {
		t.Fatalf("stale inventory verified the new scope: state=%q error_code=%q error=%v", pendingInstallationState, probeErrorCode, err)
	}
	if _, err := postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID); err != nil {
		t.Fatalf("retry after missing fresh scope evidence: %v", err)
	}
	recheck, err = postgres.ClaimProviderHealthProbe(ctx, "provider-prober-scope-change-retry", time.Minute, &installationID)
	if err != nil || recheck == nil {
		t.Fatalf("claim retried scope probe=%#v error=%v", recheck, err)
	}
	recheckedAt := time.Now().UTC()
	if err := postgres.CompleteProviderHealthProbe(ctx, *recheck, domain.ProviderProbeResult{
		HealthState: domain.HealthLive, ObservedAt: recheckedAt,
		Permissions:  []string{"repository_inventory:read"},
		Receipt:      map[string]any{"scope_verification": "verified"},
		Repositories: []domain.ProviderRepository{{ExternalID: "42", Name: "RainLib/open-review-platform", DefaultBranch: "main"}},
	}, recheckedAt.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT verification_state FROM provider_installations WHERE id=$1`, installationID).Scan(&pendingInstallationState); err != nil || pendingInstallationState != "verified" {
		t.Fatalf("scope recheck did not restore admission after provider success: state=%q error=%v", pendingInstallationState, err)
	}
}

func TestProviderHealthOverviewSurfacesMissingGitHubIssueEvent(t *testing.T) {
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
	tenantSlug := "provider-issue-event-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Provider Issue Event')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app')`, installationID, tenantID, "probe-"+installationID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	target, err := postgres.ClaimProviderHealthProbe(ctx, "provider-issue-event-test", time.Minute, &installationID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := postgres.CompleteProviderHealthProbe(ctx, *target, domain.ProviderProbeResult{
		HealthState: domain.HealthLive, ObservedAt: now,
		Permissions:  []string{"repository_inventory:read", "issue_triage:missing_event"},
		Receipt:      map[string]any{"schema": "open-review.provider-health-probe.v1", "issue_triage_state": "missing_event", "scope_verification": "inventory_backed"},
		Repositories: []domain.ProviderRepository{{ExternalID: "issue-event-repo", Name: "RainLib/open-review-platform"}},
	}, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	overview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || len(overview.Providers) != 1 {
		t.Fatalf("overview=%#v error=%v", overview, err)
	}
	provider := overview.Providers[0]
	if provider.State != domain.HealthDegraded || !strings.Contains(provider.Detail, "not subscribed to Issues events") {
		t.Fatalf("provider capability gap was not surfaced: %#v", provider)
	}
}

func TestInstallationVerificationRequestHasDurableRetryStates(t *testing.T) {
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
	tenantSlug := "verification-retry-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Verification Retry')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app')`, installationID, tenantID, "retry-"+installationID.String()); err != nil {
		t.Fatal(err)
	}

	requested, err := postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID)
	if err != nil || requested.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("initial verification request=%#v error=%v", requested, err)
	}
	target, err := postgres.ClaimProviderHealthProbe(ctx, "provider-prober-test", time.Minute, &installationID)
	if err != nil || target.Installation.VerificationState != domain.InstallationVerificationChecking {
		t.Fatalf("claimed verification target=%#v error=%v", target, err)
	}
	if _, err := postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID); !errors.Is(err, ErrConflict) {
		t.Fatalf("running verification requeue error=%v, want ErrConflict", err)
	}
	now := time.Now().UTC()
	if err := postgres.CompleteProviderHealthProbe(ctx, *target, domain.ProviderProbeResult{
		HealthState:  domain.HealthCritical,
		ObservedAt:   now,
		ErrorCode:    "provider_rejected",
		ErrorMessage: "provider rejected the read-only check",
		Receipt:      map[string]any{"schema": "open-review.provider-health-probe.v1"},
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("complete failed verification: %v", err)
	}
	requested, err = postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID)
	if err != nil || requested.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("retry verification request=%#v error=%v", requested, err)
	}
	var probeState string
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM provider_health_probes WHERE installation_id=$1`, installationID).Scan(&probeState); err != nil || probeState != "queued" {
		t.Fatalf("requeued probe state=%q error=%v", probeState, err)
	}

	// Once the connection is verified, an operator may refresh its provider
	// health without revoking existing webhook/CLI admission while the probe is
	// in flight. This is distinct from a failed installation retry above.
	target, err = postgres.ClaimProviderHealthProbe(ctx, "provider-prober-test", time.Minute, &installationID)
	if err != nil || target.Installation.VerificationState != domain.InstallationVerificationChecking {
		t.Fatalf("claim requeued verification target=%#v error=%v", target, err)
	}
	if err := postgres.CompleteProviderHealthProbe(ctx, *target, domain.ProviderProbeResult{
		HealthState:  domain.HealthLive,
		ObservedAt:   now.Add(time.Second),
		Receipt:      map[string]any{"schema": "open-review.provider-health-probe.v1", "scope_verification": "inventory_backed"},
		Repositories: []domain.ProviderRepository{{ExternalID: "retry-repo", Name: "RainLib/open-review-platform"}},
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("complete verified health probe: %v", err)
	}
	requested, err = postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID)
	if err != nil || requested.VerificationState != domain.InstallationVerificationVerified {
		t.Fatalf("verified health refresh request=%#v error=%v", requested, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT verification_state FROM provider_installations WHERE id=$1`, installationID).Scan(&probeState); err != nil || probeState != "verified" {
		t.Fatalf("verified refresh changed admission state=%q error=%v", probeState, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM provider_health_probes WHERE installation_id=$1`, installationID).Scan(&probeState); err != nil || probeState != "queued" {
		t.Fatalf("verified refresh probe state=%q error=%v", probeState, err)
	}
}

func TestVerifiedInstallationRevokesAdmissionOnPermanentProbeFailure(t *testing.T) {
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
	tenantSlug := "provider-revoke-" + tenantID.String()[:8]
	externalID := "provider-revoke-" + installationID.String()
	repository := "RainLib/probe-recovery-" + installationID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Provider Revocation')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,api_base_url,credential_ref,verification_state)
		VALUES ($1,$2,'github',$3,$4,TRUE,'https://api.github.com','github-app','verified')`, installationID, tenantID, externalID, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by,completed_at) VALUES ($1,'complete',1,'owner',now())`, tenantID); err != nil {
		t.Fatal(err)
	}
	claim := func(worker string) domain.ProviderProbeTarget {
		t.Helper()
		target, err := postgres.ClaimProviderHealthProbe(ctx, worker, time.Minute, &installationID)
		if err != nil || target == nil {
			t.Fatalf("claim provider probe=%#v error=%v", target, err)
		}
		return *target
	}
	complete := func(target domain.ProviderProbeTarget, result domain.ProviderProbeResult) {
		t.Helper()
		if err := postgres.CompleteProviderHealthProbe(ctx, target, result, result.ObservedAt.Add(time.Minute)); err != nil {
			t.Fatalf("complete provider probe: %v", err)
		}
	}
	state := func() domain.InstallationVerificationState {
		t.Helper()
		var value domain.InstallationVerificationState
		if err := postgres.pool.QueryRow(ctx, `SELECT verification_state FROM provider_installations WHERE id=$1`, installationID).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	first := claim("provider-revoke-degraded")
	complete(first, domain.ProviderProbeResult{HealthState: domain.HealthDegraded, ObservedAt: time.Now().UTC(), ErrorCode: "provider_temporarily_unavailable"})
	if got := state(); got != domain.InstallationVerificationVerified {
		t.Fatalf("transient failure revoked a verified installation: %q", got)
	}
	queued, _, err := postgres.Enqueue(ctx, inboundRoutingEvent(domain.ProviderGitHub, "https://api.github.com", externalID, repository, "https://github.com/"+repository+".git", 1))
	if err != nil {
		t.Fatalf("enqueue before revocation: %v", err)
	}
	var runID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM review_runs WHERE legacy_job_id=$1`, queued.ID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.AdvanceRun(ctx, runID, domain.RunAdmitted); err != nil {
		t.Fatalf("admit acknowledged run before revocation: %v", err)
	}
	if running, err := postgres.ClaimForRun(ctx, "runner-before-revocation", runID); err != nil || running == nil || running.ID != queued.ID {
		t.Fatalf("claim before revocation=%#v error=%v", running, err)
	}
	if _, err := postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID); err != nil {
		t.Fatal(err)
	}
	second := claim("provider-revoke-critical")
	complete(second, domain.ProviderProbeResult{HealthState: domain.HealthCritical, ObservedAt: time.Now().UTC(), ErrorCode: "repository_scope_not_authorized"})
	if got := state(); got != domain.InstallationVerificationFailed {
		t.Fatalf("permanent permission failure left admission enabled: %q", got)
	}
	if err := postgres.RenewClaim(ctx, queued.ID, "runner-before-revocation", time.Minute); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("revoked installation renewed an in-flight claim: %v", err)
	}
	if _, _, err := postgres.Enqueue(ctx, inboundRoutingEvent(domain.ProviderGitHub, "https://api.github.com", externalID, repository, "https://github.com/"+repository+".git", 2)); !errors.Is(err, ErrUnknownInstallation) {
		t.Fatalf("revoked installation admitted a webhook: %v", err)
	}
	var revocations int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='installation.verification_revoked' AND target=$2`, tenantID, installationID.String()).Scan(&revocations); err != nil || revocations != 1 {
		t.Fatalf("revocation audit count=%d error=%v", revocations, err)
	}
	if _, err := postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID); err != nil {
		t.Fatal(err)
	}
	third := claim("provider-revoke-recovered")
	complete(third, domain.ProviderProbeResult{
		HealthState: domain.HealthLive, ObservedAt: time.Now().UTC(),
		Receipt:      map[string]any{"scope_verification": "verified"},
		Repositories: []domain.ProviderRepository{{ExternalID: repository, Name: repository}},
	})
	if got := state(); got != domain.InstallationVerificationVerified {
		t.Fatalf("fresh provider authorization did not restore admission: %q", got)
	}
	if job, _, err := postgres.Enqueue(ctx, inboundRoutingEvent(domain.ProviderGitHub, "https://api.github.com", externalID, repository, "https://github.com/"+repository+".git", 3)); err != nil || job.InstallationID != installationID {
		t.Fatalf("recovered installation admission job=%#v error=%v", job, err)
	}
	if _, err := postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID); err != nil {
		t.Fatal(err)
	}
	legacyWorker := claim("provider-revoke-stale-worker")
	complete(legacyWorker, domain.ProviderProbeResult{
		HealthState: domain.HealthLive, ObservedAt: time.Now().UTC(),
		Repositories: []domain.ProviderRepository{{ExternalID: repository, Name: repository}},
	})
	if got := state(); got != domain.InstallationVerificationFailed {
		t.Fatalf("identity-only probe restored authorization without fresh scope receipt: %q", got)
	}
	var errorCode string
	if err := postgres.pool.QueryRow(ctx, `SELECT error_code FROM provider_health_probes WHERE installation_id=$1`, installationID).Scan(&errorCode); err != nil || errorCode != "repository_scope_unverified" {
		t.Fatalf("missing scope evidence code=%q error=%v", errorCode, err)
	}
}

func TestInstallationDeactivationStopsAdmissionAndInFlightVerification(t *testing.T) {
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
	tenantSlug := "deactivate-installation-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Installation Deactivation')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'reviewer','reviewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','pending')`, installationID, tenantID, "deactivate-"+installationID.String()); err != nil {
		t.Fatal(err)
	}

	if _, err := postgres.DeactivateInstallation(ctx, "reviewer", tenantSlug, installationID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer deactivation error=%v, want ErrForbidden", err)
	}
	target, err := postgres.ClaimProviderHealthProbe(ctx, "provider-prober-test", time.Minute, &installationID)
	if err != nil {
		t.Fatalf("claim verification before deactivation: %v", err)
	}
	deactivated, err := postgres.DeactivateInstallation(ctx, "owner", tenantSlug, installationID)
	if err != nil || deactivated.Active {
		t.Fatalf("deactivate result=%#v error=%v", deactivated, err)
	}
	if _, err := postgres.RequestInstallationVerification(ctx, "owner", tenantSlug, installationID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deactivated verification request error=%v, want ErrConflict", err)
	}
	if _, err := postgres.ClaimProviderHealthProbe(ctx, "provider-prober-test-2", time.Minute, &installationID); !errors.Is(err, ErrNoProviderHealthProbe) {
		t.Fatalf("deactivated probe claim error=%v, want ErrNoProviderHealthProbe", err)
	}

	now := time.Now().UTC()
	if err := postgres.CompleteProviderHealthProbe(ctx, *target, domain.ProviderProbeResult{
		HealthState: domain.HealthLive,
		ObservedAt:  now,
		Receipt:     map[string]any{"schema": "open-review.provider-health-probe.v1"},
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("complete in-flight probe: %v", err)
	}
	var active bool
	var verificationState domain.InstallationVerificationState
	if err := postgres.pool.QueryRow(ctx, `SELECT active,verification_state FROM provider_installations WHERE id=$1`, installationID).Scan(&active, &verificationState); err != nil {
		t.Fatal(err)
	}
	if active || verificationState != domain.InstallationVerificationChecking {
		t.Fatalf("installation state after in-flight probe active=%t verification=%q", active, verificationState)
	}
	var deactivationEvents int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='installation.deactivated' AND target=$2`, tenantID, installationID.String()).Scan(&deactivationEvents); err != nil {
		t.Fatal(err)
	}
	if deactivationEvents != 1 {
		t.Fatalf("deactivation audit events=%d, want 1", deactivationEvents)
	}
}

func TestReauthorizationReusesDeactivatedInstallationAndRequiresVerification(t *testing.T) {
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
	tenantSlug := "reauthorize-installation-" + tenantID.String()[:8]
	externalID := "reauthorize-" + installationID.String()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Installation reauthorization')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,minimum_severity,api_base_url,credential_ref,verification_state,active) VALUES ($1,$2,'github',$3,'RainLib/legacy',FALSE,'high','https://api.github.com','secret://old','verified',FALSE)`, installationID, tenantID, externalID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by) VALUES ($1,'complete',4,'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_health_probes (installation_id,state,worker_id,locked_until,health_state,permissions,receipt) VALUES ($1,'running','old-provider-prober',now()+interval '5 minutes','live','["repository_inventory:read"]'::jsonb,'{"previous":true}'::jsonb)`, installationID); err != nil {
		t.Fatal(err)
	}
	oldTarget := domain.ProviderProbeTarget{
		Installation: domain.Installation{ID: installationID, TenantID: tenantID},
		WorkerID:     "old-provider-prober",
	}
	automatic := true
	reauthorized, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, domain.InstallationInput{
		Provider: domain.ProviderGitHub, ExternalID: externalID, RepositoryScope: "RainLib/open-review-platform",
		AutomaticReviews: &automatic, MinimumSeverity: "medium", APIBaseURL: "https://api.github.com", CredentialRef: "secret://fresh",
	})
	if err != nil {
		t.Fatalf("reauthorize installation: %v", err)
	}
	if reauthorized.ID != installationID || !reauthorized.Active || reauthorized.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("reauthorized installation=%#v", reauthorized)
	}
	var scope, credentialRef string
	var automaticReviews bool
	var severity string
	if err := postgres.pool.QueryRow(ctx, `SELECT repository_scope,automatic_reviews,minimum_severity,credential_ref FROM provider_installations WHERE id=$1`, installationID).Scan(&scope, &automaticReviews, &severity, &credentialRef); err != nil {
		t.Fatal(err)
	}
	if scope != "RainLib/open-review-platform" || !automaticReviews || severity != "medium" || credentialRef != "secret://fresh" {
		t.Fatalf("reauthorized record scope=%q automatic=%t severity=%q credential=%q", scope, automaticReviews, severity, credentialRef)
	}
	if err := postgres.CompleteProviderHealthProbe(ctx, oldTarget, domain.ProviderProbeResult{
		HealthState: domain.HealthLive,
		ObservedAt:  time.Now().UTC(),
		Receipt:     map[string]any{"schema": "open-review.provider-health-probe.v1"},
	}, time.Now().UTC().Add(time.Minute)); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("old authorization probe completion error=%v, want ErrJobClaimLost", err)
	}
	var probeState string
	var probeWorkerID *string
	var healthState string
	if err := postgres.pool.QueryRow(ctx, `SELECT state,worker_id,health_state FROM provider_health_probes WHERE installation_id=$1`, installationID).Scan(&probeState, &probeWorkerID, &healthState); err != nil {
		t.Fatal(err)
	}
	if probeState != "queued" || probeWorkerID != nil || healthState != "configured_only" {
		t.Fatalf("reauthorized probe state=%q worker=%v health=%q", probeState, probeWorkerID, healthState)
	}
	if next, err := postgres.ClaimProviderHealthProbe(ctx, "fresh-provider-prober", time.Minute, &installationID); err != nil || next.Installation.VerificationState != domain.InstallationVerificationChecking || next.Installation.CredentialRef != "secret://fresh" {
		t.Fatalf("fresh authorization probe=%#v error=%v", next, err)
	}
	var setupEvents int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='workspace.setup_checkpoint.saved'`, tenantID).Scan(&setupEvents); err != nil {
		t.Fatal(err)
	}
	if setupEvents != 0 {
		t.Fatalf("reauthorization incorrectly restarted setup checkpoint events=%d", setupEvents)
	}
	var reauthorizationEvents int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='installation.reauthorized' AND target=$2`, tenantID, installationID.String()).Scan(&reauthorizationEvents); err != nil {
		t.Fatal(err)
	}
	if reauthorizationEvents != 1 {
		t.Fatalf("reauthorization audit events=%d, want 1", reauthorizationEvents)
	}
}

func TestGitLabScopesCanCoexistAndReauthorizeTheirOriginalInstallation(t *testing.T) {
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
	tenantSlug := "gitlab-scopes-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'GitLab Scope Installations')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	automatic := true
	apiBaseURL := "https://gitlab.example.test/api/v4"
	firstInput := domain.InstallationInput{
		Provider: domain.ProviderGitLab, ExternalID: "gitlab-scope-a-" + tenantID.String(),
		RepositoryScope: "group-a/*", AutomaticReviews: &automatic, MinimumSeverity: "medium",
		APIBaseURL: apiBaseURL, CredentialRef: "gitlab-token",
	}
	first, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, firstInput)
	if err != nil {
		t.Fatalf("create first GitLab scope: %v", err)
	}
	secondInput := domain.InstallationInput{
		Provider: domain.ProviderGitLab, ExternalID: "gitlab-scope-b-" + tenantID.String(),
		RepositoryScope: "group-b/*", AutomaticReviews: &automatic, MinimumSeverity: "medium",
		APIBaseURL: apiBaseURL, CredentialRef: "gitlab-token",
	}
	second, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, secondInput)
	if err != nil {
		t.Fatalf("create non-overlapping GitLab scope: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("distinct GitLab scopes reused installation id=%s", first.ID)
	}
	overlapping := domain.InstallationInput{
		Provider: domain.ProviderGitLab, ExternalID: "gitlab-scope-overlap-" + tenantID.String(),
		RepositoryScope: "group-a/repository", AutomaticReviews: &automatic, MinimumSeverity: "medium",
		APIBaseURL: apiBaseURL, CredentialRef: "gitlab-token",
	}
	if _, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, overlapping); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlapping GitLab scope error=%v, want ErrConflict", err)
	}
	if _, err := postgres.DeactivateInstallation(ctx, "owner", tenantSlug, first.ID); err != nil {
		t.Fatalf("deactivate first GitLab scope: %v", err)
	}
	reauthorized, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, firstInput)
	if err != nil {
		t.Fatalf("reauthorize GitLab scope: %v", err)
	}
	if reauthorized.ID != first.ID || !reauthorized.Active || reauthorized.VerificationState != domain.InstallationVerificationPending {
		t.Fatalf("reauthorized GitLab scope=%#v, want original pending active installation", reauthorized)
	}
}

func TestInstallationDeactivationPreventsQueuedAndRunningJobExecution(t *testing.T) {
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
	tenantID, installationID, queuedJobID, runningJobID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	queuedDeliveryID, runningDeliveryID := uuid.New(), uuid.New()
	tenantSlug := "deactivate-review-jobs-" + tenantID.String()[:8]
	queuedRequestID, runningRequestID := uuid.New(), uuid.New()
	queuedRunID, runningRunID := uuid.New(), uuid.New()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Installation queued review stop')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "review-stop-"+installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, queuedDeliveryID, "queued-delivery-"+queuedDeliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,available_at) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/queued','https://github.com/RainLib/queued.git',1,'main','base','head','head','queued',now()-interval '1 second')`, queuedJobID, tenantID, installationID, queuedDeliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/queued',1)`, queuedRequestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha) VALUES ($1,$2,$3,'admitted','pull_request','head','base')`, queuedRunID, queuedRequestID, queuedJobID)
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, runningDeliveryID, "running-delivery-"+runningDeliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,locked_by,locked_until) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/running','https://github.com/RainLib/running.git',2,'main','base','head','head','running','runner-before-deactivation',now()+interval '5 minutes')`, runningJobID, tenantID, installationID, runningDeliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/running',2)`, runningRequestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha) VALUES ($1,$2,$3,'analyzing','pull_request','head','base')`, runningRunID, runningRequestID, runningJobID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.DeactivateInstallation(ctx, "owner", tenantSlug, installationID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := postgres.ClaimForRun(ctx, "runner-after-deactivation", queuedRunID); !errors.Is(err, ErrNoQueuedJob) || claimed != nil {
		t.Fatalf("queued job claim after deactivation job=%#v error=%v", claimed, err)
	}
	if err := postgres.RenewClaim(ctx, runningJobID, "runner-before-deactivation", time.Minute); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("running job lease renewal after deactivation error=%v, want ErrJobClaimLost", err)
	}
	// Broker delivery may already have been consumed while the installation
	// was inactive. Prove this exact admitted run can be reclaimed after access
	// is restored; the global fair poller is covered separately and may select
	// another tenant's runnable job first in a shared integration database.
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=TRUE,verification_state='verified' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	recovered, err := postgres.ClaimForRun(ctx, "runner-after-restore", queuedRunID)
	if err != nil || recovered == nil || recovered.ID != queuedJobID {
		t.Fatalf("restored admitted job claim=%#v error=%v", recovered, err)
	}
}
