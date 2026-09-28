package notification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type fixedResolver struct{ credential Credential }

func (r fixedResolver) Resolve(context.Context, string) (Credential, error) { return r.credential, nil }

func TestSenderBuildsProviderSpecificPayloads(t *testing.T) {
	for _, provider := range []domain.NotificationProvider{domain.NotificationDingTalk, domain.NotificationFeishu, domain.NotificationSlack, domain.NotificationWebhook} {
		t.Run(string(provider), func(t *testing.T) {
			var body string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(payload)
				body = string(encoded)
				w.WriteHeader(http.StatusOK)
				switch provider {
				case domain.NotificationDingTalk:
					_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
				case domain.NotificationFeishu:
					_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
				}
			}))
			defer server.Close()
			sender := Sender{Client: server.Client(), Resolver: fixedResolver{Credential{WebhookURL: server.URL, SigningSecret: "secret"}}, Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
			delivery := domain.NotificationDelivery{Destination: domain.NotificationDestination{Provider: provider, SecretRef: "env:TEST"}, Event: domain.NotificationEvent{ID: "event-1", Repository: "RainLib/open-review-platform", ReviewNumber: 3, State: domain.RunCompleted, HeadSHA: "1234567890abcdef", FindingCount: 2, HighestSeverity: "high"}}
			if _, err := sender.Send(context.Background(), delivery); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, "RainLib/open-review-platform") {
				t.Fatalf("payload does not identify the review: %s", body)
			}
		})
	}
}

func TestSenderMarksTestNotificationsWithoutReviewData(t *testing.T) {
	for _, provider := range []domain.NotificationProvider{domain.NotificationDingTalk, domain.NotificationFeishu, domain.NotificationSlack, domain.NotificationWebhook} {
		t.Run(string(provider), func(t *testing.T) {
			var body string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				encoded, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				body = string(encoded)
				w.WriteHeader(http.StatusOK)
				switch provider {
				case domain.NotificationDingTalk:
					_, _ = w.Write([]byte(`{"errcode":0}`))
				case domain.NotificationFeishu:
					_, _ = w.Write([]byte(`{"code":0}`))
				}
			}))
			defer server.Close()
			sender := Sender{Client: server.Client(), Resolver: fixedResolver{Credential{WebhookURL: server.URL}}}
			if _, err := sender.Send(context.Background(), domain.NotificationDelivery{
				Destination: domain.NotificationDestination{Provider: provider, SecretRef: "env:TEST"},
				Event:       domain.NotificationEvent{ID: "event-test", Type: "notification.destination.test", Test: true},
			}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, "test") || strings.Contains(body, "#0") {
				t.Fatalf("test payload must identify itself without synthetic review data: %s", body)
			}
		})
	}
}

func TestSenderTreatsProviderLevelFailureAsRetryable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"invalid token"}`))
	}))
	defer server.Close()
	sender := Sender{Client: server.Client(), Resolver: fixedResolver{Credential{WebhookURL: server.URL}}}
	_, err := sender.Send(context.Background(), domain.NotificationDelivery{
		Destination: domain.NotificationDestination{Provider: domain.NotificationDingTalk, SecretRef: "env:TEST"},
		Event:       domain.NotificationEvent{ID: "event-1"},
	})
	if err == nil || strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("provider error should be safe and retryable: %v", err)
	}
}

func TestValidateWebhookURLRejectsUnsafeEndpoints(t *testing.T) {
	for _, value := range []string{"http://example.test/hook", "https://user@example.test/hook", "https://example.test/hook#fragment"} {
		if err := validateWebhookURL(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed for " + request.URL.String())
}

func TestSenderDoesNotLeakCredentialURLOnTransportFailure(t *testing.T) {
	const webhook = "https://example.test/robot?access_token=top-secret"
	sender := Sender{
		Client:   &http.Client{Transport: failingTransport{}},
		Resolver: fixedResolver{Credential{WebhookURL: webhook}},
	}
	_, err := sender.Send(context.Background(), domain.NotificationDelivery{
		Destination: domain.NotificationDestination{Provider: domain.NotificationWebhook, SecretRef: "env:TEST"},
		Event:       domain.NotificationEvent{ID: "event-1"},
	})
	if err == nil || strings.Contains(err.Error(), "top-secret") || strings.Contains(err.Error(), webhook) {
		t.Fatalf("transport error leaked credential URL: %v", err)
	}
}
