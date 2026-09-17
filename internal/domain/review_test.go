package domain

import "testing"

func TestValidRoleMatchesEnterpriseRBACContract(t *testing.T) {
	for _, role := range []string{"owner", "admin", "rule_admin", "reviewer", "viewer", "billing_viewer"} {
		if !ValidRole(role) {
			t.Fatalf("expected %q to be valid", role)
		}
	}
	if ValidRole("superuser") {
		t.Fatal("unexpected unbounded role")
	}
}
