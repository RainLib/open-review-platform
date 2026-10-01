// Package agenttasksource freezes the trusted repository base used by an
// approval-gated coding task. It is deliberately provider-read-only: it may
// resolve a short-lived token to inspect metadata, but it cannot clone, write,
// invoke a coding CLI, or create a pull request.
package agenttasksource

import (
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
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/RainLib/open-review-platform/internal/interaction"
	"github.com/google/uuid"
)

type Resolver struct {
	Resolver        credentials.Resolver
	HTTPClient      *http.Client
	AllowGitLabHTTP bool // development-only, validated by the deployment config
}

type transientSourceError struct {
	cause      error
	retryAfter time.Duration
}

func (e *transientSourceError) Error() string { return e.cause.Error() }
func (e *transientSourceError) Unwrap() error { return e.cause }

const maxSourceMetadataBytes = 1 << 20

// TransientDelay identifies only provider transport, rate-limit and server
// failures eligible for a bounded durable reread. Changed Issues, invalid
// credentials, redirects and malformed metadata must fail closed instead.
func TransientDelay(err error) (time.Duration, bool) {
	var transient *transientSourceError
	if errors.As(err, &transient) {
		return transient.retryAfter, true
	}
	return 0, false
}

func sourceRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(min(seconds, 900)) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(deadline), 0), 15*time.Minute)
	}
	return 0
}

// VerifyOrigin re-reads the exact Issue or draft PR/MR revision without
// resolving a new source commit. The isolated execution adapter calls it
// immediately before running code and again before publishing a branch, so a
// changed task request cannot silently reuse an earlier JEV decision.
func (r Resolver) VerifyOrigin(ctx context.Context, task domain.AgentTask, token string) error {
	if !task.Provider.Valid() || strings.TrimSpace(token) == "" || task.OriginNumber < 1 || strings.TrimSpace(task.OriginRevision) == "" {
		return fmt.Errorf("agent task origin verification target is invalid")
	}
	switch task.Provider {
	case domain.ProviderGitHub:
		if task.OriginKind == "issue" {
			_, err := r.githubIssue(ctx, task, token)
			return err
		}
		if task.OriginKind == "pull_request" {
			_, err := r.githubPullRequest(ctx, task, token)
			return err
		}
	case domain.ProviderGitLab:
		if task.OriginKind == "issue" {
			_, err := r.gitLabIssue(ctx, task, token)
			return err
		}
		if task.OriginKind == "pull_request" {
			_, err := r.gitLabMergeRequest(ctx, task, token)
			return err
		}
	}
	return fmt.Errorf("unsupported agent task origin")
}

// VerifyOriginWithFeedback extends the existing head/revision preflight with
// the immutable triggering comment. The adapter calls it before CLI execution
// and again before pushing, using only its separately scoped provider token.
func (r Resolver) VerifyOriginWithFeedback(ctx context.Context, task domain.AgentTask, binding *domain.AgentTaskFeedbackBinding, token string) error {
	if (task.OriginKind == "pull_request" && (binding == nil || !binding.ExecutionValid())) || (task.OriginKind == "issue" && binding != nil) {
		return fmt.Errorf("agent task feedback origin binding is invalid")
	}
	if task.OriginKind != "pull_request" {
		return r.VerifyOrigin(ctx, task, token)
	}
	var snapshot domain.AgentTaskSourceSnapshot
	var err error
	switch task.Provider {
	case domain.ProviderGitHub:
		snapshot, err = r.githubPullRequest(ctx, task, token)
	case domain.ProviderGitLab:
		snapshot, err = r.gitLabMergeRequest(ctx, task, token)
	default:
		return fmt.Errorf("unsupported agent feedback provider")
	}
	if err != nil {
		return err
	}
	if snapshot.TargetBranch != binding.TargetBranch {
		return fmt.Errorf("agent feedback Draft target branch changed")
	}
	return r.VerifyFeedbackComment(ctx, task, binding, token)
}

