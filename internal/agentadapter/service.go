package agentadapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/google/uuid"
)

// Executor is the adapter-owned sandbox boundary. It receives only a
// validated immutable Submission and must never interpret Issue text as a
// shell command. The concrete implementation lives outside the control plane.
type Executor interface {
	Execute(context.Context, string, Submission) (ExecutionResult, error)
}

// ExecutionResult is deliberately limited to independently verifiable provider
// evidence. It cannot claim approval, merge, or test success without a Draft
// PR/MR URL and exact pushed revision.
type ExecutionResult struct {
	Summary                   string
	BranchName                string
	HeadSHA                   string
	PullRequestURL            string
	PullRequestNumber         int
	PatchSHA256               string
	ChangedFileCount          int
	DiffBytes                 int64
	VerificationProfileSHA256 string
	VerificationOutputSHA256  string
	VerificationOutputBytes   int64
}

// PublicationCheckpoint is durably retained before the first provider write.
// It proves which locally validated commit may be reconciled after a crash;
// the checkpoint alone never proves that a branch or Draft was published.
type PublicationCheckpoint struct {
	HeadSHA                   string `json:"head_sha"`
	PatchSHA256               string `json:"patch_sha256"`
	ChangedFileCount          int    `json:"changed_file_count"`
	DiffBytes                 int64  `json:"diff_bytes"`
	VerificationProfileSHA256 string `json:"verification_profile_sha256,omitempty"`
	VerificationOutputSHA256  string `json:"verification_output_sha256,omitempty"`
	VerificationOutputBytes   int64  `json:"verification_output_bytes,omitempty"`
}

// PublicationReconciler must only read provider state. Recovery never repeats
// a coding run, branch push, or Draft creation after a one-use start claim.
type PublicationReconciler interface {
	ReconcilePublication(context.Context, string, Submission, PublicationCheckpoint) (ExecutionResult, error)
}

// ServiceConfig keeps the callback allowlist on the adapter side, preventing a
// signed but compromised runner from using an adapter as an arbitrary SSRF
// client. Every accepted Submission must use this exact callback URL.
type ServiceConfig struct {
	Secret           string
	CallbackURL      string
	StartGateURL     string
	AllowHTTP        bool
	DraftURLPolicy   domain.AgentDraftURLPolicy
	HeartbeatEvery   time.Duration
	RequestMaxSkew   time.Duration
	CallbackTimeout  time.Duration
	ReceiptRetention time.Duration
	ReceiptDir       string
	Executor         Executor
}

type Service struct {
	secret           string
	callbackURL      string
	startGateURL     string
	heartbeatEvery   time.Duration
	maxSkew          time.Duration
	callbackTimeout  time.Duration
	receiptRetention time.Duration
	callbackClient   *http.Client
	executor         Executor
	allowHTTP        bool
	draftURLPolicy   domain.AgentDraftURLPolicy
	receipts         *receiptStore

	mu        sync.Mutex
	byAttempt map[string]*serviceJob
	byJob     map[string]*serviceJob
	recovered []*serviceJob
	active    sync.WaitGroup
	closed    bool
}

type serviceJob struct {
	id          string
	digest      [sha256.Size]byte
	submission  Submission
	ctx         context.Context
	cancel      context.CancelFunc
	stateMu     sync.Mutex
	deliveryMu  sync.Mutex
	started     bool
	cancelled   bool
	status      string
	createdAt   time.Time
	terminal    *domain.AgentTaskAdapterEvent
	publication *PublicationCheckpoint
	delivered   bool
	rejected    bool
	retention   *time.Timer
}

type publicationLeaseGuardKey struct{}
type publicationCheckpointKey struct{}
type publicationCheckpointRecorder func(context.Context, PublicationCheckpoint) error

type publicationLeaseGuard func(context.Context, string) error

