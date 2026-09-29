package domain

import "testing"

func TestDataGovernanceInputsRejectUnsafeScopesAndJobs(t *testing.T) {
	if _, ok := NormalizeRetentionPolicyInput(RetentionPolicyInput{ScopeKind: DataScopeTenant, ScopeRef: "repo", DataClass: DataClassAudit, RetentionDays: 365, ChangeReason: "baseline"}); ok {
		t.Fatal("tenant scope must not accept a scope ref")
	}
	if normalized, ok := NormalizeRetentionPolicyInput(RetentionPolicyInput{ScopeKind: DataScopeRepository, ScopeRef: "RainLib/open-review", DataClass: DataClassFindings, RetentionDays: 90, ChangeReason: "Product policy"}); !ok || normalized.ScopeRef == "" {
		t.Fatalf("repository policy=%#v valid=%v", normalized, ok)
	}
	if _, ok := NormalizeDataLegalHoldInput(DataLegalHoldInput{ScopeKind: DataScopeRepository, ScopeRef: "RainLib/open-review", DataClass: DataClassAll, Reason: "Litigation preservation"}); !ok {
		t.Fatal("repository-wide legal hold should be valid")
	}
	if _, ok := NormalizeDataGovernanceJobInput(DataGovernanceJobInput{Kind: DataJobDeletion, ScopeKind: DataScopeTenant, DataClasses: []DataClass{DataClassAudit}, IdempotencyKey: "delete-0001", Reason: "Customer request"}); !ok {
		t.Fatal("tenant deletion request should be valid")
	}
	if _, ok := NormalizeDataGovernanceJobInput(DataGovernanceJobInput{Kind: DataJobRegionMigration, ScopeKind: DataScopeTenant, DesiredRegion: "../../etc", IdempotencyKey: "region-0001", Reason: "Residency requirement"}); ok {
		t.Fatal("unsafe region must be rejected")
	}
	if _, ok := NormalizeDataGovernanceJobInput(DataGovernanceJobInput{Kind: DataJobExport, ScopeKind: DataScopeTenant, DataClasses: []DataClass{DataClassAll}, IdempotencyKey: "export-0001", Reason: "DSAR"}); ok {
		t.Fatal("jobs must enumerate concrete data classes")
	}
	if _, ok := NormalizeDataGovernanceJobInput(DataGovernanceJobInput{Kind: DataJobDeletion, ScopeKind: DataScopeReviewRun, ScopeRef: "9d5fe8db-6db2-4431-98c6-d39b9c15fddf", DataClasses: []DataClass{DataClassFindings}, IdempotencyKey: "delete-run-0001", Reason: "unsafe unresolved hold scope"}); ok {
		t.Fatal("destructive review-run scopes must stay closed until legal-hold intersection is resolvable")
	}
	if _, ok := NormalizeDataGovernanceJobInput(DataGovernanceJobInput{Kind: DataJobDeletion, ScopeKind: DataScopeTenant, DataClasses: []DataClass{DataClassUsage}, IdempotencyKey: "delete-usage-0001", Reason: "would mutate immutable ledger"}); ok {
		t.Fatal("usage ledger deletion must fail closed until a compliant archive/redaction adapter exists")
	}
	if _, ok := NormalizeDataGovernanceJobInput(DataGovernanceJobInput{Kind: DataJobExport, ScopeKind: DataScopeAuditRange, ScopeRef: "2026-01-01T00:00:00Z/2026-02-01T00:00:00Z", DataClasses: []DataClass{DataClassAudit}, IdempotencyKey: "export-range-0001", Reason: "bounded audit export"}); !ok {
		t.Fatal("well-formed audit-range export should be valid")
	}
	if _, ok := NormalizeDataGovernanceJobInput(DataGovernanceJobInput{Kind: DataJobExport, ScopeKind: DataScopeAuditRange, ScopeRef: "last month", DataClasses: []DataClass{DataClassAudit}, IdempotencyKey: "export-range-0002", Reason: "ambiguous audit export"}); ok {
		t.Fatal("ambiguous audit range must be rejected at admission")
	}
}