// VerifyFeedbackComment re-reads only the admitted comment. Publication
// recovery must not require the Draft's old head after our exact validated
// commit may already have been pushed to that same Draft branch.
func (r Resolver) VerifyFeedbackComment(ctx context.Context, task domain.AgentTask, binding *domain.AgentTaskFeedbackBinding, token string) error {
	if task.OriginKind != "pull_request" || !task.Provider.Valid() || task.OriginNumber < 1 || binding == nil || !binding.ExecutionValid() || strings.TrimSpace(token) == "" {
		return fmt.Errorf("agent task feedback comment target is invalid")
	}
	// An internal review repair is bound by the signed control-plane handoff,
	// not by a fabricated provider comment. The Draft identity/head/target is
	// still re-read by VerifyOriginWithFeedback before execution and publication.
	if binding.Internal() {
		return nil
	}
	switch task.Provider {
	case domain.ProviderGitHub:
		_, err := r.githubFeedbackComment(ctx, task, binding, token)
		return err
	case domain.ProviderGitLab:
		_, err := r.gitLabFeedbackComment(ctx, task, binding, token)
		return err
	default:
		return fmt.Errorf("unsupported agent feedback provider")
	}
}

func (r Resolver) Resolve(ctx context.Context, target domain.AgentTaskSourceTarget) (domain.AgentTaskSourceSnapshot, error) {
	if target.Task.ID == uuid.Nil || !target.Task.Provider.Valid() || strings.TrimSpace(target.Task.APIBaseURL) == "" || strings.Trim(strings.TrimSpace(target.Task.Repository), "/") == "" || strings.TrimSpace(target.InstallationExternalID) == "" || strings.TrimSpace(target.CredentialRef) == "" || r.Resolver == nil {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("agent task source target is invalid")
	}
	token, err := r.Resolver.Resolve(ctx, domain.ReviewJob{
		TenantID: target.Task.TenantID, Provider: target.Task.Provider, APIBaseURL: target.Task.APIBaseURL,
		InstallationExternalID: target.InstallationExternalID, CredentialRef: target.CredentialRef,
		Repository: target.Task.Repository, ReviewNumber: target.Task.OriginNumber,
	})
	if err != nil || strings.TrimSpace(token) == "" {
		if err == nil {
			err = fmt.Errorf("provider credential is empty")
		}
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("resolve source provider credential: %w", err)
	}
	switch target.Task.Provider {
	case domain.ProviderGitHub:
		if target.Task.OriginKind == "pull_request" {
			snapshot, err := r.githubPullRequest(ctx, target.Task, token)
			if err != nil {
				return domain.AgentTaskSourceSnapshot{}, err
			}
			if target.Feedback == nil || !target.Feedback.ExecutionValid() || snapshot.TargetBranch != target.Feedback.TargetBranch {
				return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitHub Draft target branch differs from the original approved Draft")
			}
			if target.Feedback.Internal() {
				snapshot.Feedback = internalReviewFeedback(target.Feedback)
				return snapshot, nil
			}
			snapshot.Feedback, err = r.githubFeedbackComment(ctx, target.Task, target.Feedback, token)
			return snapshot, err
		}
		issue, err := r.githubIssue(ctx, target.Task, token)
		if err != nil {
			return domain.AgentTaskSourceSnapshot{}, err
		}
		snapshot, err := r.github(ctx, target.Task, token)
		snapshot.Issue = &issue
		return snapshot, err
	case domain.ProviderGitLab:
		if target.Task.OriginKind == "pull_request" {
			snapshot, err := r.gitLabMergeRequest(ctx, target.Task, token)
			if err != nil {
				return domain.AgentTaskSourceSnapshot{}, err
			}
			if target.Feedback == nil || !target.Feedback.ExecutionValid() || snapshot.TargetBranch != target.Feedback.TargetBranch {
				return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitLab Draft target branch differs from the original approved Draft")
			}
			if target.Feedback.Internal() {
				snapshot.Feedback = internalReviewFeedback(target.Feedback)
				return snapshot, nil
			}
			snapshot.Feedback, err = r.gitLabFeedbackComment(ctx, target.Task, target.Feedback, token)
			return snapshot, err
		}
		issue, err := r.gitLabIssue(ctx, target.Task, token)
		if err != nil {
			return domain.AgentTaskSourceSnapshot{}, err
		}
		snapshot, err := r.gitLab(ctx, target.Task, token)
		snapshot.Issue = &issue
		return snapshot, err
	default:
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("unsupported source provider %q", target.Task.Provider)
	}
}