func NewService(config ServiceConfig) (*Service, error) {
	if len(config.Secret) < 32 || config.Executor == nil || !trustedURL(config.CallbackURL, config.AllowHTTP) {
		return nil, fmt.Errorf("agent adapter secret, HTTPS callback URL and executor are required")
	}
	if validator, ok := config.Executor.(interface{ valid() error }); ok {
		if err := validator.valid(); err != nil {
			return nil, err
		}
	}
	if config.DraftURLPolicy.GitLabPublicBaseURL != "" && config.DraftURLPolicy.GitLabPublicForAPIBaseURL == "" {
		return nil, fmt.Errorf("public GitLab draft URL requires its exact internal API base")
	}
	if config.DraftURLPolicy.GitHubPublicBaseURL != "" && config.DraftURLPolicy.GitHubPublicForAPIBaseURL == "" {
		return nil, fmt.Errorf("public GitHub draft URL requires its exact API base")
	}
	if config.StartGateURL == "" && strings.HasSuffix(config.CallbackURL, "/v1/agent-adapter/events") {
		config.StartGateURL = strings.TrimSuffix(config.CallbackURL, "/events") + "/starts"
	}
	if !trustedURL(config.StartGateURL, config.AllowHTTP) || !sameURLOrigin(config.CallbackURL, config.StartGateURL) {
		return nil, fmt.Errorf("agent adapter requires an HTTPS start gate on the callback origin")
	}
	if config.HeartbeatEvery <= 0 {
		config.HeartbeatEvery = 30 * time.Second
	}
	if config.RequestMaxSkew <= 0 {
		config.RequestMaxSkew = time.Minute
	}
	if config.CallbackTimeout <= 0 {
		config.CallbackTimeout = 15 * time.Second
	}
	if config.ReceiptRetention <= 0 {
		config.ReceiptRetention = 24 * time.Hour
	}
	receipts, err := openReceiptStore(strings.TrimSpace(config.ReceiptDir))
	if err != nil {
		return nil, err
	}
	service := &Service{
		secret: config.Secret, callbackURL: strings.TrimSuffix(config.CallbackURL, "/"), startGateURL: strings.TrimSuffix(config.StartGateURL, "/"), heartbeatEvery: config.HeartbeatEvery,
		maxSkew: config.RequestMaxSkew, callbackTimeout: config.CallbackTimeout, callbackClient: httpguard.NoRedirects(nil, config.CallbackTimeout), executor: config.Executor, allowHTTP: config.AllowHTTP,
		draftURLPolicy:   config.DraftURLPolicy,
		receiptRetention: config.ReceiptRetention, receipts: receipts,
		byAttempt: map[string]*serviceJob{}, byJob: map[string]*serviceJob{},
	}
	if err := service.loadReceipts(); err != nil {
		_ = receipts.close()
		return nil, err
	}
	return service, nil
}

// Close releases the single-replica receipt lock after the HTTP server has
// stopped accepting requests. Running executors are cancelled; their durable
// state remains "started" so the next process never re-executes them.
func (service *Service) Close() error {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil
	}
	service.closed = true
	for _, job := range service.byJob {
		job.cancel()
		if job.retention != nil {
			job.retention.Stop()
		}
	}
	service.mu.Unlock()
	service.active.Wait()
	return service.receipts.close()
}

func (service *Service) loadReceipts() error {
	records, err := service.receipts.load()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.CreatedAt.After(time.Now().Add(time.Minute)) {
			return fmt.Errorf("adapter receipt timestamp is invalid")
		}
		canonical, err := json.Marshal(record.Submission)
		if err != nil || !service.validSubmission(record.Submission) || record.Submission.AttemptID != record.AttemptID {
			return fmt.Errorf("adapter receipt contains an invalid submission")
		}
		digest := sha256.Sum256(canonical)
		jobBytes, jobErr := hex.DecodeString(strings.TrimPrefix(record.JobID, "adapter-"))
		if record.Digest != hex.EncodeToString(digest[:]) || !strings.HasPrefix(record.JobID, "adapter-") || jobErr != nil || len(jobBytes) != 18 {
			return fmt.Errorf("adapter receipt identity differs from its immutable submission")
		}
		if record.Status == "terminal" && (record.Terminal == nil || !record.Terminal.Valid() || record.Terminal.Kind == "heartbeat" || record.Terminal.AdapterJobID != record.JobID || record.Terminal.AttemptID.String() != record.AttemptID) {
			return fmt.Errorf("adapter terminal receipt is invalid")
		}
		if record.Status != "terminal" && (record.Terminal != nil || record.Delivered || record.Rejected) {
			return fmt.Errorf("adapter receipt has an impossible terminal state")
		}
		if record.Publication != nil && ((record.Status != "started" && record.Status != "cancelled" && record.Status != "terminal") || !validPublicationCheckpoint(*record.Publication)) {
			return fmt.Errorf("adapter receipt has an invalid publication checkpoint")
		}
		if record.Publication != nil && record.Terminal != nil && record.Terminal.Kind == "completed" &&
			(record.Terminal.HeadSHA != record.Publication.HeadSHA || record.Terminal.PatchSHA256 != record.Publication.PatchSHA256 ||
				record.Terminal.ChangedFileCount != record.Publication.ChangedFileCount || record.Terminal.DiffBytes != record.Publication.DiffBytes ||
				record.Terminal.VerificationProfileSHA256 != record.Publication.VerificationProfileSHA256 || record.Terminal.VerificationOutputSHA256 != record.Publication.VerificationOutputSHA256 || record.Terminal.VerificationOutputBytes != record.Publication.VerificationOutputBytes) {
			return fmt.Errorf("adapter terminal receipt differs from its validated publication")
		}
		if record.Delivered && record.Rejected {
			return fmt.Errorf("adapter terminal receipt has conflicting delivery state")
		}
		if time.Since(record.CreatedAt) >= service.receiptRetention {
			if err := service.receipts.remove(record.AttemptID); err != nil {
				return err
			}
			continue
		}
		if service.byAttempt[record.AttemptID] != nil || service.byJob[record.JobID] != nil {
			return fmt.Errorf("adapter receipt identities are duplicated")
		}
		ctx, cancel := context.WithCancel(context.Background())
		job := &serviceJob{id: record.JobID, digest: digest, submission: record.Submission, ctx: ctx, cancel: cancel,
			status: record.Status, createdAt: record.CreatedAt, terminal: record.Terminal, publication: record.Publication, delivered: record.Delivered, rejected: record.Rejected}
		if record.Status != "reserved" {
			job.started = true
			job.cancelled = true
			job.cancel()
		}
		service.byAttempt[record.AttemptID], service.byJob[record.JobID] = job, job
		service.recovered = append(service.recovered, job)
		service.retain(job)
	}
	return nil
}

