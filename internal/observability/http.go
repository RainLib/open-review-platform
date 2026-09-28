// Package observability contains low-cardinality request telemetry shared by
// the control plane. It deliberately records route templates, never tenant
// slugs, provider payloads, credentials, or query strings.
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

const requestIDHeader = "X-Request-ID"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

type correlationIDContextKey struct{}

// CorrelationID returns the safe request identifier installed by HTTPMiddleware.
// It is intentionally not derived from credentials, provider IDs, or bodies.
func CorrelationID(ctx context.Context) string {
	value, _ := ctx.Value(correlationIDContextKey{}).(string)
	return value
}

// HTTPMetrics stores aggregate, bounded-cardinality HTTP measurements. Routes
// come from net/http's pattern rather than the concrete request path, so a
// tenant slug or UUID cannot create a new time series.
type HTTPMetrics struct {
	mu       sync.Mutex
	inFlight int
	series   map[httpMetricKey]httpMetricValue
}

type httpMetricKey struct {
	method string
	route  string
	status int
}

type httpMetricValue struct {
	count       uint64
	durationSum time.Duration
}

type OperationalMetricsReader interface {
	GetOperationalMetrics(context.Context) (domain.OperationalMetricsSnapshot, error)
}

func NewHTTPMetrics() *HTTPMetrics {
	return &HTTPMetrics{series: make(map[httpMetricKey]httpMetricValue)}
}

func (metrics *HTTPMetrics) start() {
	metrics.mu.Lock()
	metrics.inFlight++
	metrics.mu.Unlock()
}

func (metrics *HTTPMetrics) finish(method, route string, status int, duration time.Duration) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.inFlight--
	key := httpMetricKey{method: method, route: route, status: status}
	value := metrics.series[key]
	value.count++
	value.durationSum += duration
	metrics.series[key] = value
}

// PrometheusHandler exposes only aggregate process metrics. Deployments must
// keep this endpoint on their operator network; it is intentionally outside
// tenant authorization because collectors have no user session.
func (metrics *HTTPMetrics) PrometheusHandler(readers ...OperationalMetricsReader) http.Handler {
	var operational OperationalMetricsReader
	if len(readers) > 0 {
		operational = readers[0]
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.writePrometheus(writer)
		if operational != nil {
			metrics.writeOperationalPrometheus(request.Context(), writer, operational)
		}
	})
}

func (metrics *HTTPMetrics) writePrometheus(writer http.ResponseWriter) {
	metrics.mu.Lock()
	inFlight := metrics.inFlight
	series := make([]struct {
		key   httpMetricKey
		value httpMetricValue
	}, 0, len(metrics.series))
	for key, value := range metrics.series {
		series = append(series, struct {
			key   httpMetricKey
			value httpMetricValue
		}{key: key, value: value})
	}
	metrics.mu.Unlock()

	sort.Slice(series, func(left, right int) bool {
		if series[left].key.route != series[right].key.route {
			return series[left].key.route < series[right].key.route
		}
		if series[left].key.method != series[right].key.method {
			return series[left].key.method < series[right].key.method
		}
		return series[left].key.status < series[right].key.status
	})

	_, _ = fmt.Fprintln(writer, "# HELP open_review_http_requests_in_flight Current in-flight HTTP requests.")
	_, _ = fmt.Fprintln(writer, "# TYPE open_review_http_requests_in_flight gauge")
	_, _ = fmt.Fprintf(writer, "open_review_http_requests_in_flight %d\n", inFlight)
	_, _ = fmt.Fprintln(writer, "# HELP open_review_http_requests_total Completed HTTP requests by route, method, and status.")
	_, _ = fmt.Fprintln(writer, "# TYPE open_review_http_requests_total counter")
	_, _ = fmt.Fprintln(writer, "# HELP open_review_http_request_duration_seconds Request duration by route, method, and status.")
	_, _ = fmt.Fprintln(writer, "# TYPE open_review_http_request_duration_seconds summary")
	for _, item := range series {
		labels := fmt.Sprintf(`method=%q,route=%q,status=%q`, item.key.method, item.key.route, strconv.Itoa(item.key.status))
		_, _ = fmt.Fprintf(writer, "open_review_http_requests_total{%s} %d\n", labels, item.value.count)
		_, _ = fmt.Fprintf(writer, "open_review_http_request_duration_seconds_sum{%s} %.9f\n", labels, item.value.durationSum.Seconds())
		_, _ = fmt.Fprintf(writer, "open_review_http_request_duration_seconds_count{%s} %d\n", labels, item.value.count)
	}
}