func (r Resolver) githubIssue(ctx context.Context, task domain.AgentTask, token string) (domain.AgentTaskIssueSnapshot, error) {
	parts := strings.Split(strings.Trim(task.Repository, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || task.OriginKind != "issue" {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("GitHub agent Issue target is invalid")
	}
	base, err := trustedAPIBase(task.APIBaseURL, false)
	if err != nil {
		return domain.AgentTaskIssueSnapshot{}, err
	}
	var issue struct {
		Title       string           `json:"title"`
		Body        string           `json:"body"`
		State       string           `json:"state"`
		PullRequest *json.RawMessage `json:"pull_request"`
		Labels      []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	endpoint := base + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/issues/" + fmt.Sprint(task.OriginNumber)
	if err := r.getJSON(ctx, endpoint, token, domain.ProviderGitHub, &issue); err != nil {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("read GitHub agent Issue: %w", err)
	}
	if issue.State != "open" || issue.PullRequest != nil {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("GitHub agent Issue is closed or is a pull request")
	}
	snapshot := domain.AgentTaskIssueSnapshot{Title: issue.Title, Body: issue.Body}
	for _, label := range issue.Labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			snapshot.Labels = append(snapshot.Labels, name)
		}
	}
	snapshot.Revision = issueRevision(task, snapshot)
	if snapshot.Revision != task.OriginRevision {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("GitHub agent Issue changed since admission")
	}
	return snapshot, nil
}

func (r Resolver) gitLabIssue(ctx context.Context, task domain.AgentTask, token string) (domain.AgentTaskIssueSnapshot, error) {
	if task.OriginKind != "issue" {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("GitLab agent Issue target is invalid")
	}
	base, err := trustedAPIBase(task.APIBaseURL, r.AllowGitLabHTTP)
	if err != nil {
		return domain.AgentTaskIssueSnapshot{}, err
	}
	var issue struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		State       string   `json:"state"`
		Labels      []string `json:"labels"`
	}
	endpoint := base + "/projects/" + url.PathEscape(strings.Trim(task.Repository, "/")) + "/issues/" + fmt.Sprint(task.OriginNumber)
	if err := r.getJSON(ctx, endpoint, token, domain.ProviderGitLab, &issue); err != nil {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("read GitLab agent Issue: %w", err)
	}
	if issue.State != "opened" {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("GitLab agent Issue is not open")
	}
	snapshot := domain.AgentTaskIssueSnapshot{Title: issue.Title, Body: issue.Description, Labels: issue.Labels}
	snapshot.Revision = issueRevision(task, snapshot)
	if snapshot.Revision != task.OriginRevision {
		return domain.AgentTaskIssueSnapshot{}, fmt.Errorf("GitLab agent Issue changed since admission")
	}
	return snapshot, nil
}

func issueRevision(task domain.AgentTask, issue domain.AgentTaskIssueSnapshot) string {
	if task.RequestedBy == "policy:auto" {
		return domain.AgentAutomaticIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, issue.Title, issue.Body, issue.Labels)
	}
	return domain.AgentIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, issue.Title, issue.Body)
}

