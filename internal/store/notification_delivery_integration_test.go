package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/notification"
	"github.com/google/uuid"
)

type integrationNotificationResolver struct {
	credential notification.Credential
}

func (r integrationNotificationResolver) Resolve(context.Context, string) (notification.Credential, error) {
	return r.credential, nil
}

// This crosses durable test admission, the notifier HTTP transport and the
// delivery ledger. A local TLS receiver fails once so the same immutable
// outbox event is retried and then recorded as delivered. It does not claim
// acceptance by a real third-party provider.
func TestNotificationTestDeliveryRetriesSignedHTTPSAndRetainsReceipt(t *testing.T) {
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

	const signingSecret = "integration-signing-secret"
	fixedTime := time.Unix(1_700_000_000, 0).UTC()
	type receivedRequest struct {
		body      []byte
		timestamp string
		signature string
	}
	var (
		mu       sync.Mutex
		received []receivedRequest
	)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read notification request: %v", readErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mu.Lock()
		received = append(received, receivedRequest{
			body: body, timestamp: r.Header.Get("X-Open-Review-Timestamp"), signature: r.Header.Get("X-Open-Review-Signature-256"),
		})
		attempt := len(received)
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	tenantID := uuid.New()
	tenantSlug := "notification-send-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Notification delivery')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE aggregate_type = 'notification_destination' AND aggregate_id IN (SELECT id FROM notification_destinations WHERE tenant_id = $1)`, tenantID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	}()

	destination, err := postgres.CreateNotificationDestination(ctx, "owner", tenantSlug, domain.NotificationDestinationInput{
		Name: "Signed deployment webhook", Provider: domain.NotificationWebhook, SecretRef: "env:NOTIFICATION_SIGNED_E2E", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create notification destination: %v", err)
	}
	receipt, err := postgres.RequestNotificationTest(ctx, "owner", tenantSlug, destination.ID, domain.NotificationTestInput{ExpectedRevision: destination.Revision})
	if err != nil {
		t.Fatalf("request notification test: %v", err)
	}
	eventID, err := uuid.Parse(receipt.EventID)
	if err != nil {
		t.Fatal(err)
	}
	message := domain.OutboxMessage{
		ID: eventID, AggregateID: destination.ID, Topic: notificationTestTopic,
		Payload: map[string]any{"destination_id": destination.ID.String(), "destination_revision": float64(destination.Revision)},
	}
	service := notification.Service{
		Store: postgres,
		Sender: notification.Sender{
			Client: receiver.Client(),
			Resolver: integrationNotificationResolver{credential: notification.Credential{
				WebhookURL: receiver.URL, SigningSecret: signingSecret,
			}},
			Now: func() time.Time { return fixedTime },
		},
	}

	if err := service.Handle(ctx, message); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("first delivery error=%v, want safe HTTP 503 failure", err)
	}
	history, err := postgres.ListNotificationDeliveries(ctx, "owner", tenantSlug, 10)
	if err != nil || len(history) != 1 || history[0].State != "failed" || history[0].Attempt != 1 || history[0].ResponseCode == nil || *history[0].ResponseCode != http.StatusServiceUnavailable {
		t.Fatalf("failed delivery history=%#v err=%v", history, err)
	}

	if err := service.Handle(ctx, message); err != nil {
		t.Fatalf("retry notification delivery: %v", err)
	}
	history, err = postgres.ListNotificationDeliveries(ctx, "owner", tenantSlug, 10)
	if err != nil || len(history) != 1 || history[0].State != "delivered" || history[0].Attempt != 2 || history[0].ResponseCode == nil || *history[0].ResponseCode != http.StatusNoContent || history[0].DeliveredAt == nil || history[0].LastError != "" {
		t.Fatalf("delivered history=%#v err=%v", history, err)
	}

	mu.Lock()
	requests := append([]receivedRequest(nil), received...)
	mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("received requests=%d, want 2", len(requests))
	}
	for index, request := range requests {
		if request.timestamp != "1700000000" {
			t.Fatalf("request %d timestamp=%q", index+1, request.timestamp)
		}
		mac := hmac.New(sha256.New, []byte(signingSecret))
		_, _ = mac.Write(append([]byte(request.timestamp+"."), request.body...))
		expectedSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(request.signature), []byte(expectedSignature)) {
			t.Fatalf("request %d signature did not authenticate the exact body", index+1)
		}
		var event domain.NotificationEvent
		if err := json.Unmarshal(request.body, &event); err != nil {
			t.Fatalf("decode request %d: %v", index+1, err)
		}
		if !event.Test || event.Type != notificationTestTopic || event.ID != eventID.String() {
			t.Fatalf("request %d event=%#v", index+1, event)
		}
	}
}
