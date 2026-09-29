package agentadapter

import (
	"context"
	"testing"
	"time"
)

func TestExecutorIdentityPoolHasExclusiveLeases(t *testing.T) {
	pool, err := NewExecutorIdentityPool(10002, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, releaseFirst, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.uid == second.uid {
		t.Fatal("concurrent coding jobs received the same executor identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := pool.acquire(ctx); err == nil {
		t.Fatal("exhausted executor identity pool admitted a third coding job")
	}
	releaseFirst()
	third, releaseThird, err := pool.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third.uid != first.uid {
		t.Fatalf("released executor identity was not returned to the pool: %d", third.uid)
	}
	releaseThird()
	releaseSecond()
}

func TestExecutorIdentityPoolRejectsUnsafeRanges(t *testing.T) {
	for _, test := range []struct{ first, size int }{
		{0, 1}, {9999, 1}, {10002, 0}, {10002, 33}, {60000, 2},
	} {
		if _, err := NewExecutorIdentityPool(test.first, test.size); err == nil {
			t.Fatalf("accepted unsafe executor identity range: %+v", test)
		}
	}
}
