// Package modelprobe performs an operator-authorized, minimal model request.
// It intentionally has no access to review source code, findings, or prompts.
package modelprobe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const maxProbeResponseBytes = 64 << 10

type CredentialResolver interface {
	ResolveModelCredential(context.Context, string) (string, error)
}

type Client struct {
	Resolver             CredentialResolver
	AllowPrivateNetworks bool
	HTTPClient           *http.Client
}

// Probe sends a fixed, tiny request. It neither persists nor returns provider
// output; a SHA-256 digest is enough to prove that a bounded response arrived.
func (c Client) Probe(ctx context.Context, target domain.ModelProbeTarget) domain.ModelProbeResult {
	started := time.Now()
	finish := func(result domain.ModelProbeResult) domain.ModelProbeResult {
		result.LatencyMS = time.Since(started).Milliseconds()
		return result
	}
	if c.Resolver == nil {
		return finish(domain.ModelProbeResult{ErrorCode: "credential_unavailable", ErrorMessage: "The probe worker has no model credential resolver."})
	}
	token, err := c.Resolver.ResolveModelCredential(ctx, target.Route.CredentialRef)
	if err != nil || strings.TrimSpace(token) == "" {
		return finish(domain.ModelProbeResult{ErrorCode: "credential_unavailable", ErrorMessage: "The configured credential reference is not available to this probe worker."})
	}
	endpoint, err := probeEndpoint(target.Route)
	if err != nil {
		return finish(domain.ModelProbeResult{ErrorCode: "invalid_endpoint", ErrorMessage: "The configured model endpoint is invalid."})
	}
	body, headers := probeRequest(target.Route)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return finish(domain.ModelProbeResult{ErrorCode: "request_failed", ErrorMessage: "The model probe request could not be created."})
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if target.Route.Protocol == "anthropic-messages" {
		request.Header.Set("x-api-key", token)
	} else {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := c.HTTPClient
	if client == nil {
		client = safeHTTPClient(c.AllowPrivateNetworks)
	}
	response, err := client.Do(request)
	if err != nil {
		return finish(domain.ModelProbeResult{ErrorCode: "unreachable", ErrorMessage: "The model endpoint could not be reached."})
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return finish(domain.ModelProbeResult{ErrorCode: "unexpected_status", ErrorMessage: fmt.Sprintf("The model endpoint returned HTTP %d.", response.StatusCode)})
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxProbeResponseBytes+1))
	if err != nil || len(responseBody) == 0 || len(responseBody) > maxProbeResponseBytes || !validJSONObject(responseBody) {
		return finish(domain.ModelProbeResult{ErrorCode: "invalid_response", ErrorMessage: "The model endpoint returned an invalid probe response."})
	}
	digest := sha256.Sum256(responseBody)
	return finish(domain.ModelProbeResult{ResponseSHA256: hex.EncodeToString(digest[:])})
}

func probeEndpoint(route domain.ModelRouteConfig) (*url.URL, error) {
	if !route.Valid() || !route.Enabled {
		return nil, errors.New("disabled or invalid model route")
	}
	parsed, err := url.Parse(route.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid endpoint")
	}
	return parsed, nil
}

func probeRequest(route domain.ModelRouteConfig) ([]byte, map[string]string) {
	if route.Protocol == "anthropic-messages" {
		body, _ := json.Marshal(map[string]any{
			"model": route.Model, "max_tokens": 4,
			"messages": []map[string]string{{"role": "user", "content": "Reply exactly OK."}},
		})
		return body, map[string]string{"anthropic-version": "2023-06-01"}
	}
	body, _ := json.Marshal(map[string]any{
		"model": route.Model, "max_tokens": 4, "temperature": 0,
		"messages": []map[string]string{{"role": "user", "content": "Reply exactly OK."}},
	})
	return body, map[string]string{}
}

func validJSONObject(body []byte) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(body, &value) == nil && value != nil
}

func safeHTTPClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, resolved := range addresses {
				if !allowPrivate && privateAddress(resolved.IP) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			}
			return nil, fmt.Errorf("model endpoint resolves only to private or unsafe addresses")
		},
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("model probe redirects are not followed")
		},
	}
}

func privateAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

type ProbeStore interface {
	ClaimModelProbe(context.Context, string) (*domain.ModelProbeTarget, error)
	CompleteModelProbe(context.Context, uuid.UUID, string, domain.ModelProbeResult) error
}

type Processor struct {
	Store       ProbeStore
	Client      Client
	WorkerID    string
	TaskStarted func() func()
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	if p.Store == nil || strings.TrimSpace(p.WorkerID) == "" {
		return false, fmt.Errorf("model probe processor is not configured")
	}
	target, err := p.Store.ClaimModelProbe(ctx, p.WorkerID)
	if errors.Is(err, store.ErrNoQueuedModelProbe) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := p.Store.CompleteModelProbe(ctx, target.ReceiptID, p.WorkerID, p.Client.Probe(probeCtx, *target)); err != nil {
		return true, err
	}
	return true, nil
}
