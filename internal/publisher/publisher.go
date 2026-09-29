package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type Publisher interface {
	Publish(context.Context, domain.ReviewJob, ReviewResult) error
}

// ReviewRevisionVerifier performs a read-only provider check before the
// runner spends model budget or writes a started status. Publication repeats
// the check to close the later execution-to-publication race.
type ReviewRevisionVerifier interface {
	VerifyCurrentReview(context.Context, domain.ReviewJob) error
}

// ReceiptPublisher exposes the stable markers that were successfully written
// during a publication attempt. The runner persists them transactionally in
// the control plane; a plain Publisher remains supported for compatibility.
type ReceiptPublisher interface {
	PublishWithReceipts(context.Context, domain.ReviewJob, ReviewResult) ([]domain.PublicationReceipt, error)
}

// HTTPStatusError preserves retry-relevant status without retaining untrusted
// provider response bodies in job failures, audit evidence, or logs.
type HTTPStatusError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("provider returned HTTP %d", e.StatusCode)
}

// RetryAfter returns the bounded provider-directed wait for a retryable
// request. Callers should combine it with their own exponential backoff.
func RetryAfter(err error) time.Duration {
	var providerErr *HTTPStatusError
	if !errors.As(err, &providerErr) || providerErr.RetryAfter <= 0 {
		return 0
	}
	return providerErr.RetryAfter
}

