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

func TestProviderOAuthCredentialTenantIsolation(t *testing.T) {
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

	tenantOneID, tenantTwoID := uuid.New(), uuid.New()
	tenantOneSlug := "oauth-one-" + tenantOneID.String()[:8]
	tenantTwoSlug := "oauth-two-" + tenantTwoID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO tenants (id, slug, name) VALUES
			($1, $2, 'OAuth Credential Tenant One'),
			($3, $4, 'OAuth Credential Tenant Two')`, tenantOneID, tenantOneSlug, tenantTwoID, tenantTwoSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO memberships (tenant_id, subject, role) VALUES
			($1, 'oauth-owner-one', 'owner'),
			($2, 'oauth-owner-two', 'owner')`, tenantOneID, tenantTwoID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = ANY($1::uuid[])`, []uuid.UUID{tenantOneID, tenantTwoID})
	}()

	credentialRef := "secret://provider/gitlab-oauth/" + uuid.NewString()
	expiresAt := time.Now().UTC().Add(time.Hour)
	input := domain.ProviderOAuthCredentialInput{
		CredentialRef:          credentialRef,
		Provider:               domain.ProviderGitLab,
		AccessTokenCiphertext:  []byte(strings.Repeat("access-ciphertext-", 3)),
		RefreshTokenCiphertext: []byte(strings.Repeat("refresh-ciphertext-", 2)),
		ExpiresAt:              &expiresAt,
	}

	created, err := postgres.CreateProviderOAuthCredential(ctx, "oauth-owner-one", tenantOneSlug, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.TenantID != tenantOneID || created.CredentialRef != credentialRef {
		t.Fatalf("created credential=%#v", created)
	}

	loaded, err := postgres.LoadProviderOAuthCredential(ctx, tenantOneID, credentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TenantID != tenantOneID || string(loaded.AccessTokenCiphertext) != string(input.AccessTokenCiphertext) {
		t.Fatalf("loaded credential=%#v", loaded)
	}
	if _, err := postgres.LoadProviderOAuthCredential(ctx, tenantTwoID, credentialRef); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant credential load error=%v, want ErrNotFound", err)
	}
	if _, err := postgres.CreateProviderOAuthCredential(ctx, "oauth-owner-two", tenantTwoSlug, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-tenant credential reuse error=%v, want ErrConflict", err)
	}
	refreshInput := domain.ProviderOAuthCredentialRefresh{
		ExpectedAccessTokenCiphertext: input.AccessTokenCiphertext,
		AccessTokenCiphertext:         []byte(strings.Repeat("refreshed-access-ciphertext-", 2)),
		RefreshTokenCiphertext:        []byte(strings.Repeat("refreshed-refresh-ciphertext-", 2)),
		ExpiresAt:                     time.Now().UTC().Add(2 * time.Hour),
	}
	if updated, err := postgres.RefreshProviderOAuthCredential(ctx, tenantTwoID, credentialRef, refreshInput); err != nil || updated {
		t.Fatalf("cross-tenant credential refresh updated=%t error=%v, want false nil", updated, err)
	}
	if updated, err := postgres.RefreshProviderOAuthCredential(ctx, tenantOneID, credentialRef, refreshInput); err != nil || !updated {
		t.Fatalf("tenant credential refresh updated=%t error=%v, want true nil", updated, err)
	}
	if updated, err := postgres.RefreshProviderOAuthCredential(ctx, tenantOneID, credentialRef, refreshInput); err != nil || updated {
		t.Fatalf("stale credential refresh updated=%t error=%v, want false nil", updated, err)
	}

	var auditMetadata string
	if err := postgres.pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE tenant_id=$1 AND action='provider_oauth_credential.stored' AND target=$2`, tenantOneID, credentialRef).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditMetadata, string(input.AccessTokenCiphertext)) || strings.Contains(auditMetadata, string(input.RefreshTokenCiphertext)) {
		t.Fatalf("audit metadata leaked encrypted credential material: %q", auditMetadata)
	}
}