func verifiedFeedbackInstruction(body string, binding *domain.AgentTaskFeedbackBinding) (string, error) {
	if binding == nil || strings.TrimSpace(binding.CommentExternalID) == "" || strings.TrimSpace(binding.ActorExternalID) == "" || len(binding.InstructionSHA256) != 64 {
		return "", fmt.Errorf("frozen Draft feedback binding is missing")
	}
	command, mentioned, err := interaction.Parse(body)
	if err != nil || !mentioned || command.Kind != interaction.Revise {
		return "", fmt.Errorf("Draft feedback comment no longer contains the admitted revise command")
	}
	instruction := strings.TrimSpace(command.Instruction)
	digest := sha256.Sum256([]byte(instruction))
	if hex.EncodeToString(digest[:]) != binding.InstructionSHA256 {
		return "", fmt.Errorf("Draft feedback comment changed after admission")
	}
	return instruction, nil
}

func (r Resolver) githubFeedbackComment(ctx context.Context, task domain.AgentTask, binding *domain.AgentTaskFeedbackBinding, token string) (*domain.AgentTaskFeedbackSnapshot, error) {
	if binding == nil {
		return nil, fmt.Errorf("GitHub Draft feedback binding is missing")
	}
	commentID, err := strconv.ParseUint(binding.CommentExternalID, 10, 64)
	if err != nil || commentID == 0 {
		return nil, fmt.Errorf("GitHub Draft feedback comment identity is invalid")
	}
	parts := strings.Split(strings.Trim(task.Repository, "/"), "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("GitHub Draft feedback repository is invalid")
	}
	base, err := trustedAPIBase(task.APIBaseURL, false)
	if err != nil {
		return nil, err
	}
	var comment struct {
		ID       json.Number `json:"id"`
		Body     string      `json:"body"`
		IssueURL string      `json:"issue_url"`
		User     struct {
			ID json.Number `json:"id"`
		} `json:"user"`
	}
	endpoint := base + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/issues/comments/" + strconv.FormatUint(commentID, 10)
	if err := r.getJSON(ctx, endpoint, token, domain.ProviderGitHub, &comment); err != nil {
		return nil, fmt.Errorf("read GitHub Draft feedback comment: %w", err)
	}
	expectedIssueURL := base + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/issues/" + strconv.Itoa(task.OriginNumber)
	if comment.ID.String() != binding.CommentExternalID || comment.User.ID.String() != binding.ActorExternalID || comment.IssueURL != expectedIssueURL {
		return nil, fmt.Errorf("GitHub Draft feedback comment identity or pull request changed")
	}
	instruction, err := verifiedFeedbackInstruction(comment.Body, binding)
	if err != nil {
		return nil, err
	}
	return &domain.AgentTaskFeedbackSnapshot{CommentExternalID: binding.CommentExternalID, ActorExternalID: binding.ActorExternalID, Instruction: instruction}, nil
}

func (r Resolver) gitLabFeedbackComment(ctx context.Context, task domain.AgentTask, binding *domain.AgentTaskFeedbackBinding, token string) (*domain.AgentTaskFeedbackSnapshot, error) {
	if binding == nil {
		return nil, fmt.Errorf("GitLab Draft feedback binding is missing")
	}
	commentID, err := strconv.ParseUint(binding.CommentExternalID, 10, 64)
	if err != nil || commentID == 0 {
		return nil, fmt.Errorf("GitLab Draft feedback comment identity is invalid")
	}
	base, err := trustedAPIBase(task.APIBaseURL, r.AllowGitLabHTTP)
	if err != nil {
		return nil, err
	}
	var note struct {
		ID           json.Number `json:"id"`
		Body         string      `json:"body"`
		NoteableType string      `json:"noteable_type"`
		NoteableIID  int         `json:"noteable_iid"`
		System       bool        `json:"system"`
		Author       struct {
			ID json.Number `json:"id"`
		} `json:"author"`
	}
	endpoint := base + "/projects/" + url.PathEscape(strings.Trim(task.Repository, "/")) + "/merge_requests/" + strconv.Itoa(task.OriginNumber) + "/notes/" + strconv.FormatUint(commentID, 10)
	if err := r.getJSON(ctx, endpoint, token, domain.ProviderGitLab, &note); err != nil {
		return nil, fmt.Errorf("read GitLab Draft feedback note: %w", err)
	}
	if note.System || note.NoteableType != "MergeRequest" || note.NoteableIID != task.OriginNumber || note.ID.String() != binding.CommentExternalID || note.Author.ID.String() != binding.ActorExternalID {
		return nil, fmt.Errorf("GitLab Draft feedback note identity or merge request changed")
	}
	instruction, err := verifiedFeedbackInstruction(note.Body, binding)
	if err != nil {
		return nil, err
	}
	return &domain.AgentTaskFeedbackSnapshot{CommentExternalID: binding.CommentExternalID, ActorExternalID: binding.ActorExternalID, Instruction: instruction}, nil
}

