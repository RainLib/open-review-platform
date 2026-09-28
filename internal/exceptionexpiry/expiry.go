// Package exceptionexpiry restores issue visibility when a time-bounded rule
// exception expires. It has no provider, model, or queue capability.
package exceptionexpiry

import (
	"context"
	"fmt"
	"strings"
)

// Store is intentionally narrow so the expiry worker cannot accidentally gain
// authority to publish a review, alter a rule version, or call a provider.
type Store interface {
	ReconcileExpiredRuleExceptions(context.Context, int) (int, error)
}

type Processor struct {
	Store       Store
	TenantLimit int
	TaskStarted func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil {
		return false, fmt.Errorf("rule exception expiry store is required")
	}
	limit := p.TenantLimit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return false, fmt.Errorf("rule exception expiry tenant limit must be from 1 to 1000")
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	reconciled, err := p.Store.ReconcileExpiredRuleExceptions(ctx, limit)
	if err != nil {
		return false, err
	}
	return reconciled > 0, nil
}

// ParseTenantLimit keeps environment parsing outside the processor while
// retaining one consistent validation contract for command entry points.
func ParseTenantLimit(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed < 1 || parsed > 1000 {
		return fallback
	}
	return parsed
}