// persistJob is called under the job state lock (or before publishing a new
// job in the service map). A receipt contains no provider write credential or
// adapter HMAC secret, but its Issue/plan evidence still needs private storage.
func (service *Service) persistJob(job *serviceJob) error {
	return service.receipts.save(persistedReceipt{
		JobID: job.id, AttemptID: job.submission.AttemptID, Digest: hex.EncodeToString(job.digest[:]),
		Submission: job.submission, Status: job.status, Terminal: job.terminal, Publication: job.publication,
		Delivered: job.delivered, Rejected: job.rejected, CreatedAt: job.createdAt,
	})
}

// Recover never re-runs a job that might have consumed the one-use start
// claim. It retries a persisted terminal callback or asks the control plane
// to mark an interrupted start as needing human attention. An unclaimed
// reservation remains startable using the same durable job ID.
func (service *Service) Recover(ctx context.Context) {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return
	}
	service.active.Add(1)
	jobs := append([]*serviceJob(nil), service.recovered...)
	service.recovered = nil
	service.mu.Unlock()
	defer service.active.Done()
	for _, job := range jobs {
		// Finish converting every recovered started receipt into a durable
		// terminal event even when startup's delivery context has expired.
		// RetryPending can deliver those events later with a fresh context;
		// leaving a receipt in "started" would strand it until another restart.
		job.stateMu.Lock()
		status, terminal, publication, delivered, rejected := job.status, job.terminal, job.publication, job.delivered, job.rejected
		job.stateMu.Unlock()
		if status == "terminal" && terminal != nil && !delivered && !rejected {
			service.deliverTerminal(ctx, job, *terminal)
			continue
		}
		if status == "starting" || status == "started" {
			if status == "started" && publication != nil {
				if reconciler, ok := service.executor.(PublicationReconciler); ok {
					probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					result, err := reconciler.ReconcilePublication(probeCtx, job.id, job.submission, *publication)
					cancel()
					if err == nil && validExecutionResult(job.submission, result, service.draftURLPolicy) &&
						result.HeadSHA == publication.HeadSHA && result.PatchSHA256 == publication.PatchSHA256 &&
						result.ChangedFileCount == publication.ChangedFileCount && result.DiffBytes == publication.DiffBytes &&
						result.VerificationProfileSHA256 == publication.VerificationProfileSHA256 && result.VerificationOutputSHA256 == publication.VerificationOutputSHA256 && result.VerificationOutputBytes == publication.VerificationOutputBytes {
						service.deliverTerminal(ctx, job, completedAdapterEvent(job, result, ":reconciled"))
						continue
					}
				}
			}
			errorCode := "agent_adapter_interrupted"
			summary := "The adapter restarted after execution start became uncertain. The coding Agent was not relaunched; inspect the dedicated branch and approve a new attempt if needed."
			if publication != nil {
				errorCode = "agent_publication_unverified"
				summary = "The adapter restarted after validating a commit, but an exact open Draft could not be verified. No coding, push, or Draft creation was replayed; inspect the dedicated branch and provider Draft before deciding whether to retry."
			}
			service.deliverTerminal(ctx, job, domain.AgentTaskAdapterEvent{
				AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id,
				DeliveryID: "adapter:" + job.id + ":interrupted", Kind: "needs_attention",
				ErrorCode: errorCode,
				Summary:   summary,
			})
		}
	}
}

// RetryPending is safe to call on a timer while new jobs execute. Unlike
// Recover, it never interprets a currently running job as interrupted; it
// only redelivers an already persisted terminal event with its original ID.
func (service *Service) RetryPending(ctx context.Context) {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return
	}
	service.active.Add(1)
	jobs := make([]*serviceJob, 0, len(service.byJob))
	for _, job := range service.byJob {
		jobs = append(jobs, job)
	}
	service.mu.Unlock()
	defer service.active.Done()
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		job.stateMu.Lock()
		terminal, delivered, rejected := job.terminal, job.delivered, job.rejected
		job.stateMu.Unlock()
		if terminal != nil && !delivered && !rejected {
			service.deliverTerminal(ctx, job, *terminal)
		}
	}
}

func (service *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/open-review/health", service.probe)
	mux.HandleFunc("POST /v1/open-review/tasks", service.submit)
	mux.HandleFunc("POST /v1/open-review/tasks/{jobID}/start", service.start)
	mux.HandleFunc("POST /v1/open-review/tasks/{jobID}/cancel", service.cancel)
	return mux
}

