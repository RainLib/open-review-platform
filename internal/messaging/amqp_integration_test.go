package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// TestAMQPQuorumQueuePrioritizesSecurityWithoutStarvingStandard verifies the
// deployed RabbitMQ 4.x two-band quorum scheduler rather than assuming that a
// numeric AMQP priority creates 32 independent classes. Security messages use
// priority 8 (high band); standard/deep work uses 4 (normal band). Under a
// fully queued burst, high work must lead while normal work still receives
// service before the high backlog drains.
func TestAMQPQuorumQueuePrioritizesSecurityWithoutStarvingStandard(t *testing.T) {
	brokerURL := os.Getenv("OPEN_REVIEW_TEST_AMQP_URL")
	if brokerURL == "" {
		t.Skip("OPEN_REVIEW_TEST_AMQP_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connection, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()

	suffix := uuid.NewString()
	exchange := "openreview.test.priority." + suffix
	queue := "openreview.test.priority." + suffix
	if err := channel.ExchangeDeclare(exchange, "direct", false, true, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueueDeclare(queue, true, false, false, false, amqp.Table{
		"x-queue-type":     "quorum",
		"x-delivery-limit": int32(5),
	}); err != nil {
		t.Fatal(err)
	}
	if err := channel.QueueBind(queue, "review.run.admitted", exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = channel.QueueDelete(queue, false, false, false)
		_ = channel.ExchangeDelete(exchange, false, false)
	}()
	if err := channel.Confirm(false); err != nil {
		t.Fatal(err)
	}

	const normalCount = 12
	const highCount = 24
	for index := 0; index < normalCount; index++ {
		if err := publishPriorityFixture(ctx, channel, exchange, "normal-"+strconv.Itoa(index), 4); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < highCount; index++ {
		if err := publishPriorityFixture(ctx, channel, exchange, "high-"+strconv.Itoa(index), 8); err != nil {
			t.Fatal(err)
		}
	}
	if err := channel.Qos(1, 0, false); err != nil {
		t.Fatal(err)
	}
	deliveries, err := channel.Consume(queue, "priority-fairness-"+suffix, false, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	highSeen, normalSeen := 0, 0
	firstWindowHigh, firstWindowNormal := 0, 0
	firstNormal := -1
	for index := 0; index < normalCount+highCount; index++ {
		select {
		case delivery, ok := <-deliveries:
			if !ok {
				t.Fatal("priority delivery channel closed")
			}
			if delivery.Priority > 4 {
				highSeen++
				if index < 9 {
					firstWindowHigh++
				}
			} else {
				normalSeen++
				if index < 9 {
					firstWindowNormal++
				}
				if firstNormal == -1 {
					firstNormal = index
				}
			}
			if err := delivery.Ack(false); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatalf("timed out draining priority burst after high=%d normal=%d: %v", highSeen, normalSeen, ctx.Err())
		}
	}
	if highSeen != highCount || normalSeen != normalCount {
		t.Fatalf("drained high=%d normal=%d, want %d/%d", highSeen, normalSeen, highCount, normalCount)
	}
	if firstWindowHigh < 5 || firstWindowNormal < 1 {
		t.Fatalf("first nine deliveries high=%d normal=%d, want high-majority service without normal starvation", firstWindowHigh, firstWindowNormal)
	}
	if firstNormal < 0 || firstNormal >= highCount {
		t.Fatalf("normal work first appeared at index %d; high backlog drained before normal work received service", firstNormal)
	}
	if firstNormal > 5 {
		t.Fatalf("normal work first appeared too late at index %d under the RabbitMQ 2:1 high/normal scheduler", firstNormal)
	}
}

func publishPriorityFixture(ctx context.Context, channel *amqp.Channel, exchange, body string, priority uint8) error {
	confirmation, err := channel.PublishWithDeferredConfirmWithContext(ctx, exchange, "review.run.admitted", false, false, amqp.Publishing{
		ContentType:  "text/plain",
		DeliveryMode: amqp.Persistent,
		Priority:     priority,
		Body:         []byte(body),
	})
	if err != nil {
		return err
	}
	accepted, err := confirmation.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !accepted {
		return fmt.Errorf("RabbitMQ negatively acknowledged %s", body)
	}
	return nil
}

// This test exercises the broker contract rather than mocking it. It remains
// opt-in because normal unit runs must not require a local RabbitMQ instance.
func TestAMQPPublisherConfirmsRoutedMessagesAndRejectsReturns(t *testing.T) {
	brokerURL := os.Getenv("OPEN_REVIEW_TEST_AMQP_URL")
	if brokerURL == "" {
		t.Skip("OPEN_REVIEW_TEST_AMQP_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exchange := "openreview.test.publisher-confirm." + uuid.NewString()
	publisher, err := OpenAMQPPublisher(brokerURL, exchange)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()

	consumerConnection, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer consumerConnection.Close()

	queueName := "openreview.test.publisher-confirm." + uuid.NewString()
	consumerChannel, err := consumerConnection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer consumerChannel.Close()
	defer func() { _ = consumerChannel.ExchangeDelete(exchange, false, false) }()
	if _, err := consumerChannel.QueueDeclare(queueName, false, true, true, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := consumerChannel.QueueBind(queueName, "review.test.routed", exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	deliveries, err := consumerChannel.Consume(queueName, "publisher-confirm-integration", false, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	routed := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: uuid.New(), Topic: "review.test.routed", Payload: map[string]any{"result": "accepted"},
	}
	if err := publisher.Publish(ctx, routed); err != nil {
		t.Fatalf("publish routed message: %v", err)
	}
	select {
	case delivery := <-deliveries:
		defer delivery.Ack(false)
		var envelope struct {
			MessageID string `json:"message_id"`
			Topic     string `json:"topic"`
		}
		if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.MessageID != routed.ID.String() || envelope.Topic != routed.Topic {
			t.Fatalf("unexpected routed envelope: %#v", envelope)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for routed message")
	}

	// A broker or Docker network interruption closes the process' AMQP channel
	// while the outbox relay itself remains alive. The next retained row must
	// restore the connection and publish without requiring a container restart.
	if err := publisher.channel.Close(); err != nil {
		t.Fatal(err)
	}
	recovered := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: uuid.New(), Topic: "review.test.routed", Payload: map[string]any{"result": "recovered"},
	}
	if err := publisher.Publish(ctx, recovered); err != nil {
		t.Fatalf("publish after channel interruption: %v", err)
	}
	select {
	case delivery := <-deliveries:
		defer delivery.Ack(false)
		var envelope struct {
			MessageID string `json:"message_id"`
		}
		if err := json.Unmarshal(delivery.Body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.MessageID != recovered.ID.String() {
			t.Fatalf("recovered envelope=%#v, want %s", envelope, recovered.ID)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for recovered routed message")
	}

	// A cancelled or timed-out publish has an ambiguous broker outcome. The
	// publisher must abandon that channel before another outbox row is sent.
	cancelledCtx, cancelPublish := context.WithCancel(ctx)
	cancelPublish()
	if err := publisher.Publish(cancelledCtx, domain.OutboxMessage{ID: uuid.New(), AggregateID: uuid.New(), Topic: "review.test.routed"}); err == nil {
		t.Fatal("cancelled publish unexpectedly succeeded")
	}
	if publisher.channel != nil || publisher.connection != nil {
		t.Fatal("ambiguous publish retained a channel that can misattribute a late confirmation")
	}
	afterCancellation := domain.OutboxMessage{ID: uuid.New(), AggregateID: uuid.New(), Topic: "review.test.routed", Payload: map[string]any{"result": "after-cancellation"}}
	if err := publisher.Publish(ctx, afterCancellation); err != nil {
		t.Fatalf("publish after cancelled confirmation: %v", err)
	}
	select {
	case delivery := <-deliveries:
		defer delivery.Ack(false)
		var envelope struct {
			MessageID string `json:"message_id"`
		}
		if err := json.Unmarshal(delivery.Body, &envelope); err != nil || envelope.MessageID != afterCancellation.ID.String() {
			t.Fatalf("reconnected envelope=%#v, error=%v", envelope, err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for publish after cancellation")
	}

	unroutable := domain.OutboxMessage{ID: uuid.New(), AggregateID: uuid.New(), Topic: "review.test.unroutable", Payload: map[string]any{"result": "retry"}}
	if err := publisher.Publish(ctx, unroutable); err == nil || !strings.Contains(err.Error(), "unroutable") {
		t.Fatalf("unroutable publish error=%v, want returned-message error", err)
	}
}