// IsTerminalPublicationError identifies provider responses that cannot be
// repaired by replaying the exact same publication. Rate limits, conflicts,
// timeouts and 5xx responses remain retryable.
func IsTerminalPublicationError(err error) bool {
	var providerErr *HTTPStatusError
	if !errors.As(err, &providerErr) {
		return false
	}
	switch providerErr.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

// ErrReviewHeadChanged means the provider now points the PR/MR at a different
// commit from the immutable execution input. It is a terminal supersession,
// not a transient publication failure: retrying this job would publish stale
// evidence against the wrong revision.
var ErrReviewHeadChanged = errors.New("provider review head changed")

// ErrReviewNotOpen means a queued review no longer targets an open PR/MR.
// It is a terminal supersession, not a reason to run the model again.
var ErrReviewNotOpen = errors.New("provider review is not open")

// ErrInvalidReviewRevision means persisted execution input is not a complete
// Git commit identity. It is a local terminal failure: do not send it to a
// provider or a model and do not retry it as a transient transport error.
var ErrInvalidReviewRevision = errors.New("review revision is not a full commit sha")

// ErrProviderPaginationLimit prevents a retry from treating an incomplete
// marker scan as an empty result and duplicating provider comments. The limit
// is deliberately high enough for normal active pull requests, while still
// bounding one worker attempt against an untrusted provider response.
var ErrProviderPaginationLimit = errors.New("provider marker scan exceeded the safe pagination limit")

const (
	providerPageSize = 100
	maxProviderPages = 50
)

type providerComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

type gitLabDiscussion struct {
	Notes []providerComment `json:"notes"`
}

// LifecycleReporter owns the single evolving PR/MR status comment. Keeping it
// separate from findings publication lets the runner acknowledge work and
// report terminal states even when the review engine never returns findings.
type LifecycleReporter interface {
	PublishStarted(context.Context, domain.ReviewJob) error
	PublishTerminal(context.Context, domain.ReviewJob, LifecycleState) error
}

// ProgressReporter is optional to retain compatibility with lightweight
// lifecycle fakes and older deployments. HTTPPublisher updates the same stable
// status comment after the run reaches its preparing stage.
type ProgressReporter interface {
	PublishProgress(context.Context, domain.ReviewJob) error
}

// ReviewConfigSnapshotReader exposes the immutable configuration captured at
// review admission. Publisher keeps this narrow dependency so status reporting
// cannot read mutable workspace settings while a run is in flight.
type ReviewConfigSnapshotReader interface {
	ReviewConfigSnapshotForJob(context.Context, uuid.UUID, domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error)
}

// ReviewRunLocator maps a legacy execution job to the durable review-run id
// used by Console routes. It is deliberately optional so focused publishers
// and legacy callers retain their existing snapshot-only contract; without a
// trustworthy mapping we omit a deep link rather than link to a job id that
// the Console cannot resolve.
type ReviewRunLocator interface {
	ReviewRunIDForJob(context.Context, uuid.UUID) (uuid.UUID, error)
}

type HTTPPublisher struct {
	client     *http.Client
	resolver   credentials.Resolver
	snapshots  ReviewConfigSnapshotReader
	consoleURL string
}

func NewHTTP(githubToken, gitlabToken string) *HTTPPublisher {
	return NewHTTPWithResolver(&credentials.ProviderResolver{GitHubToken: githubToken, GitLabToken: gitlabToken})
}

func NewHTTPWithResolver(resolver credentials.Resolver) *HTTPPublisher {
	return NewHTTPWithResolverAndSnapshots(resolver, nil)
}

// NewHTTPWithResolverAndSnapshots keeps provider credentials and immutable run
// configuration separate. A nil reader deliberately preserves compatibility
// for focused publisher callers and legacy runs.
func NewHTTPWithResolverAndSnapshots(resolver credentials.Resolver, snapshots ReviewConfigSnapshotReader) *HTTPPublisher {
	return NewHTTPWithResolverAndSnapshotsAndConsoleURL(resolver, snapshots, "")
}

// NewHTTPWithResolverAndSnapshotsAndConsoleURL adds only a deployment-owned,
// public Console origin. It intentionally does not accept a request-derived
// value because the rendered provider comment is visible outside the control
// plane.
func NewHTTPWithResolverAndSnapshotsAndConsoleURL(resolver credentials.Resolver, snapshots ReviewConfigSnapshotReader, consoleURL string) *HTTPPublisher {
	return &HTTPPublisher{
		client:     &http.Client{Timeout: 30 * time.Second},
		resolver:   resolver,
		snapshots:  snapshots,
		consoleURL: strings.TrimSpace(consoleURL),
	}
}

func (p *HTTPPublisher) consoleLinksForJob(ctx context.Context, job domain.ReviewJob) (reviewURL, commandsURL string) {
	locator, ok := p.snapshots.(ReviewRunLocator)
	if !ok || job.ID == uuid.Nil {
		return "", ""
	}
	runID, err := locator.ReviewRunIDForJob(ctx, job.ID)
	if err != nil || runID == uuid.Nil {
		return "", ""
	}
	job.ID = runID
	return consoleLinks(p.consoleURL, job)
}

func (p *HTTPPublisher) Publish(ctx context.Context, job domain.ReviewJob, result ReviewResult) error {
	_, err := p.PublishWithReceipts(ctx, job, result)
	return err
}

func (p *HTTPPublisher) PublishWithReceipts(ctx context.Context, job domain.ReviewJob, result ReviewResult) ([]domain.PublicationReceipt, error) {
	if err := p.ValidateReviewRevision(job); err != nil {
		return nil, err
	}
	// Publication and replay use the admitted repository policy, not a mutable
	// caller-selected language.
	result.Language = p.reviewLanguage(ctx, job)
	token, err := p.resolve(ctx, job)
	if err != nil {
		return nil, err
	}
	if err := p.verifyCurrentHead(ctx, job, token); err != nil {
		return nil, err
	}
	if job.Provider == domain.ProviderGitHub {
		return p.publishGitHubWithReceipts(ctx, job, token, result)
	}
	if job.Provider == domain.ProviderGitLab {
		return p.publishGitLabWithReceipts(ctx, job, token, result)
	}
	return nil, fmt.Errorf("unsupported provider %q", job.Provider)
}

func (p *HTTPPublisher) VerifyCurrentReview(ctx context.Context, job domain.ReviewJob) error {
	if err := p.ValidateReviewRevision(job); err != nil {
		return err
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	return p.verifyCurrentHead(ctx, job, token)
}

// ValidateReviewRevision deliberately needs no credential or network access.
// Terminal recovery uses it before deciding whether a historical job is safe
// to publish against a provider's status/comment surfaces.
func (p *HTTPPublisher) ValidateReviewRevision(job domain.ReviewJob) error {
	if !fullCommitSHA(job.BaseSHA) || !fullCommitSHA(job.HeadSHA) {
		return ErrInvalidReviewRevision
	}
	return nil
}

func fullCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') && (character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

// verifyCurrentHead closes the race between an isolated checkout and provider
// publication. A webhook for a new push normally supersedes the old run, but
// the provider remains the final authority at this trust boundary.
func (p *HTTPPublisher) verifyCurrentHead(ctx context.Context, job domain.ReviewJob, token string) error {
	expected := strings.TrimSpace(job.HeadSHA)
	if expected == "" {
		return fmt.Errorf("review job head sha is required before publication")
	}
	var observed, state string
	switch job.Provider {
	case domain.ProviderGitHub:
		var pullRequest struct {
			State string `json:"state"`
			Head  struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d", githubAPIBase(job.APIBaseURL), job.Repository, job.ReviewNumber)
		if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &pullRequest); err != nil {
			return fmt.Errorf("load GitHub pull request head before publication: %w", err)
		}
		observed = pullRequest.Head.SHA
		state = pullRequest.State
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(job.APIBaseURL, "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		var mergeRequest struct {
			SHA   string `json:"sha"`
			State string `json:"state"`
		}
		endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d", base, url.PathEscape(job.Repository), job.ReviewNumber)
		if err := p.requestJSON(ctx, http.MethodGet, endpoint, token, nil, &mergeRequest); err != nil {
			return fmt.Errorf("load GitLab merge request head before publication: %w", err)
		}
		observed = mergeRequest.SHA
		state = mergeRequest.State
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
	observed = strings.TrimSpace(observed)
	if strings.TrimSpace(state) == "" {
		return fmt.Errorf("provider review state is missing before publication")
	}
	if (job.Provider == domain.ProviderGitHub && state != "open") || (job.Provider == domain.ProviderGitLab && state != "opened") {
		return fmt.Errorf("%w: observed %s", ErrReviewNotOpen, state)
	}
	if observed == "" {
		return fmt.Errorf("provider review head is missing before publication")
	}
	if !strings.EqualFold(observed, expected) {
		return fmt.Errorf("%w: expected %s, observed %s", ErrReviewHeadChanged, expected, observed)
	}
	return nil
}

func (p *HTTPPublisher) PublishStarted(ctx context.Context, job domain.ReviewJob) error {
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	body := StartedReportForLanguage(job, p.loadReviewContext(ctx, job, token), p.lifecycleMessage(ctx, job, "started"), marker, p.reviewLanguage(ctx, job))
	switch job.Provider {
	case domain.ProviderGitHub:
		return p.githubUpsertSummary(ctx, githubAPIBase(job.APIBaseURL), job, token, body)
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(job.APIBaseURL, "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		return p.gitLabUpsertSummary(ctx, base, url.PathEscape(job.Repository), job, token, body)
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

func (p *HTTPPublisher) PublishTerminal(ctx context.Context, job domain.ReviewJob, state LifecycleState) error {
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	body := TerminalReportForLanguage(job, state, p.lifecycleMessage(ctx, job, terminalMessageKey(state)), marker, p.reviewLanguage(ctx, job))
	switch job.Provider {
	case domain.ProviderGitHub:
		return p.githubUpsertSummary(ctx, githubAPIBase(job.APIBaseURL), job, token, body)
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(job.APIBaseURL, "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		return p.gitLabUpsertSummary(ctx, base, url.PathEscape(job.Repository), job, token, body)
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

func (p *HTTPPublisher) PublishProgress(ctx context.Context, job domain.ReviewJob) error {
	message := p.lifecycleMessage(ctx, job, "progress")
	if message == "" {
		return nil
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return err
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	body := ProgressReportForLanguage(job, p.loadReviewContext(ctx, job, token), message, marker, p.reviewLanguage(ctx, job))
	switch job.Provider {
	case domain.ProviderGitHub:
		return p.githubUpsertSummary(ctx, githubAPIBase(job.APIBaseURL), job, token, body)
	case domain.ProviderGitLab:
		base := strings.TrimSuffix(job.APIBaseURL, "/")
		if base == "" {
			base = "https://gitlab.com/api/v4"
		}
		return p.gitLabUpsertSummary(ctx, base, url.PathEscape(job.Repository), job, token, body)
	default:
		return fmt.Errorf("unsupported provider %q", job.Provider)
	}
}

// lifecycleMessage resolves only the messages snapshot taken when this run was
// admitted. Missing, malformed, and pre-snapshot runs retain the canonical
// report rather than turning provider publication into a new failure mode.
func (p *HTTPPublisher) lifecycleMessage(ctx context.Context, job domain.ReviewJob, key string) string {
	if p.snapshots == nil || job.ID == uuid.Nil || strings.TrimSpace(key) == "" {
		return ""
	}
	snapshot, err := p.snapshots.ReviewConfigSnapshotForJob(ctx, job.ID, domain.ReviewConfigMessages)
	if err != nil {
		return ""
	}
	var messages map[string]string
	if err := json.Unmarshal(snapshot.Content, &messages); err != nil {
		return ""
	}
	message := strings.TrimSpace(messages[key])
	if message == "" && key != "completed" {
		// A persisted revision from the initial configuration format used one
		// generic completed message. Keep it valid for runs that snapshot it.
		message = strings.TrimSpace(messages["completed"])
	}
	return message
}

func (p *HTTPPublisher) reviewLanguage(ctx context.Context, job domain.ReviewJob) string {
	if p.snapshots == nil || job.ID == uuid.Nil {
		return "en"
	}
	snapshot, err := p.snapshots.ReviewConfigSnapshotForJob(ctx, job.ID, domain.ReviewConfigGeneral)
	if err != nil {
		return "en"
	}
	general, err := domain.DecodeReviewGeneralConfig(snapshot.Content)
	if err != nil {
		return "en"
	}
	return general.ReviewLanguage
}

func (p *HTTPPublisher) summaryConfig(ctx context.Context, job domain.ReviewJob) domain.ReviewSummaryConfig {
	fallback := domain.DefaultReviewSummaryConfig()
	if p.snapshots == nil || job.ID == uuid.Nil {
		return fallback
	}
	snapshot, err := p.snapshots.ReviewConfigSnapshotForJob(ctx, job.ID, domain.ReviewConfigSummary)
	if err != nil {
		return fallback
	}
	config, err := domain.DecodeReviewSummaryConfig(snapshot.Content)
	if err != nil {
		return fallback
	}
	return config
}

func completionMessageKey(result ReviewResult) string {
	if result.Gate.Conclusion == CheckFailure {
		return "blocked"
	}
	if len(result.Findings) > 0 {
		return "recommendation"
	}
	return "success"
}

func terminalMessageKey(state LifecycleState) string {
	if state == LifecycleSuperseded {
		return "superseded"
	}
	if state == LifecycleNeedsAttention {
		return "needs_attention"
	}
	return "failed"
}

// PublishInteractionResponse writes a marker-keyed reply or acknowledges a
// review command with only an idempotent reaction after it is committed.
func (p *HTTPPublisher) PublishInteractionResponse(ctx context.Context, response domain.InteractionResponse) error {
	publish, err := p.PrepareInteractionResponse(ctx, response)
	if err != nil {
		return err
	}
	return publish(ctx)
}

// PrepareInteractionResponse resolves the short-lived provider credential
// before a caller acquires its database publication fence. The returned
// closure performs only provider HTTP calls; it never stores the token.
func (p *HTTPPublisher) PrepareInteractionResponse(ctx context.Context, response domain.InteractionResponse) (func(context.Context) error, error) {
	if response.ReactionOnly && (response.ResourceKind != "merge_request" || response.Reaction != domain.InteractionReactionEyes || response.Body != "" || response.CommentExternalID == "" || response.SourceRelease != nil || response.StatusVersion != 0) {
		return nil, fmt.Errorf("reaction-only review acknowledgement is invalid")
	}
	if response.Provider == domain.ProviderGitHub && response.Reaction != domain.InteractionReactionNone {
		if _, err := githubInteractionCommentID(response.CommentExternalID); err != nil {
			return nil, err
		}
	}
	if response.Provider != domain.ProviderGitHub && response.Provider != domain.ProviderGitLab {
		return nil, fmt.Errorf("unsupported interaction provider %q", response.Provider)
	}
	job := domain.ReviewJob{
		TenantID:               response.TenantID,
		Provider:               response.Provider,
		APIBaseURL:             response.APIBaseURL,
		InstallationExternalID: response.InstallationExternalID,
		CredentialRef:          response.CredentialRef,
	}
	token, err := p.resolve(ctx, job)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		if response.ReactionOnly {
			if response.Provider == domain.ProviderGitLab {
				return p.publishGitLabInteractionReaction(ctx, response, token)
			}
			return p.publishGitHubInteractionReaction(ctx, response, token)
		}
		if response.Provider == domain.ProviderGitLab {
			if err := p.publishGitLabInteractionResponse(ctx, response, token); err != nil {
				return err
			}
			if response.Reaction == domain.InteractionReactionNone {
				return nil
			}
			return p.publishGitLabInteractionReaction(ctx, response, token)
		}
		if err := p.publishGitHubInteractionResponse(ctx, response, token); err != nil {
			return err
		}
		if response.Reaction == domain.InteractionReactionNone {
			return nil
		}
		return p.publishGitHubInteractionReaction(ctx, response, token)
	}, nil
}

func (p *HTTPPublisher) publishGitHubInteractionResponse(ctx context.Context, response domain.InteractionResponse, token string) error {
	base := githubAPIBase(response.APIBaseURL)
	body := interactionResponseBody(response)
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments", base, response.Repository, response.ReviewNumber)
	comments, err := p.githubIssueCommentsSince(ctx, endpoint, token, response.MarkerSince)
	if err != nil {
		return fmt.Errorf("list GitHub interaction comments: %w", err)
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, response.Marker) {
			if interactionStatusVersion(comment.Body, response.Marker) > response.StatusVersion {
				return nil // A later source revision already reached the provider.
			}
			endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d", base, response.Repository, comment.ID)
			return p.requestJSON(ctx, http.MethodPatch, endpoint, token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}

func interactionResponseBody(response domain.InteractionResponse) string {
	body := "## Open Review Platform\n\n" + response.Body + "\n\n<!-- " + response.Marker + " -->"
	if response.StatusVersion > 0 {
		body += "\n<!-- " + response.Marker + ":status-version=" + strconv.Itoa(response.StatusVersion) + " -->"
	}
	return body
}

// interactionStatusVersion is only a publication-order fence, not an
// authorization source. Legacy comments without a version start at zero.
func interactionStatusVersion(body, marker string) int {
	prefix := "<!-- " + marker + ":status-version="
	start := strings.LastIndex(body, prefix)
	if start < 0 {
		return 0
	}
	remainder := body[start+len(prefix):]
	end := strings.Index(remainder, " -->")
	if end < 0 {
		return 0
	}
	version, err := strconv.Atoi(remainder[:end])
	if err != nil || version < 1 {
		return 0
	}
	return version
}

// GitHub makes a repeated app/content reaction idempotent. For reaction-only
// acknowledgements this write is the ordering barrier before work is released.
func (p *HTTPPublisher) publishGitHubInteractionReaction(ctx context.Context, response domain.InteractionResponse, token string) error {
	commentID, err := githubInteractionCommentID(response.CommentExternalID)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d/reactions", githubAPIBase(response.APIBaseURL), response.Repository, commentID)
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"content": string(response.Reaction)}, nil); err != nil {
		return fmt.Errorf("publish GitHub interaction reaction: %w", err)
	}
	return nil
}

func githubInteractionCommentID(externalID string) (int64, error) {
	commentID, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil || commentID < 1 {
		return 0, fmt.Errorf("invalid GitHub interaction comment id")
	}
	return commentID, nil
}

func githubAPIBase(apiBaseURL string) string {
	if base := strings.TrimSuffix(apiBaseURL, "/"); base != "" {
		return base
	}
	return "https://api.github.com"
}

// collectProviderPages is shared by every marker-keyed comment lookup. Each
// list endpoint is queried with a deterministic page number; a full final
// page is never silently assumed to be the end of the result set.
func collectProviderPages[T any](list func(page int) ([]T, error)) ([]T, error) {
	items := make([]T, 0, providerPageSize)
	for page := 1; page <= maxProviderPages; page++ {
		current, err := list(page)
		if err != nil {
			return nil, err
		}
		items = append(items, current...)
		if len(current) < providerPageSize {
			return items, nil
		}
	}
	return nil, fmt.Errorf("%w (%d pages of %d items)", ErrProviderPaginationLimit, maxProviderPages, providerPageSize)
}

func providerListPage(endpoint string, page int) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse provider pagination endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("provider pagination endpoint must be absolute")
	}
	query := parsed.Query()
	query.Set("per_page", strconv.Itoa(providerPageSize))
	query.Set("page", strconv.Itoa(page))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (p *HTTPPublisher) githubReviewComments(ctx context.Context, endpoint, token string) ([]providerComment, error) {
	return collectProviderPages(func(page int) ([]providerComment, error) {
		var comments []providerComment
		pageEndpoint, err := providerListPage(endpoint, page)
		if err != nil {
			return nil, err
		}
		if err := p.requestJSON(ctx, http.MethodGet, pageEndpoint, token, nil, &comments); err != nil {
			return nil, err
		}
		return comments, nil
	})
}

func (p *HTTPPublisher) githubIssueComments(ctx context.Context, endpoint, token string) ([]providerComment, error) {
	return p.githubIssueCommentsSince(ctx, endpoint, token, time.Time{})
}

// githubIssueCommentsSince keeps marker discovery exact while bounding the
// acknowledgement path by the interaction's immutable creation time. A
// marker is only ever created after the interaction is committed, including
// a provider timeout/retry, so older comments cannot affect deduplication.
func (p *HTTPPublisher) githubIssueCommentsSince(ctx context.Context, endpoint, token string, since time.Time) ([]providerComment, error) {
	if !since.IsZero() {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse GitHub interaction endpoint: %w", err)
		}
		query := parsed.Query()
		query.Set("since", since.UTC().Format(time.RFC3339))
		parsed.RawQuery = query.Encode()
		endpoint = parsed.String()
	}
	return collectProviderPages(func(page int) ([]providerComment, error) {
		var comments []providerComment
		pageEndpoint, err := providerListPage(endpoint, page)
		if err != nil {
			return nil, err
		}
		if err := p.requestJSON(ctx, http.MethodGet, pageEndpoint, token, nil, &comments); err != nil {
			return nil, err
		}
		return comments, nil
	})
}

func (p *HTTPPublisher) gitLabNotes(ctx context.Context, endpoint, token string) ([]providerComment, error) {
	return collectProviderPages(func(page int) ([]providerComment, error) {
		var notes []providerComment
		pageEndpoint, err := providerListPage(endpoint, page)
		if err != nil {
			return nil, err
		}
		if err := p.requestJSON(ctx, http.MethodGet, pageEndpoint, token, nil, &notes); err != nil {
			return nil, err
		}
		return notes, nil
	})
}

func (p *HTTPPublisher) gitLabDiscussions(ctx context.Context, endpoint, token string) ([]gitLabDiscussion, error) {
	return collectProviderPages(func(page int) ([]gitLabDiscussion, error) {
		var discussions []gitLabDiscussion
		pageEndpoint, err := providerListPage(endpoint, page)
		if err != nil {
			return nil, err
		}
		if err := p.requestJSON(ctx, http.MethodGet, pageEndpoint, token, nil, &discussions); err != nil {
			return nil, err
		}
		return discussions, nil
	})
}

func (p *HTTPPublisher) publishGitLabInteractionResponse(ctx context.Context, response domain.InteractionResponse, token string) error {
	base := strings.TrimSuffix(response.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(response.Repository)
	body := interactionResponseBody(response)
	resource := "merge_requests"
	if response.ResourceKind == "issue" {
		resource = "issues"
	}
	endpoint := fmt.Sprintf("%s/projects/%s/%s/%d/notes", base, project, resource, response.ReviewNumber)
	comments, err := p.gitLabNotes(ctx, endpoint, token)
	if err != nil {
		return fmt.Errorf("list GitLab interaction notes: %w", err)
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, response.Marker) {
			if interactionStatusVersion(comment.Body, response.Marker) > response.StatusVersion {
				return nil // Do not replace a newer source result with a delayed retry.
			}
			endpoint := fmt.Sprintf("%s/projects/%s/%s/%d/notes/%d", base, project, resource, response.ReviewNumber, comment.ID)
			return p.requestJSON(ctx, http.MethodPut, endpoint, token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}

// A duplicate GitLab award is an idempotent success. A missing source note is
// best-effort only when a separate marker-keyed reply was already published;
// reaction-only acknowledgement must fail closed instead of releasing work.
func (p *HTTPPublisher) publishGitLabInteractionReaction(ctx context.Context, response domain.InteractionResponse, token string) error {
	commentID := strings.TrimSpace(response.CommentExternalID)
	if commentID == "" {
		return fmt.Errorf("invalid GitLab interaction comment id")
	}
	base := strings.TrimSuffix(response.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	resource := "merge_requests"
	if response.ResourceKind == "issue" {
		resource = "issues"
	}
	endpoint := fmt.Sprintf("%s/projects/%s/%s/%d/notes/%s/award_emoji?name=%s", base, url.PathEscape(response.Repository), resource, response.ReviewNumber, url.PathEscape(commentID), url.QueryEscape(string(response.Reaction)))
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, nil, nil); err != nil {
		if status, ok := err.(*HTTPStatusError); ok && (status.StatusCode == http.StatusConflict || (status.StatusCode == http.StatusNotFound && !response.ReactionOnly)) {
			return nil
		}
		return fmt.Errorf("publish GitLab interaction reaction: %w", err)
	}
	return nil
}

func (p *HTTPPublisher) publishGitHub(ctx context.Context, job domain.ReviewJob, token string, result ReviewResult) error {
	_, err := p.publishGitHubWithReceipts(ctx, job, token, result)
	return err
}

type githubPendingFinding struct {
	comment githubInlineComment
	receipt domain.PublicationReceipt
}

func (p *HTTPPublisher) publishGitHubWithReceipts(ctx context.Context, job domain.ReviewJob, token string, result ReviewResult) ([]domain.PublicationReceipt, error) {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	findings := result.Findings
	existing, err := p.githubMarkers(ctx, base, job, token)
	if err != nil {
		return nil, err
	}
	receipts := make([]domain.PublicationReceipt, 0, len(findings)+1)
	var inline []githubPendingFinding
	for _, finding := range findings {
		marker := findingMarker(job, finding)
		receipt := publicationReceipt("inline_finding", marker, FindingReportForLanguage(job, finding, marker, result.Language))
		if existing[marker] {
			receipts = append(receipts, receipt)
			continue
		}
		if canInline(job, finding) {
			inline = append(inline, githubPendingFinding{comment: githubInlineComment{Path: finding.Path, Line: finding.EndLine, Side: "RIGHT", Body: FindingReportForLanguage(job, finding, marker, result.Language)}, receipt: receipt})
		}
	}
	for start := 0; start < len(inline); start += 25 {
		end := min(start+25, len(inline))
		pending := inline[start:end]
		comments := make([]githubInlineComment, 0, len(pending))
		for _, item := range pending {
			comments = append(comments, item.comment)
		}
		payload := struct {
			Body     string                `json:"body"`
			CommitID string                `json:"commit_id"`
			Event    string                `json:"event"`
			Comments []githubInlineComment `json:"comments"`
		}{
			Body:     localizedInlineReviewSummary(findings, result.Language) + "\n\n<!-- open-review-platform:review:" + job.ID.String() + " -->",
			CommitID: job.HeadSHA,
			Event:    "COMMENT",
			Comments: comments,
		}
		endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/reviews", base, job.Repository, job.ReviewNumber)
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, payload, nil); err != nil {
			return receipts, fmt.Errorf("publish GitHub inline review: %w", err)
		}
		for _, item := range pending {
			receipts = append(receipts, item.receipt)
		}
	}
	// Inline annotations are easy to miss in GitHub's Files view. Always leave
	// one concise, updatable PR-level result so authors know whether the bot
	// recommends changes or found no actionable risk.
	body := CompletedReportWithSummary(job, p.loadReviewContext(ctx, job, token), result, p.summaryConfig(ctx, job), p.lifecycleMessage(ctx, job, completionMessageKey(result)), "open-review-platform:summary:"+job.ID.String())
	if err := p.githubUpsertSummary(ctx, base, job, token, body); err != nil {
		return receipts, err
	}
	return append(receipts, publicationReceipt("summary", "open-review-platform:summary:"+job.ID.String(), body)), nil
}

func (p *HTTPPublisher) githubMarkers(ctx context.Context, base string, job domain.ReviewJob, token string) (map[string]bool, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d/comments", base, job.Repository, job.ReviewNumber)
	comments, err := p.githubReviewComments(ctx, endpoint, token)
	if err != nil {
		return nil, fmt.Errorf("list GitHub review comments: %w", err)
	}
	return markers(comments), nil
}

func (p *HTTPPublisher) githubUpsertSummary(ctx context.Context, base string, job domain.ReviewJob, token, body string) error {
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments", base, job.Repository, job.ReviewNumber)
	comments, err := p.githubIssueComments(ctx, endpoint, token)
	if err != nil {
		return fmt.Errorf("list GitHub summary comments: %w", err)
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	for _, comment := range comments {
		if strings.Contains(comment.Body, marker) {
			endpoint := fmt.Sprintf("%s/repos/%s/issues/comments/%d", base, job.Repository, comment.ID)
			return p.requestJSON(ctx, http.MethodPatch, endpoint, token, map[string]string{"body": body}, nil)
		}
	}
	return p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil)
}

func (p *HTTPPublisher) publishGitLab(ctx context.Context, job domain.ReviewJob, token string, result ReviewResult) error {
	_, err := p.publishGitLabWithReceipts(ctx, job, token, result)
	return err
}

func (p *HTTPPublisher) publishGitLabWithReceipts(ctx context.Context, job domain.ReviewJob, token string, result ReviewResult) ([]domain.PublicationReceipt, error) {
	base := strings.TrimSuffix(job.APIBaseURL, "/")
	if base == "" {
		base = "https://gitlab.com/api/v4"
	}
	project := url.PathEscape(job.Repository)
	findings := result.Findings
	existing, err := p.gitLabMarkers(ctx, base, project, job, token)
	if err != nil {
		return nil, err
	}
	receipts := make([]domain.PublicationReceipt, 0, len(findings)+1)
	for _, finding := range findings {
		marker := findingMarker(job, finding)
		body := FindingReportForLanguage(job, finding, marker, result.Language)
		receipt := publicationReceipt("inline_finding", marker, body)
		if existing[marker] {
			receipts = append(receipts, receipt)
			continue
		}
		if canInline(job, finding) && job.BaseSHA != "" {
			payload := struct {
				Body     string `json:"body"`
				Position struct {
					PositionType string `json:"position_type"`
					BaseSHA      string `json:"base_sha"`
					StartSHA     string `json:"start_sha"`
					HeadSHA      string `json:"head_sha"`
					NewPath      string `json:"new_path"`
					NewLine      int    `json:"new_line"`
				} `json:"position"`
			}{Body: body}
			payload.Position.PositionType = "text"
			payload.Position.BaseSHA, payload.Position.StartSHA, payload.Position.HeadSHA = job.BaseSHA, job.BaseSHA, job.HeadSHA
			payload.Position.NewPath, payload.Position.NewLine = finding.Path, finding.EndLine
			endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/discussions", base, project, job.ReviewNumber)
			if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, payload, nil); err != nil {
				return receipts, fmt.Errorf("publish GitLab inline discussion: %w", err)
			}
			receipts = append(receipts, receipt)
			continue
		}
		endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", base, project, job.ReviewNumber)
		if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil); err != nil {
			return receipts, fmt.Errorf("publish GitLab note: %w", err)
		}
		receipts = append(receipts, receipt)
	}
	body := CompletedReportWithSummary(job, p.loadReviewContext(ctx, job, token), result, p.summaryConfig(ctx, job), p.lifecycleMessage(ctx, job, completionMessageKey(result)), "open-review-platform:summary:"+job.ID.String())
	if err := p.gitLabUpsertSummary(ctx, base, project, job, token, body); err != nil {
		return receipts, err
	}
	return append(receipts, publicationReceipt("summary", "open-review-platform:summary:"+job.ID.String(), body)), nil
}

func (p *HTTPPublisher) gitLabUpsertSummary(ctx context.Context, base, project string, job domain.ReviewJob, token, body string) error {
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", base, project, job.ReviewNumber)
	notes, err := p.gitLabNotes(ctx, endpoint, token)
	if err != nil {
		return fmt.Errorf("list GitLab summary notes: %w", err)
	}
	marker := "open-review-platform:summary:" + job.ID.String()
	for _, note := range notes {
		if strings.Contains(note.Body, marker) {
			return p.requestJSON(ctx, http.MethodPut, fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes/%d", base, project, job.ReviewNumber, note.ID), token, map[string]string{"body": body}, nil)
		}
	}
	if err := p.requestJSON(ctx, http.MethodPost, endpoint, token, map[string]string{"body": body}, nil); err != nil {
		return fmt.Errorf("publish GitLab summary: %w", err)
	}
	return nil
}

func (p *HTTPPublisher) gitLabMarkers(ctx context.Context, base, project string, job domain.ReviewJob, token string) (map[string]bool, error) {
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests/%d/discussions", base, project, job.ReviewNumber)
	discussions, err := p.gitLabDiscussions(ctx, endpoint, token)
	if err != nil {
		return nil, fmt.Errorf("list GitLab discussions: %w", err)
	}
	found := make(map[string]bool)
	for _, discussion := range discussions {
		for _, note := range discussion.Notes {
			for marker := range markers([]providerComment{note}) {
				found[marker] = true
			}
			for _, token := range strings.Fields(note.Body) {
				if strings.HasPrefix(token, "open-review-platform:summary:") {
					found[strings.TrimSuffix(token, "-->")] = true
				}
			}
		}
	}
	// Non-inline MR notes are not returned by the discussions endpoint. They
	// carry the same stable finding markers when GitLab cannot attach a diff
	// position, so they must participate in retry deduplication as well.
	endpoint = fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", base, project, job.ReviewNumber)
	notes, err := p.gitLabNotes(ctx, endpoint, token)
	if err != nil {
		return nil, fmt.Errorf("list GitLab merge request notes: %w", err)
	}
	for marker := range markers(notes) {
		found[marker] = true
	}
	return found, nil
}

type githubInlineComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

func (p *HTTPPublisher) requestJSON(ctx context.Context, method, endpoint, token string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal provider request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create provider request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send provider request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &HTTPStatusError{StatusCode: response.StatusCode, RetryAfter: providerRetryAfter(response.Header.Get("Retry-After"), time.Now())}
	}
	if responseBody != nil {
		if err := json.NewDecoder(response.Body).Decode(responseBody); err != nil {
			return fmt.Errorf("decode provider response: %w", err)
		}
	}
	return nil
}

func providerRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var retryAfter time.Duration
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds > 0 {
			retryAfter = time.Duration(seconds) * time.Second
		}
	} else if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		retryAfter = retryAt.Sub(now)
	}
	if retryAfter > 15*time.Minute {
		return 15 * time.Minute
	}
	return retryAfter
}

func canInline(job domain.ReviewJob, finding domain.Finding) bool {
	return job.HeadSHA != "" && finding.Path != "" && finding.StartLine > 0 && finding.EndLine >= finding.StartLine
}

func findingMarker(job domain.ReviewJob, finding domain.Finding) string {
	return domain.FindingMarker(job.ID, finding)
}

func publicationReceipt(kind, marker, payload string) domain.PublicationReceipt {
	digest := sha256.Sum256([]byte(payload))
	return domain.PublicationReceipt{
		ReceiptKind:  kind,
		StableMarker: marker,
		PayloadHash:  fmt.Sprintf("%x", digest[:]),
		Published:    true,
	}
}

// ResultSummary is used both in the PR result comment and the GitHub Check so
// the status surface communicates an actionable verdict instead of a generic
// "completed" message.
func ResultSummary(findings []domain.Finding) string {
	if len(findings) == 0 {
		return "AI analysis completed: no actionable risks were detected."
	}
	counts := make(map[string]int)
	for _, finding := range findings {
		severity := strings.ToLower(strings.TrimSpace(finding.Severity))
		if severity == "" {
			severity = "medium"
		}
		counts[severity]++
	}
	parts := make([]string, 0, len(counts))
	for _, severity := range []string{"critical", "high", "medium", "low"} {
		if count := counts[severity]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, severity))
			delete(counts, severity)
		}
	}
	for severity, count := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", count, severity))
	}
	return fmt.Sprintf("AI analysis completed: changes recommended for %d actionable finding(s) (%s).", len(findings), strings.Join(parts, ", "))
}

func markers(comments []providerComment) map[string]bool {
	found := make(map[string]bool)
	for _, comment := range comments {
		for _, token := range strings.Fields(comment.Body) {
			if strings.HasPrefix(token, "open-review-platform:finding:") {
				found[strings.TrimSuffix(token, "-->")] = true
			}
		}
	}
	return found
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