func (service *Service) probe(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 3))
	if err != nil || !Verify(service.secret, request.Header.Get(HeaderTimestamp), request.Header.Get(HeaderSignature), body, time.Now(), service.maxSkew) {
		writeServiceError(writer, http.StatusUnauthorized, "invalid adapter signature")
		return
	}
	if !bytes.Equal(body, []byte("{}")) {
		writeServiceError(writer, http.StatusBadRequest, "invalid adapter probe")
		return
	}
	service.mu.Lock()
	closed := service.closed
	service.mu.Unlock()
	if closed {
		writeServiceError(writer, http.StatusServiceUnavailable, "adapter is shutting down")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (service *Service) submit(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, (128<<10)+1))
	if err != nil || len(body) > 128<<10 {
		writeServiceError(writer, http.StatusBadRequest, "invalid request body")
		return
	}
	if !Verify(service.secret, request.Header.Get(HeaderTimestamp), request.Header.Get(HeaderSignature), body, time.Now(), service.maxSkew) {
		writeServiceError(writer, http.StatusUnauthorized, "invalid adapter signature")
		return
	}
	var submission Submission
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&submission); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !service.validSubmission(submission) {
		writeServiceError(writer, http.StatusBadRequest, "invalid immutable task submission")
		return
	}
	canonical, err := json.Marshal(submission)
	if err != nil {
		writeServiceError(writer, http.StatusBadRequest, "invalid immutable task submission")
		return
	}
	digest := sha256.Sum256(canonical)
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		writeServiceError(writer, http.StatusServiceUnavailable, "adapter is shutting down")
		return
	}
	if existing := service.byAttempt[submission.AttemptID]; existing != nil {
		service.mu.Unlock()
		if existing.digest != digest {
			writeServiceError(writer, http.StatusConflict, "attempt submission differs from the accepted immutable job")
			return
		}
		writeServiceJSON(writer, http.StatusOK, submitResponse{JobID: existing.id})
		return
	}
	jobID, err := newServiceJobID()
	if err != nil {
		service.mu.Unlock()
		writeServiceError(writer, http.StatusInternalServerError, "could not allocate adapter job")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &serviceJob{id: jobID, digest: digest, submission: submission, ctx: ctx, cancel: cancel, status: "reserved", createdAt: time.Now().UTC()}
	if err := service.persistJob(job); err != nil {
		cancel()
		service.mu.Unlock()
		writeServiceError(writer, http.StatusServiceUnavailable, "could not retain adapter job")
		return
	}
	service.byAttempt[submission.AttemptID], service.byJob[jobID] = job, job
	service.mu.Unlock()
	service.retain(job)
	writeServiceJSON(writer, http.StatusAccepted, submitResponse{JobID: jobID})
}

// Start is deliberately separate from Submit: the runner first persists the
// returned job ID against its live attempt lease, then authorizes execution.
// Thus even an immediate terminal callback can match the durable attempt.
func (service *Service) start(writer http.ResponseWriter, request *http.Request) {
	payload, ok := service.controlPayload(writer, request)
	if !ok {
		return
	}
	service.mu.Lock()
	job := service.byJob[strings.TrimSpace(request.PathValue("jobID"))]
	service.mu.Unlock()
	if job == nil || job.submission.AttemptID != payload.AttemptID {
		writeServiceError(writer, http.StatusNotFound, "adapter job not found")
		return
	}
	job.stateMu.Lock()
	defer job.stateMu.Unlock()
	if job.cancelled {
		writeServiceError(writer, http.StatusConflict, "adapter job was cancelled")
		return
	}
	if job.started {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, job.submission.Limits.DeadlineAt)
	if err != nil || !deadline.After(time.Now()) {
		job.cancelled = true
		job.cancel()
		job.status = "cancelled"
		if persistErr := service.persistJob(job); persistErr != nil {
			writeServiceError(writer, http.StatusServiceUnavailable, "could not retain expired adapter job")
			return
		}
		writeServiceError(writer, http.StatusConflict, "adapter job deadline has elapsed")
		return
	}
	job.status = "starting"
	if err := service.persistJob(job); err != nil {
		job.cancelled = true
		job.cancel()
		writeServiceError(writer, http.StatusServiceUnavailable, "could not retain adapter start intent")
		return
	}
	if status, err := service.claimRemoteStart(request.Context(), job); err != nil {
		// A lost start-gate response is ambiguous. Never reclaim or launch a
		// potentially already-started job from this process or a restart.
		job.cancelled = true
		job.cancel()
		writeServiceError(writer, status, "control-plane start gate is unavailable")
		return
	}
	job.status = "started"
	if err := service.persistJob(job); err != nil {
		job.cancelled = true
		job.cancel()
		writeServiceError(writer, http.StatusServiceUnavailable, "could not retain adapter start claim")
		return
	}
	job.started = true
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		job.cancelled = true
		job.cancel()
		writeServiceError(writer, http.StatusServiceUnavailable, "adapter is shutting down")
		return
	}
	service.active.Add(1)
	service.mu.Unlock()
	go service.run(job.ctx, job)
	writer.WriteHeader(http.StatusNoContent)
}