func TestDeactivationRevokesOnlyOrphanedGitLabOAuthCredential(t *testing.T) {
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
	tenantSlug := "oauth-deactivation-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'OAuth deactivation')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	credentialRef := "secret://provider/gitlab-oauth/" + uuid.NewString()
	expiresAt := time.Now().UTC().Add(time.Hour)
	if _, err := postgres.CreateProviderOAuthCredential(ctx, "owner", tenantSlug, domain.ProviderOAuthCredentialInput{
		CredentialRef: credentialRef, Provider: domain.ProviderGitLab,
		AccessTokenCiphertext: []byte(strings.Repeat("access-ciphertext-", 3)),
		ExpiresAt:             &expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	firstID, secondID := uuid.New(), uuid.New()
	for _, item := range []struct {
		id    uuid.UUID
		scope string
	}{{firstID, "team/first"}, {secondID, "team/second"}} {
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state)
			VALUES ($1,$2,'gitlab',$3,$4,'https://gitlab.example/api/v4',$5,'verified')`,
			item.id, tenantID, item.id.String(), item.scope, credentialRef); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := postgres.DeactivateInstallation(ctx, "owner", tenantSlug, firstID); err != nil {
		t.Fatal(err)
	}
	credential, err := postgres.LoadProviderOAuthCredential(ctx, tenantID, credentialRef)
	if err != nil || credential.RevokedAt != nil {
		t.Fatalf("shared credential revoked before last active scope: revoked=%v error=%v", credential.RevokedAt, err)
	}
	if _, err := postgres.DeactivateInstallation(ctx, "owner", tenantSlug, secondID); err != nil {
		t.Fatal(err)
	}
	credential, err = postgres.LoadProviderOAuthCredential(ctx, tenantID, credentialRef)
	if err != nil || credential.RevokedAt == nil {
		t.Fatalf("orphaned credential remains usable: revoked=%v error=%v", credential.RevokedAt, err)
	}
	updated, err := postgres.RefreshProviderOAuthCredential(ctx, tenantID, credentialRef, domain.ProviderOAuthCredentialRefresh{
		ExpectedAccessTokenCiphertext: credential.AccessTokenCiphertext,
		AccessTokenCiphertext:         []byte(strings.Repeat("refreshed-ciphertext-", 2)),
		ExpiresAt:                     expiresAt.Add(time.Hour),
	})
	if err != nil || updated {
		t.Fatalf("revoked credential refresh updated=%t error=%v", updated, err)
	}
	var revocations int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='provider_oauth_credential.revoked' AND target=$2`, tenantID, credentialRef).Scan(&revocations); err != nil || revocations != 1 {
		t.Fatalf("credential revocation audits=%d error=%v", revocations, err)
	}
	if _, err := postgres.DeactivateInstallation(ctx, "owner", tenantSlug, secondID); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated deactivation error=%v, want ErrConflict", err)
	}
}

func TestOrphanedGitLabOAuthCleanupKeepsActiveAndFreshAuthorizations(t *testing.T) {
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
	tenantSlug := "oauth-orphans-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'OAuth orphan cleanup')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	refs := []string{
		"secret://provider/gitlab-oauth/" + uuid.NewString(),
		"secret://provider/gitlab-oauth/" + uuid.NewString(),
		"secret://provider/gitlab-oauth/" + uuid.NewString(),
	}
	for _, ref := range refs {
		if _, err := postgres.CreateProviderOAuthCredential(ctx, "owner", tenantSlug, domain.ProviderOAuthCredentialInput{
			CredentialRef: ref, Provider: domain.ProviderGitLab,
			AccessTokenCiphertext: []byte(strings.Repeat("access-ciphertext-", 3)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The first receipt was abandoned, the second completed an installation,
	// and the third is a still-valid in-progress setup attempt.
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_oauth_credentials SET created_at=now()-interval '2 hours' WHERE credential_ref=ANY($1::text[])`, refs[:2]); err != nil {
		t.Fatal(err)
	}
	automatic := true
	installationInput := domain.InstallationInput{
		Provider: domain.ProviderGitLab, ExternalID: "oauth-active-" + uuid.NewString(),
		RepositoryScope: "team/active", AutomaticReviews: &automatic,
		MinimumSeverity: "medium", APIBaseURL: "https://gitlab.example/api/v4",
		CredentialRef: refs[1],
	}
	if _, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, installationInput); err != nil {
		t.Fatalf("connect valid OAuth credential: %v", err)
	}
	if _, err := postgres.RevokeOrphanedProviderOAuthCredentials(ctx, time.Time{}, 100); !errors.Is(err, ErrConflict) {
		t.Fatalf("zero cutoff error=%v, want ErrConflict", err)
	}
	if _, err := postgres.RevokeOrphanedProviderOAuthCredentials(ctx, time.Now(), 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("zero batch error=%v, want ErrConflict", err)
	}
	cutoff := time.Now().UTC().Add(-time.Hour)
	revoked, err := postgres.RevokeOrphanedProviderOAuthCredentials(ctx, cutoff, 100)
	if err != nil || revoked != 1 {
		t.Fatalf("orphan cleanup revoked=%d error=%v, want one", revoked, err)
	}
	if again, err := postgres.RevokeOrphanedProviderOAuthCredentials(ctx, cutoff, 100); err != nil || again != 0 {
		t.Fatalf("repeated orphan cleanup revoked=%d error=%v", again, err)
	}
	for index, ref := range refs {
		credential, err := postgres.LoadProviderOAuthCredential(ctx, tenantID, ref)
		if err != nil {
			t.Fatal(err)
		}
		if (credential.RevokedAt != nil) != (index == 0) {
			t.Fatalf("credential %d revoked=%t, want %t", index, credential.RevokedAt != nil, index == 0)
		}
	}
	installationInput.ExternalID = "oauth-revoked-" + uuid.NewString()
	installationInput.RepositoryScope = "team/revoked"
	installationInput.CredentialRef = refs[0]
	if _, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, installationInput); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked OAuth reference accepted: %v", err)
	}
	installationInput.CredentialRef = "secret://provider/gitlab-oauth/" + uuid.NewString()
	if _, err := postgres.CreateInstallation(ctx, "owner", tenantSlug, installationInput); !errors.Is(err, ErrConflict) {
		t.Fatalf("nonexistent OAuth reference accepted: %v", err)
	}
	var audits int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='provider_oauth_credential.revoked' AND target=$2 AND metadata->>'reason'='orphaned_authorization'`, tenantID, refs[0]).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("orphan cleanup audit count=%d error=%v", audits, err)
	}
}
