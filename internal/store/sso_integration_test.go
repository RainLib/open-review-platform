package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestSSOControlPlaneRequiresProbeDomainMappingAndBreakGlassOwner(t *testing.T) {
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
	tenantSlug := "sso-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'SSO Integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'admin','admin'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	overview, err := postgres.GetSSOOverview(ctx, "owner", tenantSlug)
	if err != nil || overview.Configuration != nil || overview.Readiness.Ready || len(overview.Readiness.Blockers) != 3 {
		t.Fatalf("initial overview=%#v error=%v", overview, err)
	}
	if _, err := postgres.GetSSOOverview(ctx, "viewer", tenantSlug); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer overview error=%v, want ErrForbidden", err)
	}

	overview, err = postgres.SaveSSOConfiguration(ctx, "admin", tenantSlug, domain.SSOConfigurationInput{
		Protocol: domain.SSOProtocolOIDC, DisplayName: "Acme workforce", IssuerURL: "https://id.example.com",
		ClientID: "open-review", SecretRef: "secret://sso/acme", GroupClaim: "teams", ExpectedRevision: 0,
	})
	if err != nil || overview.Configuration == nil || overview.Configuration.Revision != 1 || !overview.Configuration.SecretConfigured || overview.Configuration.State != domain.SSOStateDraftSaved {
		t.Fatalf("saved overview=%#v error=%v", overview, err)
	}
	receipt, err := postgres.RequestSSOProbe(ctx, "admin", tenantSlug, 1)
	if err != nil || receipt.State != domain.SSOProbeQueued {
		t.Fatalf("probe receipt=%#v error=%v", receipt, err)
	}
	target, err := postgres.ClaimSSOProbe(ctx, "probe-worker")
	if err != nil || target.ReceiptID != receipt.ID || target.ExpectedClientID != "open-review" {
		t.Fatalf("probe target=%#v error=%v", target, err)
	}
	if err := postgres.CompleteSSOProbe(ctx, receipt.ID, "probe-worker", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", ""); err != nil {
		t.Fatal(err)
	}

	domainItem, err := postgres.AddSSODomain(ctx, "admin", tenantSlug, "engineering.example.com")
	if err != nil || domainItem.State != "pending" {
		t.Fatalf("domain=%#v error=%v", domainItem, err)
	}
	if _, err := postgres.VerifySSODomain(ctx, "admin", tenantSlug, domainItem.ID, []string{"wrong"}); !errors.Is(err, ErrSSODomainUnverified) {
		t.Fatalf("wrong domain challenge error=%v", err)
	}
	verifiedDomain, err := postgres.VerifySSODomain(ctx, "admin", tenantSlug, domainItem.ID, []string{domainItem.ChallengeToken})
	if err != nil || verifiedDomain.State != "verified" {
		t.Fatalf("verified domain=%#v error=%v", verifiedDomain, err)
	}
	mapping, err := postgres.CreateSSORoleMapping(ctx, "admin", tenantSlug, domain.SSORoleMappingInput{GroupValue: "platform-engineering", Role: "reviewer", RepositoryScope: "RainLib/*"})
	if err != nil || mapping.Revision != 1 {
		t.Fatalf("mapping=%#v error=%v", mapping, err)
	}
	overview, err = postgres.GetSSOOverview(ctx, "owner", tenantSlug)
	if err != nil || !overview.Readiness.Ready || overview.Configuration.State != domain.SSOStateReady || overview.Configuration.TestedRevision == nil || *overview.Configuration.TestedRevision != 1 {
		t.Fatalf("ready overview=%#v error=%v", overview, err)
	}
	if _, err := postgres.EnforceSSO(ctx, "admin", tenantSlug, domain.SSOEnforcementInput{ExpectedRevision: 1, BreakGlassSubject: "owner"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin enforcement error=%v, want ErrForbidden", err)
	}
	overview, err = postgres.EnforceSSO(ctx, "owner", tenantSlug, domain.SSOEnforcementInput{ExpectedRevision: 1, BreakGlassSubject: "owner"})
	if err != nil || overview.Configuration.State != domain.SSOStateEnforced || overview.Configuration.BreakGlass != "owner" {
		t.Fatalf("enforced overview=%#v error=%v", overview, err)
	}
	if _, err := postgres.SaveSSOConfiguration(ctx, "owner", tenantSlug, domain.SSOConfigurationInput{Protocol: domain.SSOProtocolOIDC, DisplayName: "Changed", IssuerURL: "https://id.example.com", ClientID: "open-review", KeepSecret: true, ExpectedRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("edit enforced configuration error=%v, want ErrConflict", err)
	}
	overview, err = postgres.SuspendSSO(ctx, "owner", tenantSlug, 1)
	if err != nil || overview.Configuration.State != domain.SSOStateSuspended {
		t.Fatalf("suspended overview=%#v error=%v", overview, err)
	}
}