func (metrics *HTTPMetrics) writeOperationalPrometheus(ctx context.Context, writer http.ResponseWriter, reader OperationalMetricsReader) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snapshot, err := reader.GetOperationalMetrics(ctx)
	_, _ = fmt.Fprintln(writer, "# HELP open_review_operational_metrics_scrape_success Whether the authoritative PostgreSQL operational snapshot succeeded.")
	_, _ = fmt.Fprintln(writer, "# TYPE open_review_operational_metrics_scrape_success gauge")
	if err != nil {
		_, _ = fmt.Fprintln(writer, "open_review_operational_metrics_scrape_success 0")
		return
	}
	_, _ = fmt.Fprintln(writer, "open_review_operational_metrics_scrape_success 1")
	writeOperationalGauge(writer, "open_review_review_jobs_queued", "Review jobs waiting for a worker.", snapshot.QueuedReviewJobs)
	writeOperationalGauge(writer, "open_review_review_queue_oldest_age_seconds", "Age of the oldest queued review job.", snapshot.OldestQueuedReviewJobSeconds)
	writeOperationalGauge(writer, "open_review_outbox_unpublished", "Durable outbox messages not yet confirmed by the broker.", snapshot.UnpublishedOutboxMessages)
	writeOperationalGauge(writer, "open_review_outbox_oldest_age_seconds", "Age of the oldest unpublished outbox message.", snapshot.OldestUnpublishedOutboxSeconds)
	writeOperationalGauge(writer, "open_review_ack_sla_breaches", "Unpublished acknowledgement or interaction responses older than five seconds.", snapshot.AcknowledgementSLABreaches)
	writeOperationalGauge(writer, "open_review_review_leases_expired", "Running review jobs whose worker lease has expired.", snapshot.ExpiredReviewLeases)
	writeOperationalGauge(writer, "open_review_worker_heartbeats_stale", "Worker kinds whose latest durable heartbeat has expired.", snapshot.StaleWorkerHeartbeats)
	writeOperationalGauge(writer, "open_review_publication_failures_last_hour", "Provider publication receipts with an error in the last hour.", snapshot.PublicationFailuresLastHour)
	writeOperationalGauge(writer, "open_review_runs_failed_last_hour", "Review runs that failed in the last hour.", snapshot.FailedRunsLastHour)
	writeOperationalGauge(writer, "open_review_runs_terminal_last_hour", "Review runs reaching any terminal state in the last hour.", snapshot.TerminalRunsLastHour)
	writeOperationalGauge(writer, "open_review_notifications_pending", "Notification deliveries waiting to be sent.", snapshot.PendingNotifications)
	writeOperationalGauge(writer, "open_review_notification_oldest_age_seconds", "Age of the oldest pending notification delivery.", snapshot.OldestPendingNotificationSeconds)
}

func writeOperationalGauge(writer http.ResponseWriter, name, help string, value int64) {
	_, _ = fmt.Fprintf(writer, "# HELP %s %s\n", name, help)
	_, _ = fmt.Fprintf(writer, "# TYPE %s gauge\n", name)
	_, _ = fmt.Fprintf(writer, "%s %d\n", name, value)
}

// HTTPMiddleware installs an opaque correlation ID, returns it to the caller,
// logs only route-level metadata, and records the request after ServeMux has
// resolved r.Pattern. Invalid caller-provided IDs are replaced instead of
// becoming log/metric input.
func HTTPMiddleware(next http.Handler, metrics *HTTPMetrics, logger *slog.Logger) http.Handler {
	if metrics == nil {
		metrics = NewHTTPMetrics()
	}
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := trustedRequestID(request.Header.Get(requestIDHeader))
		writer.Header().Set(requestIDHeader, requestID)
		request = request.WithContext(context.WithValue(request.Context(), correlationIDContextKey{}, requestID))
		response := &responseRecorder{ResponseWriter: writer, status: http.StatusOK}
		started := time.Now()
		metrics.start()
		next.ServeHTTP(response, request)
		duration := time.Since(started)
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}
		metrics.finish(request.Method, route, response.status, duration)
		logger.Info("control api request complete",
			"correlation_id", requestID,
			"method", request.Method,
			"route", route,
			"status", response.status,
			"duration_ms", duration.Milliseconds(),
		)
	})
}

// RequestTimeoutMiddleware bounds ordinary control-plane requests while
// leaving the authenticated run event stream open until the browser (or an
// upstream proxy) disconnects. Transport-wide deadlines cannot distinguish a
// slow request from a deliberately long-lived SSE response.
func RequestTimeoutMiddleware(next http.Handler, timeout time.Duration) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isRunEventStream(request) {
			next.ServeHTTP(writer, request)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func isRunEventStream(request *http.Request) bool {
	if request.Method != http.MethodGet || !strings.Contains(strings.ToLower(request.Header.Get("Accept")), "text/event-stream") {
		return false
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	return len(parts) == 6 && parts[0] == "v1" && parts[1] == "tenants" && parts[3] == "runs" && parts[5] == "events"
}

func trustedRequestID(value string) string {
	value = strings.TrimSpace(value)
	if requestIDPattern.MatchString(value) {
		return value
	}
	return uuid.NewString()
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.wroteHeader {
		return
	}
	recorder.wroteHeader = true
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(body)
}

// Flush preserves the Server-Sent Events contract used by task/run streams.
func (recorder *responseRecorder) Flush() {
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