func sameURLOrigin(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	return errA == nil && errB == nil && a.Scheme == b.Scheme && a.Host == b.Host
}

func (service *Service) claimRemoteStart(ctx context.Context, job *serviceJob) (int, error) {
	body, err := json.Marshal(struct {
		AttemptID    string `json:"attempt_id"`
		AdapterJobID string `json:"adapter_job_id"`
	}{AttemptID: job.submission.AttemptID, AdapterJobID: job.id})
	if err != nil {
		return http.StatusInternalServerError, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, service.startGateURL, bytes.NewReader(body))
	if err != nil {
		return http.StatusInternalServerError, err
	}
	timestamp := strconvUnix(time.Now())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderSignature, Sign(service.secret, timestamp, body))
	response, err := service.callbackClient.Do(request)
	if err != nil {
		return http.StatusServiceUnavailable, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return http.StatusNoContent, nil
	}
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusNotFound {
		return http.StatusConflict, fmt.Errorf("control-plane start gate denied job")
	}
	return http.StatusServiceUnavailable, fmt.Errorf("control-plane start gate returned HTTP %d", response.StatusCode)
}

type serviceControlPayload struct {
	AttemptID string `json:"attempt_id"`
}

func (service *Service) controlPayload(writer http.ResponseWriter, request *http.Request) (serviceControlPayload, bool) {
	body, err := io.ReadAll(io.LimitReader(request.Body, (16<<10)+1))
	if err != nil || len(body) > 16<<10 {
		writeServiceError(writer, http.StatusBadRequest, "invalid request body")
		return serviceControlPayload{}, false
	}
	if !Verify(service.secret, request.Header.Get(HeaderTimestamp), request.Header.Get(HeaderSignature), body, time.Now(), service.maxSkew) {
		writeServiceError(writer, http.StatusUnauthorized, "invalid adapter signature")
		return serviceControlPayload{}, false
	}
	var payload serviceControlPayload
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || decoder.Decode(&struct{}{}) != io.EOF || parseAdapterUUID(payload.AttemptID) == uuid.Nil {
		writeServiceError(writer, http.StatusBadRequest, "invalid adapter control request")
		return serviceControlPayload{}, false
	}
	return payload, true
}

