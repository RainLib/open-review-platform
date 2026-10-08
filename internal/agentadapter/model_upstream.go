package agentadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const modelUpstreamRetries = 2

type modelUpstreamObservation struct {
	status    int
	errorType string
}

type modelUpstreamState struct {
	sync.Mutex
	observation        modelUpstreamObservation
	blocked            bool
	blockedObservation modelUpstreamObservation
}

func (broker *modelBroker) observeUpstream(status int, errorType string) {
	broker.upstreamStatus.Store(int32(status))
	broker.upstreamState.Lock()
	broker.upstreamState.observation = modelUpstreamObservation{status: status, errorType: errorType}
	broker.upstreamState.Unlock()
	if status >= 300 && status < 500 && status != http.StatusTooManyRequests {
		broker.blockUpstream(modelUpstreamObservation{status: status, errorType: errorType})
	}
}

// A terminal broker failure ends this coding job, including the CLI's own
// retry loop. Preserve the first failure and never spend another reservation
// on a new SDK request after the bounded upstream retries are exhausted.
func (broker *modelBroker) blockUpstream(observation modelUpstreamObservation) {
	broker.upstreamState.Lock()
	if !broker.upstreamState.blocked {
		broker.upstreamState.blockedObservation = observation
	}
	broker.upstreamState.blocked = true
	broker.upstreamState.Unlock()
	if broker.failure != nil {
		broker.failureOnce.Do(func() { close(broker.failure) })
	}
}

func (broker *modelBroker) codingContext(ctx context.Context) (context.Context, context.CancelFunc) {
	coding, cancel := context.WithCancel(ctx)
	if broker.blockedUpstream().status != 0 {
		cancel()
		return coding, cancel
	}
	go func() {
		select {
		case <-broker.failure:
			cancel()
		case <-coding.Done():
		}
	}()
	return coding, cancel
}

func (broker *modelBroker) blockedUpstream() modelUpstreamObservation {
	broker.upstreamState.Lock()
	defer broker.upstreamState.Unlock()
	if broker.upstreamState.blocked {
		return broker.upstreamState.blockedObservation
	}
	return modelUpstreamObservation{}
}

func (broker *modelBroker) lastUpstream() modelUpstreamObservation {
	broker.upstreamState.Lock()
	defer broker.upstreamState.Unlock()
	if broker.upstreamState.blocked {
		return broker.upstreamState.blockedObservation
	}
	return broker.upstreamState.observation
}

// Retain only a known protocol enum. Provider messages may echo prompts or
// credentials, and are deliberately excluded from diagnostics.
func modelUpstreamErrorType(body []byte) string {
	var value struct {
		Code  string `json:"code"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &value) != nil {
		return "unknown"
	}
	switch value.Code {
	case "AccessDenied.Unpurchased", "Model.AccessDenied", "App.AccessDenied", "InvalidApiKey", "Arrearage":
		return value.Code
	}
	switch value.Error.Type {
	case "permission_error", "authentication_error", "invalid_request_error", "not_found_error", "rate_limit_error", "overloaded_error", "api_error", "timeout_error":
		return value.Error.Type
	default:
		return "unknown"
	}
}

// Retry only definite HTTP failures before any response is exposed to the
// child. Never replay a transport failure or a partial/successful stream: its
// generation and spend may already have started. Each retry uses the same
// body/endpoint, and consumes the unchanged request/input/output ceilings.
func (broker *modelBroker) sendModelRequest(request *http.Request, body []byte, outputTokens int) (*http.Response, error) {
	for retry := 0; ; retry++ {
		copy := request.Clone(request.Context())
		copy.Body = io.NopCloser(bytes.NewReader(body))
		response, err := broker.transport.Do(copy)
		if err != nil {
			broker.observeUpstream(-1, "transport_error")
			broker.blockUpstream(modelUpstreamObservation{status: -1, errorType: "transport_error"})
			return nil, err
		}
		errorType := ""
		if response.StatusCode >= 300 {
			prefix, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
			errorType = modelUpstreamErrorType(prefix)
			response.Body = &prefixReadCloser{Reader: io.MultiReader(bytes.NewReader(prefix), response.Body), Closer: response.Body}
		}
		broker.observeUpstream(response.StatusCode, errorType)
		delay, retryable := modelRetryDelay(response.StatusCode, response.Header.Get("Retry-After"), retry, time.Now())
		if !retryable || retry >= modelUpstreamRetries {
			if response.StatusCode >= 300 {
				broker.blockUpstream(modelUpstreamObservation{status: response.StatusCode, errorType: errorType})
			}
			return response, nil
		}
		if broker.requests.Add(1) > modelBrokerRequestLimit ||
			broker.outputTokens.Add(int64(outputTokens)) > modelBrokerTotalOutputTokens ||
			broker.requestBytes.Add(int64(len(body))) > modelBrokerTotalRequestBytes {
			broker.blockUpstream(modelUpstreamObservation{status: response.StatusCode, errorType: errorType})
			return response, nil
		}
		_ = response.Body.Close()
		if err := waitModelRetry(request.Context(), delay); err != nil {
			return nil, err
		}
	}
}

type prefixReadCloser struct {
	io.Reader
	io.Closer
}

func modelRetryDelay(status int, retryAfter string, retry int, now time.Time) (time.Duration, bool) {
	if status != http.StatusTooManyRequests && status != 500 && status != 502 && status != 503 && status != 504 && status != 529 {
		return 0, false
	}
	delay := time.Duration(1<<retry) * 250 * time.Millisecond
	if retryAfter != "" {
		seconds, err := strconv.Atoi(retryAfter)
		if err == nil && seconds >= 0 {
			if seconds > 5 {
				return 0, false
			}
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else if date, err := http.ParseTime(retryAfter); err == nil {
			if date.Sub(now) > 5*time.Second {
				return 0, false
			}
			delay = max(delay, date.Sub(now))
		} else {
			return 0, false
		}
	}
	return delay, true
}

func waitModelRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
