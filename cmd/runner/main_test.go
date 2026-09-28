package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeQueueConsumer struct {
	consume func(context.Context) error
	closed  *atomic.Int32
}

func (c fakeQueueConsumer) Consume(ctx context.Context, _, _ string, _ func(context.Context, []byte) error) error {
	return c.consume(ctx)
}

func (c fakeQueueConsumer) Close() error {
	c.closed.Add(1)
	return nil
}

func TestConsumeQueueReconnectsAfterDeliveryChannelCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var opened, closed atomic.Int32
	secondReady := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeQueue(ctx, "amqp://test", "events", "review", "runner", func(context.Context, []byte) error { return nil },
			func(_, _ string) (queueConsumer, error) {
				if opened.Add(1) == 1 {
					return fakeQueueConsumer{consume: func(context.Context) error { return errors.New("delivery channel closed") }, closed: &closed}, nil
				}
				return fakeQueueConsumer{consume: func(ctx context.Context) error {
					close(secondReady)
					<-ctx.Done()
					return ctx.Err()
				}, closed: &closed}, nil
			}, time.Millisecond)
	}()
	select {
	case <-secondReady:
	case <-ctx.Done():
		t.Fatal("consumer did not reconnect")
	}
	cancel()
	<-done
	if opened.Load() != 2 || closed.Load() != 2 {
		t.Fatalf("opened=%d closed=%d, want two complete consumer lifecycles", opened.Load(), closed.Load())
	}
}

func TestConsumeQueueRetriesInitialBrokerFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var opened, closed atomic.Int32
	connected := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeQueue(ctx, "amqp://test", "events", "review", "runner", func(context.Context, []byte) error { return nil },
			func(_, _ string) (queueConsumer, error) {
				if opened.Add(1) == 1 {
					return nil, errors.New("broker unavailable")
				}
				return fakeQueueConsumer{consume: func(ctx context.Context) error {
					close(connected)
					<-ctx.Done()
					return ctx.Err()
				}, closed: &closed}, nil
			}, time.Millisecond)
	}()
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("consumer did not connect after broker recovery")
	}
	cancel()
	<-done
	if opened.Load() != 2 || closed.Load() != 1 {
		t.Fatalf("opened=%d closed=%d, want one failed open and one closed consumer", opened.Load(), closed.Load())
	}
}
