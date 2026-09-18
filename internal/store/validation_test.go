package store

import (
	"context"
	"errors"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestManagementValidationErrorsAreSentinels(t *testing.T) {
	postgres := &PostgresStore{}
	ctx := context.Background()

	if _, err := postgres.CreateRuleBinding(ctx, "operator", "acme", domain.RuleBindingInput{}); !errors.Is(err, ErrInvalidRuleBinding) {
		t.Fatalf("CreateRuleBinding error=%v, want ErrInvalidRuleBinding", err)
	}
	if _, err := postgres.UpdateRuleBinding(ctx, "operator", "acme", uuid.Nil, domain.RuleBindingUpdateInput{}); !errors.Is(err, ErrInvalidRuleBinding) {
		t.Fatalf("UpdateRuleBinding error=%v, want ErrInvalidRuleBinding", err)
	}
	if _, err := postgres.UpsertProviderIdentity(ctx, "operator", "acme", domain.ProviderIdentity{}); !errors.Is(err, ErrInvalidProviderIdentity) {
		t.Fatalf("UpsertProviderIdentity error=%v, want ErrInvalidProviderIdentity", err)
	}
	if _, err := postgres.CreateRuleSet(ctx, "operator", "acme", domain.RuleSetInput{}); !errors.Is(err, ErrInvalidRuleSet) {
		t.Fatalf("CreateRuleSet error=%v, want ErrInvalidRuleSet", err)
	}
	if _, err := postgres.RequestRuleApproval(ctx, "operator", "acme", uuid.Nil, 0, domain.RuleApprovalRequestInput{}); !errors.Is(err, ErrInvalidRuleApproval) {
		t.Fatalf("RequestRuleApproval error=%v, want ErrInvalidRuleApproval", err)
	}
	if _, err := postgres.DecideRuleApproval(ctx, "operator", "acme", uuid.Nil, domain.RuleApprovalDecisionInput{}); !errors.Is(err, ErrInvalidRuleApproval) {
		t.Fatalf("DecideRuleApproval error=%v, want ErrInvalidRuleApproval", err)
	}
}
