package governance

import (
	"bytes"
	"context"
	"crypto/hmac"
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
	"github.com/google/uuid"
)

const maxRegionResponseBytes = 64 << 10

type RegionMigrationResult struct {
	Status      string
	OperationID string
	Progress    int
	Message     string
	Observed    domain.DataResidency
	Receipt     map[string]any
}

type RegionMigrator interface {
	Reconcile(context.Context, domain.DataGovernanceJobTarget) (RegionMigrationResult, error)
}

type HTTPRegionOrchestrator struct {
	endpoint string
	secret   []byte
	client   *http.Client
}

type RegionOrchestratorOptions struct {
	Endpoint             string
	Secret               string
	AllowPrivateNetworks bool
	AllowInsecureHTTP    bool
	Timeout              time.Duration
	HTTPClient           *http.Client
}

func NewHTTPRegionOrchestrator(options RegionOrchestratorOptions) (*HTTPRegionOrchestrator, error) {
	parsed, err := url.Parse(strings.TrimSpace(options.Endpoint))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("region orchestrator URL is invalid")
	}
	if parsed.Scheme != "https" && !(options.AllowInsecureHTTP && parsed.Scheme == "http") {
		return nil, errors.New("region orchestrator URL must use HTTPS")
	}
	if len(strings.TrimSpace(options.Secret)) < 32 {
		return nil, errors.New("region orchestrator secret must contain at least 32 characters")
	}
	client := options.HTTPClient
	if client == nil {
		timeout := options.Timeout
		if timeout == 0 {
			timeout = 20 * time.Second
		}
		client = safeRegionClient(options.AllowPrivateNetworks, options.AllowInsecureHTTP, timeout)
	}
	return &HTTPRegionOrchestrator{endpoint: parsed.String(), secret: []byte(strings.TrimSpace(options.Secret)), client: client}, nil
}

func (o *HTTPRegionOrchestrator) Reconcile(ctx context.Context, target domain.DataGovernanceJobTarget) (RegionMigrationResult, error) {
	if target.Kind != domain.DataJobRegionMigration || target.ID == uuid.Nil || target.TenantID == uuid.Nil || target.DesiredRegion == "" {
		return RegionMigrationResult{}, errors.New("region migration target is invalid")
	}
	action := "start"
	if target.ExternalOperationID != "" {
		action = "reconcile"
	}
	payload := map[string]any{
		"schema":                "open-review.region-migration-command.v1",
		"action":                action,
		"job_id":                target.ID,
		"tenant_id":             target.TenantID,
		"desired_region":        target.DesiredRegion,
		"external_operation_id": target.ExternalOperationID,
		"attempt":               target.Attempt,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return RegionMigrationResult{}, fmt.Errorf("encode region migration command: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(body))
	if err != nil {
		return RegionMigrationResult{}, fmt.Errorf("create region migration request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Open-Review-Signature-256", regionSignature(o.secret, body))
	response, err := o.client.Do(request)
	if err != nil {
		return RegionMigrationResult{}, errors.New("region orchestrator could not be reached")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxRegionResponseBytes+1))
	if err != nil || len(responseBody) == 0 || len(responseBody) > maxRegionResponseBytes {
		return RegionMigrationResult{}, errors.New("region orchestrator response is empty or exceeds 64 KiB")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return RegionMigrationResult{}, fmt.Errorf("region orchestrator returned HTTP %d", response.StatusCode)
	}
	if !hmac.Equal([]byte(response.Header.Get("X-Open-Review-Signature-256")), []byte(regionSignature(o.secret, responseBody))) {
		return RegionMigrationResult{}, errors.New("region orchestrator response signature is invalid")
	}
	var decoded struct {
		Schema      string `json:"schema"`
		Status      string `json:"status"`
		OperationID string `json:"operation_id"`
		Progress    int    `json:"progress"`
		Message     string `json:"message"`
		Observed    struct {
			PrimaryRegion string     `json:"primary_region"`
			BackupRegion  string     `json:"backup_region"`
			ObjectRegion  string     `json:"object_region"`
			QueueRegion   string     `json:"queue_region"`
			ModelBoundary string     `json:"model_boundary"`
			ObservedAt    *time.Time `json:"observed_at"`
		} `json:"observed"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil || decoded.Schema != "open-review.region-migration-result.v1" {
		return RegionMigrationResult{}, errors.New("region orchestrator response schema is invalid")
	}
	if decoded.OperationID == "" || (target.ExternalOperationID != "" && decoded.OperationID != target.ExternalOperationID) || decoded.Progress < 0 || decoded.Progress > 100 || len(decoded.Message) > 500 {
		return RegionMigrationResult{}, errors.New("region orchestrator response identity or progress is invalid")
	}
	if decoded.Status != "accepted" && decoded.Status != "running" && decoded.Status != "completed" && decoded.Status != "failed" {
		return RegionMigrationResult{}, errors.New("region orchestrator response status is invalid")
	}
	if decoded.Status == "failed" && strings.TrimSpace(decoded.Message) == "" {
		decoded.Message = "Region orchestrator reported failure without detail."
	}
	observed := domain.DataResidency{
		PrimaryRegion: decoded.Observed.PrimaryRegion,
		BackupRegion:  decoded.Observed.BackupRegion,
		ObjectRegion:  decoded.Observed.ObjectRegion,
		QueueRegion:   decoded.Observed.QueueRegion,
		ModelBoundary: decoded.Observed.ModelBoundary,
		ObservedAt:    decoded.Observed.ObservedAt,
	}
	if decoded.Status == "completed" && (decoded.Progress != 100 || observed.PrimaryRegion != target.DesiredRegion || observed.ObservedAt == nil || observed.ObservedAt.After(time.Now().Add(5*time.Minute))) {
		return RegionMigrationResult{}, errors.New("completed region migration lacks matching observed evidence")
	}
	return RegionMigrationResult{
		Status: decoded.Status, OperationID: decoded.OperationID, Progress: decoded.Progress,
		Message: decoded.Message, Observed: observed,
		Receipt: map[string]any{
			"schema":       decoded.Schema,
			"status":       decoded.Status,
			"operation_id": decoded.OperationID,
			"progress":     decoded.Progress,
			"message":      decoded.Message,
			"observed":     decoded.Observed,
		},
	}, nil
}

func regionSignature(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func safeRegionClient(allowPrivate, allowHTTP bool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
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
				if !allowPrivate && unsafeRegionAddress(resolved.IP) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			}
			return nil, errors.New("region orchestrator resolves only to private or unsafe addresses")
		},
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > 0 {
				return errors.New("region orchestrator redirects are not allowed")
			}
			if request.URL.Scheme != "https" && !(allowHTTP && request.URL.Scheme == "http") {
				return errors.New("region orchestrator redirect is not secure")
			}
			return nil
		},
	}
}

func unsafeRegionAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
