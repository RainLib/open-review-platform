// Package agentworkflow observes delivered Agent changes and requests ordinary
// revision-pinned reviews. It never owns coding or provider-write credentials.
package agentworkflow

import (
	"context"
	"encoding/hex"
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
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/RainLib/open-review-platform/internal/providertransport"
	"github.com/RainLib/open-review-platform/internal/store"
)

type Store interface {
	ClaimAgentWorkflow(context.Context, string) (*store.AgentWorkflowTarget, error)
	EnsureAgentRereview(context.Context, store.AgentWorkflowTarget, domain.InboundEvent) error
	FinishAgentWorkflowObservation(context.Context, string, store.AgentWorkflowTarget, string, string) error
}
type Processor struct {
	Store                           Store
	Resolver                        credentials.Resolver
	HTTPClient                      *http.Client
	WorkerID                        string
	AllowHTTP, AllowPrivateNetworks bool
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	target, err := p.Store.ClaimAgentWorkflow(ctx, p.WorkerID)
	if errors.Is(err, store.ErrNoQueuedAgentTask) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	event, state, err := p.readDraft(ctx, *target)
	if err != nil {
		return true, p.Store.FinishAgentWorkflowObservation(ctx, p.WorkerID, *target, "", "unavailable")
	}
	if event.HeadSHA == target.Acceptance.HeadSHA && state == "open" {
		if err = p.Store.EnsureAgentRereview(ctx, *target, event); err != nil {
			_ = p.Store.FinishAgentWorkflowObservation(ctx, p.WorkerID, *target, event.HeadSHA, "review_unavailable")
			return true, err
		}
	}
	return true, p.Store.FinishAgentWorkflowObservation(ctx, p.WorkerID, *target, event.HeadSHA, state)
}
func (p Processor) readDraft(ctx context.Context, target store.AgentWorkflowTarget) (domain.InboundEvent, string, error) {
	job := target.Job
	event := domain.InboundEvent{Provider: job.Provider, APIBaseURL: job.APIBaseURL, InstallationExternalID: job.InstallationExternalID, Repository: job.Repository, ReviewNumber: job.ReviewNumber, DeliveryID: "agent-rereview:" + target.Attempt.ID.String() + ":" + target.Acceptance.HeadSHA, EventName: "agent_delivery", Payload: json.RawMessage(`{}`), ReceivedAt: time.Now().UTC(), TriggerKind: "manual", ActorKind: "system", ActorSubject: target.Task.RequestedBy}
	base, err := url.Parse(strings.TrimSuffix(job.APIBaseURL, "/"))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && !(p.AllowHTTP && base.Scheme == "http")) || job.CredentialRef == "" || p.Resolver == nil {
		return event, "", fmt.Errorf("provider origin or credential unavailable")
	}
	token, err := p.Resolver.Resolve(ctx, job)
	if err != nil || token == "" {
		return event, "", fmt.Errorf("provider read unavailable")
	}
	endpoint := ""
	parts := strings.Split(job.Repository, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return event, "", fmt.Errorf("invalid repository")
		}
	}
	if job.Provider == domain.ProviderGitHub && len(parts) == 2 && base.Scheme == "https" && (base.Path == "" || base.Path == "/api/v3") {
		endpoint = base.String() + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/pulls/" + strconv.Itoa(job.ReviewNumber)
	} else if job.Provider == domain.ProviderGitLab && strings.HasSuffix(base.Path, "/api/v4") {
		endpoint = base.String() + "/projects/" + url.PathEscape(job.Repository) + "/merge_requests/" + strconv.Itoa(job.ReviewNumber)
	} else {
		return event, "", fmt.Errorf("invalid provider base")
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if job.Provider == domain.ProviderGitHub {
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/vnd.github+json")
	} else {
		request.Header.Set("PRIVATE-TOKEN", token)
	}
	client := p.HTTPClient
	if client == nil {
		client = providertransport.NewClient(p.AllowPrivateNetworks)
	}
	response, err := httpguard.NoRedirects(client, 20*time.Second).Do(request)
	if err != nil {
		return event, "", fmt.Errorf("provider unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return event, "", fmt.Errorf("provider read failed")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return event, "", fmt.Errorf("provider response invalid")
	}
	state := ""
	if job.Provider == domain.ProviderGitHub {
		var result struct {
			Number int    `json:"number"`
			State  string `json:"state"`
			Draft  bool   `json:"draft"`
			Merged bool   `json:"merged"`
			Head   struct {
				SHA, Ref string
				Repo     struct {
					FullName string `json:"full_name"`
				}
			}
			Base struct {
				SHA, Ref string
				Repo     struct {
					FullName string `json:"full_name"`
				}
			}
		}
		if json.Unmarshal(raw, &result) != nil || result.Number != job.ReviewNumber || result.Head.Repo.FullName != job.Repository || result.Base.Repo.FullName != job.Repository || result.Head.Ref != target.Task.ExecutionBranch || result.Base.Ref != job.BaseRef {
			return event, "", fmt.Errorf("Draft identity changed")
		}
		event.HeadSHA, event.HeadRef, event.BaseSHA, event.BaseRef, event.IsDraft = result.Head.SHA, result.Head.Ref, result.Base.SHA, result.Base.Ref, result.Draft
		state = result.State
		if result.Merged {
			state = "merged"
		}
		cloneBase := base.Scheme + "://" + base.Host
		if base.Host == "api.github.com" {
			cloneBase = "https://github.com"
		}
		event.CloneURL = cloneBase + "/" + job.Repository + ".git"
	} else {
		var result struct {
			IID             int    `json:"iid"`
			State           string `json:"state"`
			SHA             string `json:"sha"`
			SourceBranch    string `json:"source_branch"`
			TargetBranch    string `json:"target_branch"`
			SourceProjectID int64  `json:"source_project_id"`
			TargetProjectID int64  `json:"target_project_id"`
			Draft           bool   `json:"draft"`
			DiffRefs        struct {
				BaseSHA string `json:"base_sha"`
				HeadSHA string `json:"head_sha"`
			} `json:"diff_refs"`
		}
		if json.Unmarshal(raw, &result) != nil || result.IID != job.ReviewNumber || result.SourceBranch != target.Task.ExecutionBranch || result.SourceProjectID == 0 || result.SourceProjectID != result.TargetProjectID || result.TargetBranch != job.BaseRef {
			return event, "", fmt.Errorf("Draft identity changed")
		}
		event.HeadSHA, event.HeadRef, event.BaseSHA, event.BaseRef, event.IsDraft = result.SHA, result.SourceBranch, result.DiffRefs.BaseSHA, result.TargetBranch, result.Draft
		state = result.State
		if state == "opened" {
			state = "open"
		}
		event.CloneURL = strings.TrimSuffix(base.String(), "/api/v4") + "/" + job.Repository + ".git"
	}
	if state != "open" && state != "merged" && state != "closed" {
		return event, "", fmt.Errorf("provider state invalid")
	}
	if !validProviderCommit(event.BaseSHA) || !validProviderCommit(event.HeadSHA) || !(domain.AgentTaskSourceSnapshot{BaseRef: event.BaseRef, BaseSHA: event.BaseSHA}).Valid() || !(domain.AgentTaskSourceSnapshot{BaseRef: event.HeadRef, BaseSHA: event.HeadSHA}).Valid() {
		return event, "", fmt.Errorf("provider returned invalid immutable source")
	}
	return event, state, nil
}

func validProviderCommit(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(sha)
	return err == nil && hex.EncodeToString(decoded) == sha
}