func (service *Service) cancel(writer http.ResponseWriter, request *http.Request) {
	payload, ok := service.controlPayload(writer, request)
	if !ok {
		return
	}
	service.mu.Lock()
	job := service.byJob[strings.TrimSpace(request.PathValue("jobID"))]
	service.mu.Unlock()
	if job != nil && job.submission.AttemptID == payload.AttemptID {
		job.stateMu.Lock()
		if job.status == "terminal" {
			job.stateMu.Unlock()
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		job.cancelled = true
		job.cancel()
		job.status = "cancelled"
		err := service.persistJob(job)
		job.stateMu.Unlock()
		if err != nil {
			writeServiceError(writer, http.StatusServiceUnavailable, "could not retain adapter cancellation")
			return
		}
	}
	// Cancellation is idempotent: a missing/finished job must not cause the
	// runner to retry a revoked control-plane lease.
	writer.WriteHeader(http.StatusNoContent)
}

func (service *Service) run(ctx context.Context, job *serviceJob) {
	defer service.active.Done()
	heartbeatDone := make(chan struct{})
	go service.heartbeats(ctx, job, heartbeatDone)
	ctx = context.WithValue(ctx, publicationLeaseGuardKey{}, publicationLeaseGuard(func(checkCtx context.Context, checkpoint string) error {
		event := domain.AgentTaskAdapterEvent{
			AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id,
			DeliveryID: "adapter:" + job.id + ":" + checkpoint, Kind: "heartbeat",
		}
		if err := service.callback(checkCtx, job, event); err != nil {
			job.cancel()
			return fmt.Errorf("control-plane lease is unavailable before %s: %w", checkpoint, err)
		}
		return nil
	}))
	ctx = context.WithValue(ctx, publicationCheckpointKey{}, publicationCheckpointRecorder(func(checkCtx context.Context, checkpoint PublicationCheckpoint) error {
		if !validPublicationCheckpoint(checkpoint) {
			return fmt.Errorf("validated publication checkpoint is invalid")
		}
		if service.receipts == nil {
			return fmt.Errorf("durable adapter receipt storage is required before provider publication")
		}
		job.stateMu.Lock()
		if job.status != "started" || job.cancelled {
			job.stateMu.Unlock()
			return fmt.Errorf("adapter job is no longer startable for publication")
		}
		if job.publication != nil && *job.publication != checkpoint {
			job.stateMu.Unlock()
			return fmt.Errorf("adapter publication checkpoint changed")
		}
		previous := job.publication
		job.publication = &checkpoint
		if err := service.persistJob(job); err != nil {
			job.publication = previous
			job.stateMu.Unlock()
			return fmt.Errorf("retain publication checkpoint: %w", err)
		}
		job.stateMu.Unlock()
		event := domain.AgentTaskAdapterEvent{
			AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id,
			DeliveryID: "adapter:" + job.id + ":publication-checkpoint", Kind: "publication_checkpoint",
			BranchName: job.submission.Task.BranchName, HeadSHA: checkpoint.HeadSHA,
			PatchSHA256: checkpoint.PatchSHA256, ChangedFileCount: checkpoint.ChangedFileCount, DiffBytes: checkpoint.DiffBytes,
			VerificationProfileSHA256: checkpoint.VerificationProfileSHA256, VerificationOutputSHA256: checkpoint.VerificationOutputSHA256, VerificationOutputBytes: checkpoint.VerificationOutputBytes,
		}
		if err := service.callback(checkCtx, job, event); err != nil {
			job.cancel()
			return fmt.Errorf("control plane did not retain publication checkpoint before provider write: %w", err)
		}
		return nil
	}))
	result, err := service.executor.Execute(ctx, job.id, job.submission)
	close(heartbeatDone)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		// Preserve a bounded diagnostic stage without logging the child output,
		// repository content, provider response, or credential-bearing error.
		log.Printf("agent adapter execution failed for job %s at stage %s", job.id, executionFailureStage(err))
		service.deliverTerminal(ctx, job, domain.AgentTaskAdapterEvent{AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id, DeliveryID: "adapter:" + job.id + ":failed", Kind: "needs_attention", ErrorCode: "agent_adapter_execution_failed", Summary: "The isolated adapter could not complete the approved task. Inspect its private execution evidence before retrying."})
		return
	}
	if !validExecutionResult(job.submission, result, service.draftURLPolicy) {
		service.deliverTerminal(ctx, job, domain.AgentTaskAdapterEvent{AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id, DeliveryID: "adapter:" + job.id + ":invalid-result", Kind: "needs_attention", ErrorCode: "agent_adapter_result_rejected", Summary: "The isolated adapter result did not satisfy the immutable branch, revision, and Draft PR evidence contract."})
		return
	}
	if _, recoverable := service.executor.(PublicationReconciler); recoverable {
		job.stateMu.Lock()
		checkpoint := job.publication
		job.stateMu.Unlock()
		if checkpoint == nil || result.HeadSHA != checkpoint.HeadSHA || result.PatchSHA256 != checkpoint.PatchSHA256 ||
			result.ChangedFileCount != checkpoint.ChangedFileCount || result.DiffBytes != checkpoint.DiffBytes ||
			result.VerificationProfileSHA256 != checkpoint.VerificationProfileSHA256 || result.VerificationOutputSHA256 != checkpoint.VerificationOutputSHA256 || result.VerificationOutputBytes != checkpoint.VerificationOutputBytes {
			service.deliverTerminal(ctx, job, domain.AgentTaskAdapterEvent{AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id, DeliveryID: "adapter:" + job.id + ":checkpoint-mismatch", Kind: "needs_attention", ErrorCode: "agent_adapter_result_rejected", Summary: "The Draft result did not match the durable validated publication checkpoint. Inspect the provider branch before retrying."})
			return
		}
	}
	service.deliverTerminal(ctx, job, completedAdapterEvent(job, result, ":completed"))
}

func executionFailureStage(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "agent produced no allowed source change"):
		return "empty_patch"
	case strings.Contains(message, "patch creates disallowed path"), strings.Contains(message, "patch changes disallowed path"):
		return "disallowed_path"
	case strings.Contains(message, "patch appears to contain a secret"):
		return "secret_scan"
	case strings.Contains(message, "patch whitespace validation failed"):
		return "patch_whitespace"
	case strings.Contains(message, "patch creates a non-regular file"), strings.Contains(message, "patch changes a non-regular file"):
		return "non_regular_file"
	case strings.Contains(message, "patch exceeds changed-file budget"), strings.Contains(message, "patch exceeds diff-byte budget"):
		return "patch_budget"
	case strings.Contains(message, "list new agent files"), strings.Contains(message, "include new agent files"):
		return "patch_inventory"
	case strings.Contains(message, "run fixed coding-agent profile"):
		return "coding_executor"
	case strings.Contains(message, "agent changed trusted Git metadata"):
		return "git_metadata"
	case strings.Contains(message, "normalize agent staging area"):
		return "git_index"
	case strings.Contains(message, "stage validated patch"), strings.Contains(message, "staged patch"):
		return "git_stage"
	case strings.Contains(message, "commit validated patch"), strings.Contains(message, "committed patch"):
		return "git_commit"
	case strings.Contains(message, "read patch revision"):
		return "git_head"
	case strings.Contains(message, "clone immutable source"), strings.Contains(message, "checkout immutable source"):
		return "source_checkout"
	case strings.Contains(message, "verify current task origin"):
		return "source_verification"
	case strings.Contains(message, "validated patch"), strings.Contains(message, "unapproved path"):
		return "patch_validation"
	case strings.Contains(message, "push dedicated agent branch"):
		return "branch_publication"
	case strings.Contains(message, "draft"):
		return "draft_publication"
	default:
		return "preflight_or_other"
	}
}

