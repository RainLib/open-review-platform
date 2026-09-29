package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type operationalMetricsReaderFunc func(context.Context) (domain.OperationalMetricsSnapshot, error)

func (function operationalMetricsReaderFunc) GetOperationalMetrics(ctx context.Context) (domain.OperationalMetricsSnapshot, error) {
	return function(ctx)
}

func TestHTTPMiddlewareReturnsTrustedCorrelationAndUsesRouteTemplateMetrics(t *testing.T) {
	metrics := NewHTTPMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/tenants/{slug}/reviews", func(writer http.ResponseWriter, request *http.Request) {
		if got := CorrelationID(request.Context()); got != "trace-42" {
			t.Fatalf("correlation id=%q", got)
		}
		writer.WriteHeader(http.StatusAccepted)
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/reviews", nil)
	request.Header.Set(requestIDHeader, "trace-42")
	response := httptest.NewRecorder()
	HTTPMiddleware(mux, metrics, nil).ServeHTTP(response, request)
	if got := response.Header().Get(requestIDHeader); got != "trace-42" {
		t.Fatalf("request id header=%q", got)
	}
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d", response.Code)
	}

	metricsResponse := httptest.NewRecorder()
	metrics.PrometheusHandler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	if !strings.Contains(body, `route="GET /v1/tenants/{slug}/reviews"`) {
		t.Fatalf("metrics did not retain route template: %s", body)
	}
	if strings.Contains(body, "acme") {
		t.Fatalf("metrics leaked concrete tenant path: %s", body)
	}
}

func TestPrometheusHandlerExportsLowCardinalityOperationalSnapshot(t *testing.T) {
	metrics := NewHTTPMetrics()
	reader := operationalMetricsReaderFunc(func(context.Context) (domain.OperationalMetricsSnapshot, error) {
		return domain.OperationalMetricsSnapshot{
			QueuedReviewJobs:                 7,
			OldestQueuedReviewJobSeconds:     42,
			UnpublishedOutboxMessages:        3,
			OldestUnpublishedOutboxSeconds:   11,
			AcknowledgementSLABreaches:       2,
			ExpiredReviewLeases:              1,
			StaleWorkerHeartbeats:            4,
			PublicationFailuresLastHour:      5,
			FailedRunsLastHour:               1,
			TerminalRunsLastHour:             20,
			PendingNotifications:             6,
			OldestPendingNotificationSeconds: 13,
		}, nil
	})
	response := httptest.NewRecorder()
	metrics.PrometheusHandler(reader).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		"open_review_operational_metrics_scrape_success 1",
		"open_review_review_jobs_queued 7",
		"open_review_ack_sla_breaches 2",
		"open_review_worker_heartbeats_stale 4",
		"open_review_runs_terminal_last_hour 20",
		"open_review_notification_oldest_age_seconds 13",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("operational metrics missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "tenant=") || strings.Contains(body, "repository=") {
		t.Fatalf("fleet metrics unexpectedly exposed tenant/repository dimensions: %s", body)
	}
}

func TestPrometheusHandlerFailsClosedWithoutLeakingDatabaseError(t *testing.T) {
	reader := operationalMetricsReaderFunc(func(context.Context) (domain.OperationalMetricsSnapshot, error) {
		return domain.OperationalMetricsSnapshot{}, errors.New("postgres://secret@database/internal")
	})
	response := httptest.NewRecorder()
	NewHTTPMetrics().PrometheusHandler(reader).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	if !strings.Contains(body, "open_review_operational_metrics_scrape_success 0") {
		t.Fatalf("failed operational scrape was not observable: %s", body)
	}
	if strings.Contains(body, "secret") || strings.Contains(body, "database/internal") {
		t.Fatalf("operational scrape leaked its database error: %s", body)
	}
}

func TestHTTPMiddlewareReplacesInvalidCorrelationID(t *testing.T) {
	metrics := NewHTTPMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		if got := CorrelationID(request.Context()); got == "invalid id with spaces" || got == "" {
			t.Fatalf("unsafe correlation id=%q", got)
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(requestIDHeader, "invalid id with spaces")
	response := httptest.NewRecorder()
	HTTPMiddleware(mux, metrics, nil).ServeHTTP(response, request)
	if got := response.Header().Get(requestIDHeader); got == "invalid id with spaces" || !requestIDPattern.MatchString(got) {
		t.Fatalf("response correlation id=%q", got)
	}
}

func TestHTTPMiddlewarePreservesStreamingFlush(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", func(writer http.ResponseWriter, _ *http.Request) {
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatal("middleware response writer no longer supports http.Flusher")
		}
		_, _ = writer.Write([]byte("event: run\n\n"))
		flusher.Flush()
	})

	response := httptest.NewRecorder()
	HTTPMiddleware(mux, NewHTTPMetrics(), nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !response.Flushed {
		t.Fatal("expected SSE flush to reach the underlying response writer")
	}
}

func TestRequestTimeoutMiddlewareBoundsOrdinaryRequests(t *testing.T) {
	const timeout = 30 * time.Second
	var remaining time.Duration
	handler := RequestTimeoutMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		deadline, ok := request.Context().Deadline()
		if !ok {
			t.Fatal("ordinary request did not receive a deadline")
		}
		remaining = time.Until(deadline)
	}), timeout)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/reviews", nil))
	if remaining <= 0 || remaining > timeout {
		t.Fatalf("ordinary request deadline remaining=%s", remaining)
	}
}

func TestRequestTimeoutMiddlewareLeavesRunEventStreamLongLivedAndCancellable(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/runs/8f893f5c-0e92-4c3e-870a-cb9090e21454/events", nil).WithContext(parent)
	request.Header.Set("Accept", "text/event-stream")

	handler := RequestTimeoutMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		if _, ok := request.Context().Deadline(); ok {
			t.Fatal("run event stream unexpectedly received a deadline")
		}
		cancel()
		select {
		case <-request.Context().Done():
		default:
			t.Fatal("browser cancellation did not propagate to the stream")
		}
	}), 30*time.Second)
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

func TestRequestTimeoutMiddlewareDoesNotExemptLookalikeRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		accept string
	}{
		{name: "missing accept", method: http.MethodGet, path: "/v1/tenants/acme/runs/run-id/events"},
		{name: "wrong method", method: http.MethodPost, path: "/v1/tenants/acme/runs/run-id/events", accept: "text/event-stream"},
		{name: "extra path", method: http.MethodGet, path: "/v1/tenants/acme/runs/run-id/events/export", accept: "text/event-stream"},
		{name: "different resource", method: http.MethodGet, path: "/v1/tenants/acme/reviews/run-id/events", accept: "text/event-stream"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Accept", test.accept)
			RequestTimeoutMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				if _, ok := request.Context().Deadline(); !ok {
					t.Fatal("lookalike request bypassed the ordinary timeout")
				}
			}), time.Second).ServeHTTP(httptest.NewRecorder(), request)
		})
	}
}
