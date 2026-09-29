package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/notification"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

type notificationFlowPublisher struct {
	channel  *amqp.Channel
	exchange string
}

func (p notificationFlowPublisher) Publish(ctx context.Context, message domain.OutboxMessage) error {
	body, err := json.Marshal(map[string]any{
		"message_id": message.ID.String(), "aggregate_id": message.AggregateID.String(),
		"topic": message.Topic, "payload": message.Payload,
	})
	if err != nil {
		return err
	}
	return p.channel.PublishWithContext(ctx, p.exchange, message.Topic, false, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: message.ID.String(), Body: body,
	})
}

type notificationFlowResolver struct{ credential notification.Credential }

func (r notificationFlowResolver) Resolve(context.Context, string) (notification.Credential, error) {
	return r.credential, nil
}

// This opt-in test crosses the durable outbox, the relay claim/confirmation
// boundary, a real RabbitMQ exchange and queue, the exactly-once inbox, the
// notifier HTTP transport, and the delivery ledger. It uses an isolated
// database and unique broker topology; it does not claim third-party provider
// acceptance.
func TestNotificationOutboxBrokerConsumerAndReceipt(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	brokerURL := os.Getenv("OPEN_REVIEW_TEST_AMQP_URL")
	if databaseURL == "" || brokerURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL and OPEN_REVIEW_TEST_AMQP_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	postgres, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	cleanupPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupPool.Close()

	actor := "notification-flow-owner"
	tenant, err := postgres.CreateTenant(ctx, actor, "notification-flow-"+uuid.NewString()[:8], "Notification flow")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := postgres.CreateNotificationDestination(ctx, actor, tenant.Slug, domain.NotificationDestinationInput{
		Name: "Isolated signed webhook", Provider: domain.NotificationWebhook,
		SecretRef: "env:OPENREVIEW_NOTIFY_PLATFORM", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := postgres.RequestNotificationTest(ctx, actor, tenant.Slug, destination.ID, domain.NotificationTestInput{ExpectedRevision: destination.Revision})
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := uuid.Parse(receipt.EventID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = cleanupPool.Exec(context.Background(), `DELETE FROM inbox_messages WHERE message_id = $1`, messageID)
		_, _ = cleanupPool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE id = $1`, messageID)
		_, _ = cleanupPool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenant.ID)
	}()

	receiverCalls := 0
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		receiverCalls++
		var event domain.NotificationEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			t.Errorf("decode notification event: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !event.Test || event.ID != messageID.String() || event.Type != "notification.destination.test" {
			t.Errorf("unexpected notification event: %#v", event)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

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
	exchange := "openreview.test.notification-flow." + suffix
	queue := exchange
	if err := channel.ExchangeDeclare(exchange, "topic", false, true, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueueDeclare(queue, false, true, true, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := channel.QueueBind(queue, "notification.destination.test", exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	deliveries, err := channel.Consume(queue, "notification-flow-"+suffix, false, true, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	published, err := (Relay{
		Store: postgres, Publisher: notificationFlowPublisher{channel: channel, exchange: exchange},
		RelayID: "notification-flow-" + suffix, BatchSize: 10,
	}).Flush(ctx)
	if err != nil || published != 1 {
		t.Fatalf("relay published=%d err=%v, want one confirmed outbox handoff", published, err)
	}

	service := notification.Service{
		Store: postgres,
		Sender: notification.Sender{
			Client:   receiver.Client(),
			Resolver: notificationFlowResolver{credential: notification.Credential{WebhookURL: receiver.URL}},
		},
	}
	var body []byte
	select {
	case delivery := <-deliveries:
		body = delivery.Body
		message, decodeErr := DecodeOutboxMessage(body)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if err := HandleExactlyOnce(ctx, postgres, "notification-flow-v1", message, service.Handle); err != nil {
			t.Fatal(err)
		}
		if err := delivery.Ack(false); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for notification broker delivery")
	}

	message, err := DecodeOutboxMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleExactlyOnce(ctx, postgres, "notification-flow-v1", message, service.Handle); err != nil {
		t.Fatal(err)
	}
	if receiverCalls != 1 {
		t.Fatalf("receiver calls=%d, want exactly one after duplicate broker handling", receiverCalls)
	}
	history, err := postgres.ListNotificationDeliveries(ctx, actor, tenant.Slug, 10)
	if err != nil || len(history) != 1 || history[0].State != "delivered" || history[0].Attempt != 1 || history[0].ResponseCode == nil || *history[0].ResponseCode != http.StatusNoContent {
		t.Fatalf("delivery history=%#v err=%v", history, err)
	}
}