func completedAdapterEvent(job *serviceJob, result ExecutionResult, suffix string) domain.AgentTaskAdapterEvent {
	return domain.AgentTaskAdapterEvent{AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id, DeliveryID: "adapter:" + job.id + suffix, Kind: "completed", Summary: boundedSummary(result.Summary), BranchName: result.BranchName, HeadSHA: result.HeadSHA, PullRequestURL: result.PullRequestURL, PullRequestNumber: result.PullRequestNumber, PatchSHA256: result.PatchSHA256, ChangedFileCount: result.ChangedFileCount, DiffBytes: result.DiffBytes, VerificationProfileSHA256: result.VerificationProfileSHA256, VerificationOutputSHA256: result.VerificationOutputSHA256, VerificationOutputBytes: result.VerificationOutputBytes}
}

func validPublicationCheckpoint(checkpoint PublicationCheckpoint) bool {
	if len(checkpoint.HeadSHA) != 40 && len(checkpoint.HeadSHA) != 64 {
		return false
	}
	if _, err := hex.DecodeString(checkpoint.HeadSHA); err != nil {
		return false
	}
	patch, err := hex.DecodeString(checkpoint.PatchSHA256)
	return err == nil && len(patch) == sha256.Size && hex.EncodeToString(patch) == checkpoint.PatchSHA256 && checkpoint.ChangedFileCount > 0 && checkpoint.DiffBytes > 0 && validAdapterVerificationEvidence(checkpoint.VerificationProfileSHA256, checkpoint.VerificationOutputSHA256, checkpoint.VerificationOutputBytes)
}

// Terminal callbacks reuse one delivery ID across bounded retries so the
// control plane can deduplicate an accepted response lost in transit. The
// event is retained before delivery and replayed after a single-node restart;
// this is not a distributed publication transaction.
func (service *Service) deliverTerminal(ctx context.Context, job *serviceJob, event domain.AgentTaskAdapterEvent) {
	job.deliveryMu.Lock()
	defer job.deliveryMu.Unlock()
	job.stateMu.Lock()
	if job.delivered || job.rejected {
		job.stateMu.Unlock()
		return
	}
	if job.terminal != nil && job.terminal.DeliveryID != event.DeliveryID {
		job.stateMu.Unlock()
		log.Printf("agent adapter ignored conflicting terminal event for job %s", job.id)
		return
	}
	job.status, job.terminal = "terminal", &event
	if err := service.persistJob(job); err != nil {
		job.stateMu.Unlock()
		log.Printf("agent adapter could not retain terminal event for job %s: %v", job.id, err)
		return
	}
	job.stateMu.Unlock()
	backoff := 250 * time.Millisecond
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = service.callback(ctx, job, event); err == nil {
			job.stateMu.Lock()
			job.delivered = true
			if saveErr := service.persistJob(job); saveErr != nil {
				log.Printf("agent adapter terminal delivery acknowledgement was not retained for job %s: %v", job.id, saveErr)
			}
			job.stateMu.Unlock()
			return
		}
		var statusErr callbackStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusConflict {
			job.stateMu.Lock()
			job.rejected = true
			if saveErr := service.persistJob(job); saveErr != nil {
				log.Printf("agent adapter could not retain terminal lease rejection for job %s: %v", job.id, saveErr)
			}
			job.stateMu.Unlock()
			return
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff *= 2
	}
	log.Printf("agent adapter terminal callback exhausted for job %s: %v", job.id, err)
}

func (service *Service) heartbeats(ctx context.Context, job *serviceJob, done <-chan struct{}) {
	ticker := time.NewTicker(service.heartbeatEvery)
	defer ticker.Stop()
	sequence := 0
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			sequence++
			if err := service.callback(ctx, job, domain.AgentTaskAdapterEvent{AttemptID: parseAdapterUUID(job.submission.AttemptID), AdapterJobID: job.id, DeliveryID: fmt.Sprintf("adapter:%s:heartbeat:%d", job.id, sequence), Kind: "heartbeat"}); err != nil {
				log.Printf("agent adapter lease heartbeat failed for job %s: %v", job.id, err)
				job.cancel()
				return
			}
		}
	}
}

func (service *Service) callback(parent context.Context, job *serviceJob, event domain.AgentTaskAdapterEvent) error {
	if event.AttemptID == uuid.Nil || !event.Valid() {
		return fmt.Errorf("adapter callback event is invalid")
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, service.callbackTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, service.callbackURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	timestamp := strconvUnix(time.Now())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderSignature, Sign(service.secret, timestamp, body))
	response, err := service.callbackClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return callbackStatusError{StatusCode: response.StatusCode, Status: response.Status}
	}
	return nil
}

type callbackStatusError struct {
	StatusCode int
	Status     string
}

func (err callbackStatusError) Error() string {
	return "control-plane adapter callback returned " + err.Status
}

