package agentadapter

import (
	"context"
	"fmt"
)

// ExecutorIdentityPool reserves one distinct OS identity for each active
// coding job. The adapter retains provider credentials under its own identity;
// a coding CLI only receives the checkout while it owns a leased identity.
// This is a credential boundary, not a substitute for a per-job container.
type ExecutorIdentityPool struct {
	available chan executorIdentity
}

type executorIdentity struct {
	uid uint32
	gid uint32
}

func NewExecutorIdentityPool(firstUID, size int) (*ExecutorIdentityPool, error) {
	if firstUID < 10000 || firstUID > 60000 || size < 1 || size > 32 || firstUID+size > 60001 {
		return nil, fmt.Errorf("executor identity pool requires 1-32 UIDs in the 10000-60000 range")
	}
	pool := &ExecutorIdentityPool{available: make(chan executorIdentity, size)}
	for offset := 0; offset < size; offset++ {
		uid := uint32(firstUID + offset)
		pool.available <- executorIdentity{uid: uid, gid: uid}
	}
	return pool, nil
}

func (pool *ExecutorIdentityPool) acquire(ctx context.Context) (executorIdentity, func(), error) {
	if pool == nil {
		return executorIdentity{}, nil, fmt.Errorf("executor identity pool is not configured")
	}
	select {
	case identity := <-pool.available:
		return identity, func() { pool.available <- identity }, nil
	case <-ctx.Done():
		return executorIdentity{}, nil, ctx.Err()
	}
}