func (r Resolver) githubPullRequest(ctx context.Context, task domain.AgentTask, token string) (domain.AgentTaskSourceSnapshot, error) {
	parts := strings.Split(strings.Trim(task.Repository, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || !strings.HasPrefix(task.ExecutionBranch, "agent/") {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitHub feedback source is invalid")
	}
	base, err := trustedAPIBase(task.APIBaseURL, false)
	if err != nil {
		return domain.AgentTaskSourceSnapshot{}, err
	}
	var pull struct {
		Draft bool `json:"draft"`
		Base  struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := r.getJSON(ctx, base+"/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1])+"/pulls/"+fmt.Sprint(task.OriginNumber), token, domain.ProviderGitHub, &pull); err != nil {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("read GitHub draft pull request: %w", err)
	}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: strings.TrimSpace(pull.Head.Ref), BaseSHA: strings.TrimSpace(pull.Head.SHA), TargetBranch: strings.TrimSpace(pull.Base.Ref)}
	if !pull.Draft || snapshot.BaseRef != task.ExecutionBranch || !strings.EqualFold(snapshot.BaseSHA, task.OriginRevision) || !snapshot.Valid() {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitHub draft pull request head no longer matches the admitted feedback revision")
	}
	return snapshot, nil
}

func (r Resolver) github(ctx context.Context, task domain.AgentTask, token string) (domain.AgentTaskSourceSnapshot, error) {
	parts := strings.Split(strings.Trim(task.Repository, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitHub repository is invalid")
	}
	base, err := trustedAPIBase(task.APIBaseURL, false)
	if err != nil {
		return domain.AgentTaskSourceSnapshot{}, err
	}
	var repository struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := r.getJSON(ctx, base+"/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), token, domain.ProviderGitHub, &repository); err != nil {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("read GitHub default branch: %w", err)
	}
	ref := strings.TrimSpace(repository.DefaultBranch)
	if ref == "" {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitHub default branch is missing")
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := r.getJSON(ctx, base+"/repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1])+"/commits/"+url.PathEscape(ref), token, domain.ProviderGitHub, &commit); err != nil {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("read GitHub default branch commit: %w", err)
	}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: ref, BaseSHA: strings.TrimSpace(commit.SHA)}
	if !snapshot.Valid() {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitHub default branch commit is invalid")
	}
	return snapshot, nil
}

func (r Resolver) gitLab(ctx context.Context, task domain.AgentTask, token string) (domain.AgentTaskSourceSnapshot, error) {
	base, err := trustedAPIBase(task.APIBaseURL, r.AllowGitLabHTTP)
	if err != nil {
		return domain.AgentTaskSourceSnapshot{}, err
	}
	project := url.PathEscape(strings.Trim(task.Repository, "/"))
	var metadata struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := r.getJSON(ctx, base+"/projects/"+project, token, domain.ProviderGitLab, &metadata); err != nil {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("read GitLab default branch: %w", err)
	}
	ref := strings.TrimSpace(metadata.DefaultBranch)
	if ref == "" {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitLab default branch is missing")
	}
	var branch struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := r.getJSON(ctx, base+"/projects/"+project+"/repository/branches/"+url.PathEscape(ref), token, domain.ProviderGitLab, &branch); err != nil {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("read GitLab default branch commit: %w", err)
	}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: ref, BaseSHA: strings.TrimSpace(branch.Commit.ID)}
	if !snapshot.Valid() {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitLab default branch commit is invalid")
	}
	return snapshot, nil
}

func (r Resolver) gitLabMergeRequest(ctx context.Context, task domain.AgentTask, token string) (domain.AgentTaskSourceSnapshot, error) {
	if !strings.HasPrefix(task.ExecutionBranch, "agent/") {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitLab feedback source is invalid")
	}
	base, err := trustedAPIBase(task.APIBaseURL, r.AllowGitLabHTTP)
	if err != nil {
		return domain.AgentTaskSourceSnapshot{}, err
	}
	var mergeRequest struct {
		Draft        bool   `json:"draft"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		SHA          string `json:"sha"`
	}
	if err := r.getJSON(ctx, base+"/projects/"+url.PathEscape(strings.Trim(task.Repository, "/"))+"/merge_requests/"+fmt.Sprint(task.OriginNumber), token, domain.ProviderGitLab, &mergeRequest); err != nil {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("read GitLab draft merge request: %w", err)
	}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: strings.TrimSpace(mergeRequest.SourceBranch), BaseSHA: strings.TrimSpace(mergeRequest.SHA), TargetBranch: strings.TrimSpace(mergeRequest.TargetBranch)}
	if !mergeRequest.Draft || snapshot.BaseRef != task.ExecutionBranch || !strings.EqualFold(snapshot.BaseSHA, task.OriginRevision) || !snapshot.Valid() {
		return domain.AgentTaskSourceSnapshot{}, fmt.Errorf("GitLab draft merge request head no longer matches the admitted feedback revision")
	}
	return snapshot, nil
}

func (r Resolver) getJSON(ctx context.Context, endpoint, token string, provider domain.Provider, output any) error {
	client := httpguard.NoRedirects(r.HTTPClient, 20*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create provider source request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if provider == domain.ProviderGitHub {
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := client.Do(req)
	if err != nil {
		wrapped := fmt.Errorf("request provider source metadata: %w", err)
		var networkError net.Error
		if ctx.Err() == nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout())) {
			return &transientSourceError{cause: wrapped}
		}
		return wrapped
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		wrapped := fmt.Errorf("provider source metadata returned HTTP %d", resp.StatusCode)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return &transientSourceError{cause: wrapped, retryAfter: sourceRetryAfter(resp.Header.Get("Retry-After"))}
		}
		return wrapped
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceMetadataBytes+1))
	if err != nil {
		wrapped := fmt.Errorf("read provider source metadata: %w", err)
		var networkError net.Error
		if ctx.Err() == nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &networkError) && networkError.Timeout())) {
			return &transientSourceError{cause: wrapped}
		}
		return wrapped
	}
	if len(body) > maxSourceMetadataBytes {
		return fmt.Errorf("provider source metadata exceeds size limit")
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode provider source metadata: %w", err)
	}
	return nil
}

func trustedAPIBase(value string, allowHTTP bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("provider API base URL is invalid")
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return "", fmt.Errorf("provider API base URL scheme is invalid")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func internalReviewFeedback(binding *domain.AgentTaskFeedbackBinding) *domain.AgentTaskFeedbackSnapshot {
	return &domain.AgentTaskFeedbackSnapshot{CommentExternalID: binding.CommentExternalID, ActorExternalID: binding.ActorExternalID, Instruction: binding.SystemInstruction}
}
