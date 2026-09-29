package agentadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/google/uuid"
)

// Submission contains a minimum, immutable execution handoff. The adapter is
// responsible for provisioning its own sandbox and scoped provider access; it
// is never handed Open Review's provider credential reference or local path.
type Submission struct {
	AttemptID   string `json:"attempt_id"`
	CallbackURL string `json:"callback_url"`
	Task        struct {
		InstallationID  string                           `json:"installation_id"`
		Provider        domain.Provider                  `json:"provider"`
		APIBaseURL      string                           `json:"api_base_url"`
		Repository      string                           `json:"repository"`
		OriginKind      string                           `json:"origin_kind"`
		OriginNumber    int                              `json:"origin_number"`
		OriginRevision  string                           `json:"origin_revision"`
		ExecutorProfile string                           `json:"executor_profile"`
		SourceBaseRef   string                           `json:"source_base_ref"`
		SourceBaseSHA   string                           `json:"source_base_sha"`
		BranchName      string                           `json:"branch_name"`
		Feedback        *domain.AgentTaskFeedbackBinding `json:"feedback,omitempty"`
	} `json:"task"`
	Plan struct {
		Revision int    `json:"revision"`
		SHA256   string `json:"sha256"`
		Summary  string `json:"summary"`
	} `json:"plan"`
	// Limits are copied from the admitted task, not from adapter input. The
	// adapter must stop at deadline_at; the control plane independently enforces
	// it when it accepts heartbeats and reaps attempts.
	Limits struct {
		MaxAttempts         int    `json:"max_attempts"`
		MaxExecutionSeconds int    `json:"max_execution_seconds"`
		DeadlineAt          string `json:"deadline_at"`
	} `json:"limits"`
}

type submitResponse struct {
	JobID string `json:"job_id"`
}

type Client struct {
	endpoint string
	secret   string
	callback string
	http     *http.Client
}

func NewClient(endpoint, secret, callbackURL string, timeout time.Duration, allowHTTP bool) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	callbackURL = strings.TrimSpace(callbackURL)
	if endpoint == "" || callbackURL == "" || len(secret) < 32 || timeout <= 0 {
		return nil, fmt.Errorf("agent adapter endpoint, callback URL, secret and timeout are required")
	}
	if !trustedURL(endpoint, allowHTTP) || !trustedURL(callbackURL, allowHTTP) {
		return nil, fmt.Errorf("agent adapter endpoint and callback URL must be trusted HTTPS URLs")
	}
	return &Client{endpoint: strings.TrimSuffix(endpoint, "/"), secret: secret, callback: strings.TrimSuffix(callbackURL, "/"), http: httpguard.NoRedirects(nil, timeout)}, nil
}