func (service *Service) validSubmission(submission Submission) bool {
	if !submission.Limits.Workflow.Valid() {
		return false
	}
	if strings.TrimSpace(submission.CallbackURL) != service.callbackURL || parseAdapterUUID(submission.AttemptID) == uuid.Nil || parseAdapterUUID(submission.Task.InstallationID) == uuid.Nil || !submission.Task.Provider.Valid() || strings.TrimSpace(submission.Task.APIBaseURL) == "" || strings.Trim(strings.TrimSpace(submission.Task.Repository), "/") == "" || submission.Task.OriginNumber < 1 || strings.TrimSpace(submission.Task.OriginRevision) == "" || (submission.Task.ExecutorProfile != "codex" && submission.Task.ExecutorProfile != "claude") || !validSourcePair(submission.Task.SourceBaseRef, submission.Task.SourceBaseSHA) || strings.TrimSpace(submission.Task.BranchName) == "" || submission.Plan.Revision < 1 || len(strings.TrimSpace(submission.Plan.SHA256)) != 64 || submission.Limits.MaxAttempts < 1 || submission.Limits.MaxAttempts > 3 || submission.Limits.MaxExecutionSeconds < 60 || submission.Limits.MaxExecutionSeconds > 7200 || !validRFC3339(submission.Limits.DeadlineAt) {
		return false
	}
	if (submission.Task.OriginKind == "pull_request" && (submission.Task.Feedback == nil || !submission.Task.Feedback.ExecutionValid())) || (submission.Task.OriginKind == "issue" && submission.Task.Feedback != nil) || (submission.Task.OriginKind != "issue" && submission.Task.OriginKind != "pull_request") {
		return false
	}
	planDigest := sha256.Sum256([]byte(submission.Plan.Summary))
	return strings.TrimSpace(submission.Plan.Summary) != "" && strings.EqualFold(submission.Plan.SHA256, hex.EncodeToString(planDigest[:])) && strings.HasPrefix(submission.Task.BranchName, "agent/")
}

func validExecutionResult(submission Submission, result ExecutionResult, policy domain.AgentDraftURLPolicy) bool {
	if !validAdapterVerificationEvidence(result.VerificationProfileSHA256, result.VerificationOutputSHA256, result.VerificationOutputBytes) {
		return false
	}
	if result.VerificationProfileSHA256 != "" && result.PatchSHA256 == "" {
		return false
	}
	if result.PatchSHA256 != "" || result.ChangedFileCount != 0 || result.DiffBytes != 0 {
		decoded, err := hex.DecodeString(result.PatchSHA256)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != result.PatchSHA256 || result.ChangedFileCount <= 0 || result.DiffBytes <= 0 {
			return false
		}
	}
	return strings.TrimSpace(result.Summary) != "" && len(result.Summary) <= 4000 && result.BranchName == submission.Task.BranchName && validSourcePair("base", result.HeadSHA) && validAdapterResultURL(submission, result.PullRequestURL, result.PullRequestNumber, policy)
}

func validAdapterVerificationEvidence(profileSHA, outputSHA string, outputBytes int64) bool {
	if profileSHA == "" && outputSHA == "" && outputBytes == 0 {
		return true
	}
	if outputBytes < 0 || outputBytes > maxExecutorOutputBytes {
		return false
	}
	for _, value := range []string{profileSHA, outputSHA} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value {
			return false
		}
	}
	return true
}

func validSourcePair(ref, sha string) bool {
	return (domain.AgentTaskSourceSnapshot{BaseRef: ref, BaseSHA: sha}).Valid()
}
func validRFC3339(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}
func parseAdapterUUID(value string) uuid.UUID {
	id, _ := uuid.Parse(strings.TrimSpace(value))
	return id
}
func boundedSummary(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 4000 {
		return value[:4000]
	}
	return value
}

func validAdapterResultURL(submission Submission, value string, number int, policy domain.AgentDraftURLPolicy) bool {
	_, err := policy.Canonical(submission.Task.Provider, submission.Task.APIBaseURL, submission.Task.Repository, number, value)
	return err == nil
}
func (service *Service) forget(job *serviceJob) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return
	}
	if service.byJob[job.id] != job {
		return
	}
	delete(service.byAttempt, job.submission.AttemptID)
	delete(service.byJob, job.id)
	if err := service.receipts.remove(job.submission.AttemptID); err != nil {
		log.Printf("agent adapter could not expire receipt for job %s: %v", job.id, err)
	}
}

// retain bounds local receipt storage after an attempt's maximum execution
// window. The control-plane lease remains the authoritative start fence.
func (service *Service) retain(job *serviceJob) {
	remaining := time.Until(job.createdAt.Add(service.receiptRetention))
	if remaining < 0 {
		remaining = 0
	}
	job.retention = time.AfterFunc(remaining, func() { service.forget(job) })
}
func newServiceJobID() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "adapter-" + hex.EncodeToString(bytes), nil
}
func writeServiceError(writer http.ResponseWriter, status int, message string) {
	writeServiceJSON(writer, status, map[string]string{"error": message})
}
func writeServiceJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