// Probe proves only that the configured adapter endpoint currently accepts
// this runner's signing secret. It does not run a model, claim a task, inspect
// provider credentials, or prove that a sandbox can publish a Draft PR.
func (client *Client) Probe(ctx context.Context) error {
	if client == nil {
		return fmt.Errorf("agent adapter client is not configured")
	}
	body := []byte("{}")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/v1/open-review/health", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create agent adapter probe: %w", err)
	}
	timestamp := strconvUnix(time.Now())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderSignature, Sign(client.secret, timestamp, body))
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("probe agent adapter: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("agent adapter probe returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (client *Client) Submit(ctx context.Context, target domain.AgentTaskAttemptTarget) (string, error) {
	if client == nil || target.Attempt.ID.String() == "" || target.Task.ID.String() == "" || target.Plan.ID.String() == "" || target.Task.SourceState != "ready" || !(domain.AgentTaskSourceSnapshot{BaseRef: target.Task.SourceBaseRef, BaseSHA: target.Task.SourceBaseSHA}).Valid() {
		return "", fmt.Errorf("agent adapter client submission is invalid")
	}
	requestBody := Submission{AttemptID: target.Attempt.ID.String(), CallbackURL: client.callback}
	requestBody.Task.InstallationID = target.Task.InstallationID.String()
	requestBody.Task.Provider = target.Task.Provider
	requestBody.Task.APIBaseURL = target.Task.APIBaseURL
	requestBody.Task.Repository = target.Task.Repository
	requestBody.Task.OriginKind = target.Task.OriginKind
	requestBody.Task.OriginNumber = target.Task.OriginNumber
	requestBody.Task.OriginRevision = target.Task.OriginRevision
	requestBody.Task.ExecutorProfile = target.Task.ExecutorProfile
	requestBody.Task.SourceBaseRef = target.Task.SourceBaseRef
	requestBody.Task.SourceBaseSHA = target.Task.SourceBaseSHA
	requestBody.Task.BranchName = target.Task.ExecutionBranch
	requestBody.Task.Feedback = target.Feedback
	requestBody.Plan.Revision = target.Plan.Revision
	requestBody.Plan.SHA256 = target.Plan.PlanSHA256
	requestBody.Plan.Summary = target.Plan.Summary
	requestBody.Limits.MaxAttempts = target.Task.MaxAttempts
	requestBody.Limits.MaxExecutionSeconds = target.Task.MaxExecutionSeconds
	if target.Attempt.DeadlineAt != nil {
		requestBody.Limits.DeadlineAt = target.Attempt.DeadlineAt.UTC().Format(time.RFC3339Nano)
	}
	if target.Task.InstallationID == uuid.Nil || (requestBody.Task.ExecutorProfile != "codex" && requestBody.Task.ExecutorProfile != "claude") || requestBody.Limits.MaxAttempts < 1 || requestBody.Limits.MaxExecutionSeconds < 60 || requestBody.Limits.DeadlineAt == "" || (target.Task.OriginKind == "pull_request" && (target.Feedback == nil || !target.Feedback.ExecutionValid())) || (target.Task.OriginKind == "issue" && target.Feedback != nil) || (target.Task.OriginKind != "issue" && target.Task.OriginKind != "pull_request") {
		return "", fmt.Errorf("agent task execution envelope is invalid")
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("encode agent adapter submission: %w", err)
	}
	timestamp := strconvUnix(time.Now())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/v1/open-review/tasks", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create agent adapter submission: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderSignature, Sign(client.secret, timestamp, body))
	response, err := client.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("submit agent task to adapter: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("agent adapter returned %s", response.Status)
	}
	var parsed submitResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil || !validJobID(parsed.JobID) {
		return "", fmt.Errorf("agent adapter returned an invalid job id")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("agent adapter returned trailing response data")
	}
	return strings.TrimSpace(parsed.JobID), nil
}

// Start authorizes execution only after the runner has durably attached the
// adapter job ID to the approved attempt. Repeating this signed request may
// never launch a second sandbox for the same job.
func (client *Client) Start(ctx context.Context, attemptID uuid.UUID, jobID string) error {
	if client == nil || attemptID == uuid.Nil || !validJobID(jobID) {
		return fmt.Errorf("agent adapter start is invalid")
	}
	return client.jobAction(ctx, attemptID, jobID, "start")
}

// Cancel asks an adapter to stop one previously accepted sandbox job. It is
// intentionally keyed by both the Open Review attempt and adapter job ID so a
// delayed cancellation cannot affect a newer attempt for the same task.
// Calling it more than once is safe: adapters must retain an idempotent stop
// receipt for this tuple and return any 2xx response.
func (client *Client) Cancel(ctx context.Context, request domain.AgentTaskCancellationRequest) error {
	if client == nil || !request.Valid() {
		return fmt.Errorf("agent adapter cancellation is invalid")
	}
	return client.jobAction(ctx, request.AttemptID, request.AdapterJobID, "cancel")
}

func (client *Client) jobAction(ctx context.Context, attemptID uuid.UUID, jobID, action string) error {
	body, err := json.Marshal(struct {
		AttemptID string `json:"attempt_id"`
	}{AttemptID: attemptID.String()})
	if err != nil {
		return fmt.Errorf("encode agent adapter %s: %w", action, err)
	}
	timestamp := strconvUnix(time.Now())
	endpoint := client.endpoint + "/v1/open-review/tasks/" + url.PathEscape(strings.TrimSpace(jobID)) + "/" + action
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create agent adapter %s: %w", action, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set(HeaderTimestamp, timestamp)
	httpRequest.Header.Set(HeaderSignature, Sign(client.secret, timestamp, body))
	response, err := client.http.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("%s agent task at adapter: %w", action, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("agent adapter %s returned %s", action, response.Status)
	}
	// Bound the body so a compromised adapter cannot make the runner retain an
	// unbounded response. Its content is not evidence and is intentionally
	// discarded; completion remains an authenticated callback contract.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 16<<10))
	return nil
}

func trustedURL(value string, allowHTTP bool) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "https" || (allowHTTP && parsed.Scheme == "http")
}

func validJobID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 1 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func strconvUnix(now time.Time) string { return fmt.Sprintf("%d", now.Unix()) }
