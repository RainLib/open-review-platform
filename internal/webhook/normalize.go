package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func NormalizeGitHub(deliveryID, eventName string, body []byte, now time.Time) (domain.InboundEvent, bool, error) {
	if eventName != "pull_request" {
		return domain.InboundEvent{}, false, nil
	}
	var payload struct {
		Action       string `json:"action"`
		Number       int    `json:"number"`
		Installation struct {
			ID json.Number `json:"id"`
		} `json:"installation"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
		PullRequest struct {
			Title string `json:"title"`
			Draft bool   `json:"draft"`
			User  struct {
				Login string      `json:"login"`
				ID    json.Number `json:"id"`
			} `json:"user"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
			Base struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"base"`
			Head struct {
				Ref  string `json:"ref"`
				SHA  string `json:"sha"`
				Repo struct {
					CloneURL string `json:"clone_url"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.InboundEvent{}, false, fmt.Errorf("decode GitHub payload: %w", err)
	}
	if payload.Action != "opened" && payload.Action != "reopened" && payload.Action != "synchronize" && payload.Action != "ready_for_review" {
		return domain.InboundEvent{}, false, nil
	}
	if deliveryID == "" || payload.Installation.ID == "" || payload.Repository.FullName == "" || payload.Repository.CloneURL == "" || payload.Number <= 0 || payload.PullRequest.Base.Ref == "" || payload.PullRequest.Head.SHA == "" {
		return domain.InboundEvent{}, false, fmt.Errorf("GitHub pull_request payload is missing required review fields")
	}
	cloneURL := payload.Repository.CloneURL
	if payload.PullRequest.Head.Repo.CloneURL != "" {
		cloneURL = payload.PullRequest.Head.Repo.CloneURL
	}
	labels := make([]string, 0, len(payload.PullRequest.Labels))
	for _, label := range payload.PullRequest.Labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			labels = append(labels, name)
		}
	}
	return domain.InboundEvent{
		Provider:               domain.ProviderGitHub,
		APIBaseURL:             githubAPIBaseURL(cloneURL),
		DeliveryID:             deliveryID,
		EventName:              eventName,
		InstallationExternalID: payload.Installation.ID.String(),
		Repository:             payload.Repository.FullName,
		CloneURL:               cloneURL,
		ReviewNumber:           payload.Number,
		BaseRef:                payload.PullRequest.Base.Ref,
		BaseSHA:                payload.PullRequest.Base.SHA,
		HeadRef:                payload.PullRequest.Head.Ref,
		HeadSHA:                payload.PullRequest.Head.SHA,
		Payload:                append(json.RawMessage(nil), body...),
		ReceivedAt:             now.UTC(),
		Action:                 payload.Action,
		IsDraft:                payload.PullRequest.Draft,
		Title:                  strings.TrimSpace(payload.PullRequest.Title),
		Author:                 strings.TrimSpace(payload.PullRequest.User.Login),
		AuthorExternalID:       providerActorID(payload.PullRequest.User.ID),
		Labels:                 labels,
	}, true, nil
}

// NormalizeGitHubIssueComment accepts only comments attached to pull requests.
// An issue with the same number is not a review target and is intentionally
// ignored rather than treated as a command.
func NormalizeGitHubIssueComment(deliveryID string, body []byte) (domain.CommentEvent, bool, error) {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID json.Number `json:"id"`
		} `json:"installation"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
		Issue struct {
			Number      int       `json:"number"`
			PullRequest *struct{} `json:"pull_request"`
		} `json:"issue"`
		Comment struct {
			ID   json.Number `json:"id"`
			Body string      `json:"body"`
			User struct {
				ID json.Number `json:"id"`
			} `json:"user"`
		} `json:"comment"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.CommentEvent{}, false, fmt.Errorf("decode GitHub issue_comment payload: %w", err)
	}
	if payload.Action != "created" || payload.Issue.PullRequest == nil {
		return domain.CommentEvent{}, false, nil
	}
	if deliveryID == "" || payload.Installation.ID == "" || payload.Repository.FullName == "" || payload.Repository.CloneURL == "" || payload.Issue.Number <= 0 || payload.Comment.ID == "" || payload.Comment.User.ID == "" {
		return domain.CommentEvent{}, false, fmt.Errorf("GitHub issue_comment payload is missing required review fields")
	}
	return domain.CommentEvent{Provider: domain.ProviderGitHub, APIBaseURL: githubAPIBaseURL(payload.Repository.CloneURL), DeliveryID: deliveryID, InstallationExternalID: payload.Installation.ID.String(), Repository: payload.Repository.FullName, CloneURL: payload.Repository.CloneURL, ReviewNumber: payload.Issue.Number, CommentExternalID: payload.Comment.ID.String(), ActorExternalID: payload.Comment.User.ID.String(), Body: payload.Comment.Body}, true, nil
}

// NormalizeGitHubAgentTaskIssueComment accepts only ordinary Issue comments.
// Pull-request comments stay on the review command path so an implement
// request cannot silently fabricate an Issue-origin Agent task.
func NormalizeGitHubAgentTaskIssueComment(deliveryID string, body []byte) (domain.AgentTaskCommandEvent, bool, error) {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID json.Number `json:"id"`
		} `json:"installation"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
		Issue struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
			PullRequest *struct{} `json:"pull_request"`
		} `json:"issue"`
		Comment struct {
			ID   json.Number `json:"id"`
			Body string      `json:"body"`
			User struct {
				ID json.Number `json:"id"`
			} `json:"user"`
		} `json:"comment"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.AgentTaskCommandEvent{}, false, fmt.Errorf("decode GitHub agent Issue comment payload: %w", err)
	}
	if payload.Action != "created" || payload.Issue.PullRequest != nil {
		return domain.AgentTaskCommandEvent{}, false, nil
	}
	if deliveryID == "" || payload.Installation.ID == "" || payload.Repository.FullName == "" || payload.Repository.CloneURL == "" || payload.Issue.Number <= 0 || payload.Comment.ID == "" || payload.Comment.User.ID == "" {
		return domain.AgentTaskCommandEvent{}, false, fmt.Errorf("GitHub agent Issue comment payload is missing required fields")
	}
	revision := domain.AgentIssueRevision(domain.ProviderGitHub, githubAPIBaseURL(payload.Repository.CloneURL), payload.Repository.FullName, payload.Issue.Number, payload.Issue.Title, payload.Issue.Body)
	labels := make([]string, 0, len(payload.Issue.Labels))
	for _, label := range payload.Issue.Labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			labels = append(labels, name)
		}
	}
	return domain.AgentTaskCommandEvent{Provider: domain.ProviderGitHub, APIBaseURL: githubAPIBaseURL(payload.Repository.CloneURL), DeliveryID: deliveryID, InstallationExternalID: payload.Installation.ID.String(), Repository: payload.Repository.FullName, IssueNumber: payload.Issue.Number, IssueRevision: revision, CommentExternalID: payload.Comment.ID.String(), ActorExternalID: payload.Comment.User.ID.String(), Body: payload.Comment.Body, IssueTitle: payload.Issue.Title, IssueBody: payload.Issue.Body, IssueLabels: labels}, true, nil
}

// NormalizeGitHubAgentTaskPullRequestComment extracts the narrow input for a
// feedback cycle. It deliberately carries no head SHA: the control plane must
// bind it to a completed Draft PR attempt and re-read the provider head before
// any new plan can be created.
func NormalizeGitHubAgentTaskPullRequestComment(deliveryID string, body []byte) (domain.AgentTaskFeedbackEvent, bool, error) {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID json.Number `json:"id"`
		} `json:"installation"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
		Issue struct {
			Number      int       `json:"number"`
			PullRequest *struct{} `json:"pull_request"`
		} `json:"issue"`
		Comment struct {
			ID   json.Number `json:"id"`
			Body string      `json:"body"`
			User struct {
				ID json.Number `json:"id"`
			} `json:"user"`
		} `json:"comment"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.AgentTaskFeedbackEvent{}, false, fmt.Errorf("decode GitHub agent feedback payload: %w", err)
	}
	if payload.Action != "created" || payload.Issue.PullRequest == nil {
		return domain.AgentTaskFeedbackEvent{}, false, nil
	}
	if deliveryID == "" || payload.Installation.ID == "" || payload.Repository.FullName == "" || payload.Repository.CloneURL == "" || payload.Issue.Number <= 0 || payload.Comment.ID == "" || payload.Comment.User.ID == "" {
		return domain.AgentTaskFeedbackEvent{}, false, fmt.Errorf("GitHub agent feedback payload is missing required fields")
	}
	return domain.AgentTaskFeedbackEvent{Provider: domain.ProviderGitHub, APIBaseURL: githubAPIBaseURL(payload.Repository.CloneURL), DeliveryID: deliveryID, InstallationExternalID: payload.Installation.ID.String(), Repository: payload.Repository.FullName, PullRequestNumber: payload.Issue.Number, CommentExternalID: payload.Comment.ID.String(), ActorExternalID: payload.Comment.User.ID.String(), Instruction: payload.Comment.Body}, true, nil
}

// NormalizeGitHubIssue accepts user-authored Issue lifecycle events only. Pull
// requests arrive on a different GitHub event, while bot-authored Issues and
// Open Review markers are rejected here to prevent analysis loops.
func NormalizeGitHubIssue(deliveryID string, body []byte, now time.Time) (domain.ProviderIssueEvent, bool, error) {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID json.Number `json:"id"`
		} `json:"installation"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
		Issue struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			User   struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"user"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
			PullRequest *struct{} `json:"pull_request"`
		} `json:"issue"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.ProviderIssueEvent{}, false, fmt.Errorf("decode GitHub issues payload: %w", err)
	}
	if payload.Action != "opened" && payload.Action != "edited" && payload.Action != "reopened" {
		return domain.ProviderIssueEvent{}, false, nil
	}
	if payload.Issue.PullRequest != nil || strings.EqualFold(payload.Issue.User.Type, "Bot") || strings.Contains(payload.Issue.Body, "open-review-platform:external-issue:") {
		return domain.ProviderIssueEvent{}, false, nil
	}
	if deliveryID == "" || payload.Installation.ID == "" || payload.Repository.FullName == "" || payload.Repository.CloneURL == "" || payload.Issue.Number <= 0 || strings.TrimSpace(payload.Issue.Title) == "" {
		return domain.ProviderIssueEvent{}, false, fmt.Errorf("GitHub issues payload is missing required fields")
	}
	labels := make([]string, 0, len(payload.Issue.Labels))
	for _, label := range payload.Issue.Labels {
		if value := strings.TrimSpace(label.Name); value != "" {
			labels = append(labels, value)
		}
	}
	return domain.ProviderIssueEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: githubAPIBaseURL(payload.Repository.CloneURL), DeliveryID: deliveryID,
		EventName: "issues", InstallationExternalID: payload.Installation.ID.String(), Repository: payload.Repository.FullName,
		IssueNumber: payload.Issue.Number, Action: payload.Action, Title: boundedRunes(payload.Issue.Title, 500),
		Body: boundedRunes(payload.Issue.Body, 30000), Author: boundedRunes(payload.Issue.User.Login, 255), AuthorType: payload.Issue.User.Type,
		Labels: labels, Payload: append(json.RawMessage(nil), body...), ReceivedAt: now.UTC(),
	}, true, nil
}

func NormalizeGitHubReaction(deliveryID string, body []byte) (domain.FindingReaction, bool, error) {
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Comment struct {
			Body string `json:"body"`
		} `json:"comment"`
		Reaction struct {
			ID      json.Number `json:"id"`
			Content string      `json:"content"`
			User    struct {
				ID json.Number `json:"id"`
			} `json:"user"`
		} `json:"reaction"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return domain.FindingReaction{}, false, fmt.Errorf("decode GitHub reaction payload: %w", err)
	}
	if payload.Action != "created" && payload.Action != "deleted" {
		return domain.FindingReaction{}, false, nil
	}
	kind := ""
	switch payload.Reaction.Content {
	case "+1":
		kind = "useful"
	case "-1":
		kind = "false_positive"
	default:
		return domain.FindingReaction{}, false, nil
	}
	marker := providerCommentMarker(payload.Comment.Body, "open-review-platform:finding:")
	if deliveryID == "" || payload.Repository.FullName == "" || payload.Reaction.ID.String() == "" || payload.Reaction.User.ID.String() == "" || marker == "" {
		return domain.FindingReaction{}, false, nil
	}
	return domain.FindingReaction{Provider: domain.ProviderGitHub, DeliveryID: deliveryID, ReactionExternalID: payload.Reaction.ID.String(), ActorExternalID: payload.Reaction.User.ID.String(), Repository: payload.Repository.FullName, FindingMarker: marker, Kind: kind, Action: payload.Action}, true, nil
}

// NormalizeGitHubProviderIssueReaction turns thumbs feedback on the stable
// Issue-analysis comment into a bounded event. It deliberately ignores the
// eyes acknowledgement on the Issue itself and every unrelated reaction.
func NormalizeGitHubProviderIssueReaction(deliveryID string, body []byte) (domain.ProviderIssueReaction, bool, error) {
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Comment struct {
			Body string `json:"body"`
		} `json:"comment"`
		Reaction struct {
			ID      json.Number `json:"id"`
			Content string      `json:"content"`
			User    struct {
				ID json.Number `json:"id"`
			} `json:"user"`
		} `json:"reaction"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.ProviderIssueReaction{}, false, fmt.Errorf("decode GitHub provider Issue reaction payload: %w", err)
	}
	if payload.Action != "created" && payload.Action != "deleted" {
		return domain.ProviderIssueReaction{}, false, nil
	}
	kind := ""
	switch payload.Reaction.Content {
	case "+1":
		kind = "useful"
	case "-1":
		kind = "not_useful"
	default:
		return domain.ProviderIssueReaction{}, false, nil
	}
	marker := providerCommentMarker(payload.Comment.Body, "open-review-platform:issue-triage:")
	if deliveryID == "" || payload.Repository.FullName == "" || payload.Reaction.ID.String() == "" || payload.Reaction.User.ID.String() == "" || marker == "" {
		return domain.ProviderIssueReaction{}, false, nil
	}
	return domain.ProviderIssueReaction{
		Provider: domain.ProviderGitHub, DeliveryID: deliveryID,
		ReactionExternalID: payload.Reaction.ID.String(), ActorExternalID: payload.Reaction.User.ID.String(),
		Repository: payload.Repository.FullName, AnalysisMarker: marker, Kind: kind, Action: payload.Action,
	}, true, nil
}

// NormalizeGitLabNoteComment accepts notes authored on a merge request. It
// deliberately excludes issue, commit, and snippet notes so @openreview cannot
// start work outside a registered code-review target.
func NormalizeGitLabNoteComment(deliveryID string, body []byte) (domain.CommentEvent, bool, error) {
	var payload struct {
		Project struct {
			ID                json.Number `json:"id"`
			PathWithNamespace string      `json:"path_with_namespace"`
			GitHTTPURL        string      `json:"git_http_url"`
			HTTPURL           string      `json:"http_url"`
		} `json:"project"`
		MergeRequest struct {
			IID int `json:"iid"`
		} `json:"merge_request"`
		ObjectAttributes struct {
			Action       string      `json:"action"`
			ID           json.Number `json:"id"`
			Note         string      `json:"note"`
			NoteableType string      `json:"noteable_type"`
		} `json:"object_attributes"`
		User struct {
			ID json.Number `json:"id"`
		} `json:"user"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.CommentEvent{}, false, fmt.Errorf("decode GitLab note payload: %w", err)
	}
	if payload.ObjectAttributes.Action != "create" || payload.ObjectAttributes.NoteableType != "MergeRequest" {
		return domain.CommentEvent{}, false, nil
	}
	if deliveryID == "" {
		hash := sha256.Sum256(body)
		deliveryID = "body-sha256:" + hex.EncodeToString(hash[:])
	}
	cloneURL := gitLabProjectCloneURL(payload.Project.GitHTTPURL, payload.Project.HTTPURL)
	providerAPIBase := gitLabAPIBaseURL(cloneURL, payload.Project.PathWithNamespace)
	if payload.Project.ID == "" || payload.Project.PathWithNamespace == "" || providerAPIBase == "" || payload.MergeRequest.IID <= 0 || payload.ObjectAttributes.ID == "" || payload.User.ID == "" {
		return domain.CommentEvent{}, false, fmt.Errorf("GitLab note payload is missing required review fields")
	}
	return domain.CommentEvent{
		Provider: domain.ProviderGitLab, APIBaseURL: providerAPIBase, DeliveryID: deliveryID,
		InstallationExternalID: payload.Project.ID.String(), Repository: payload.Project.PathWithNamespace, CloneURL: cloneURL, ReviewNumber: payload.MergeRequest.IID,
		CommentExternalID: payload.ObjectAttributes.ID.String(), ActorExternalID: payload.User.ID.String(), Body: payload.ObjectAttributes.Note,
	}, true, nil
}

// NormalizeGitLabAgentTaskIssueNote is the self-managed GitLab-safe
// counterpart. It uses only Issue notes; merge-request notes remain review
// commands and project identity remains the installation boundary.
func NormalizeGitLabAgentTaskIssueNote(deliveryID string, body []byte) (domain.AgentTaskCommandEvent, bool, error) {
	var payload struct {
		Project struct {
			ID                json.Number `json:"id"`
			PathWithNamespace string      `json:"path_with_namespace"`
			GitHTTPURL        string      `json:"git_http_url"`
			HTTPURL           string      `json:"http_url"`
		} `json:"project"`
		Issue struct {
			IID         int    `json:"iid"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Labels      []struct {
				Title string `json:"title"`
			} `json:"labels"`
		} `json:"issue"`
		ObjectAttributes struct {
			Action       string      `json:"action"`
			ID           json.Number `json:"id"`
			Note         string      `json:"note"`
			NoteableType string      `json:"noteable_type"`
		} `json:"object_attributes"`
		User struct {
			ID json.Number `json:"id"`
		} `json:"user"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.AgentTaskCommandEvent{}, false, fmt.Errorf("decode GitLab agent Issue note payload: %w", err)
	}
	if payload.ObjectAttributes.Action != "create" || payload.ObjectAttributes.NoteableType != "Issue" {
		return domain.AgentTaskCommandEvent{}, false, nil
	}
	if deliveryID == "" {
		digest := sha256.Sum256(body)
		deliveryID = "body-sha256:" + hex.EncodeToString(digest[:])
	}
	cloneURL := gitLabProjectCloneURL(payload.Project.GitHTTPURL, payload.Project.HTTPURL)
	providerAPIBase := gitLabAPIBaseURL(cloneURL, payload.Project.PathWithNamespace)
	if payload.Project.ID == "" || payload.Project.PathWithNamespace == "" || providerAPIBase == "" || payload.Issue.IID <= 0 || payload.ObjectAttributes.ID == "" || payload.User.ID == "" {
		return domain.AgentTaskCommandEvent{}, false, fmt.Errorf("GitLab agent Issue note payload is missing required fields")
	}
	revision := domain.AgentIssueRevision(domain.ProviderGitLab, providerAPIBase, payload.Project.PathWithNamespace, payload.Issue.IID, payload.Issue.Title, payload.Issue.Description)
	labels := make([]string, 0, len(payload.Issue.Labels))
	for _, label := range payload.Issue.Labels {
		if title := strings.TrimSpace(label.Title); title != "" {
			labels = append(labels, title)
		}
	}
	return domain.AgentTaskCommandEvent{Provider: domain.ProviderGitLab, APIBaseURL: providerAPIBase, DeliveryID: deliveryID, InstallationExternalID: payload.Project.ID.String(), Repository: payload.Project.PathWithNamespace, IssueNumber: payload.Issue.IID, IssueRevision: revision, CommentExternalID: payload.ObjectAttributes.ID.String(), ActorExternalID: payload.User.ID.String(), Body: payload.ObjectAttributes.Note, IssueTitle: payload.Issue.Title, IssueBody: payload.Issue.Description, IssueLabels: labels}, true, nil
}

func NormalizeGitLabAgentTaskMergeRequestNote(deliveryID string, body []byte) (domain.AgentTaskFeedbackEvent, bool, error) {
	var payload struct {
		Project struct {
			ID                json.Number `json:"id"`
			PathWithNamespace string      `json:"path_with_namespace"`
			GitHTTPURL        string      `json:"git_http_url"`
			HTTPURL           string      `json:"http_url"`
		} `json:"project"`
		MergeRequest struct {
			IID int `json:"iid"`
		} `json:"merge_request"`
		ObjectAttributes struct {
			Action       string      `json:"action"`
			ID           json.Number `json:"id"`
			Note         string      `json:"note"`
			NoteableType string      `json:"noteable_type"`
		} `json:"object_attributes"`
		User struct {
			ID json.Number `json:"id"`
		} `json:"user"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.AgentTaskFeedbackEvent{}, false, fmt.Errorf("decode GitLab agent feedback payload: %w", err)
	}
	if payload.ObjectAttributes.Action != "create" || payload.ObjectAttributes.NoteableType != "MergeRequest" {
		return domain.AgentTaskFeedbackEvent{}, false, nil
	}
	if deliveryID == "" {
		digest := sha256.Sum256(body)
		deliveryID = "body-sha256:" + hex.EncodeToString(digest[:])
	}
	cloneURL := gitLabProjectCloneURL(payload.Project.GitHTTPURL, payload.Project.HTTPURL)
	providerAPIBase := gitLabAPIBaseURL(cloneURL, payload.Project.PathWithNamespace)
	if payload.Project.ID == "" || payload.Project.PathWithNamespace == "" || providerAPIBase == "" || payload.MergeRequest.IID <= 0 || payload.ObjectAttributes.ID == "" || payload.User.ID == "" {
		return domain.AgentTaskFeedbackEvent{}, false, fmt.Errorf("GitLab agent feedback payload is missing required fields")
	}
	return domain.AgentTaskFeedbackEvent{Provider: domain.ProviderGitLab, APIBaseURL: providerAPIBase, DeliveryID: deliveryID, InstallationExternalID: payload.Project.ID.String(), Repository: payload.Project.PathWithNamespace, PullRequestNumber: payload.MergeRequest.IID, CommentExternalID: payload.ObjectAttributes.ID.String(), ActorExternalID: payload.User.ID.String(), Instruction: payload.ObjectAttributes.Note}, true, nil
}

// NormalizeGitLabIssue is the GitLab counterpart of NormalizeGitHubIssue. It
// accepts open/update/reopen transitions, ignores service accounts, and uses a
// body digest when a self-managed GitLab version omits the event UUID.
func NormalizeGitLabIssue(deliveryID string, body []byte, now time.Time) (domain.ProviderIssueEvent, bool, error) {
	var payload struct {
		ObjectKind string `json:"object_kind"`
		Project    struct {
			ID                json.Number `json:"id"`
			PathWithNamespace string      `json:"path_with_namespace"`
			GitHTTPURL        string      `json:"git_http_url"`
			HTTPURL           string      `json:"http_url"`
			WebURL            string      `json:"web_url"`
		} `json:"project"`
		ObjectAttributes struct {
			Action      string `json:"action"`
			IID         int    `json:"iid"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"object_attributes"`
		User struct {
			Username string `json:"username"`
			Name     string `json:"name"`
			Bot      bool   `json:"bot"`
		} `json:"user"`
		Labels []struct {
			Title string `json:"title"`
		} `json:"labels"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.ProviderIssueEvent{}, false, fmt.Errorf("decode GitLab issue payload: %w", err)
	}
	if payload.ObjectKind != "issue" || (payload.ObjectAttributes.Action != "open" && payload.ObjectAttributes.Action != "update" && payload.ObjectAttributes.Action != "reopen") {
		return domain.ProviderIssueEvent{}, false, nil
	}
	if payload.User.Bot || strings.HasSuffix(strings.ToLower(payload.User.Username), "-bot") || strings.Contains(payload.ObjectAttributes.Description, "open-review-platform:external-issue:") {
		return domain.ProviderIssueEvent{}, false, nil
	}
	if deliveryID == "" {
		hash := sha256.Sum256(body)
		deliveryID = "body-sha256:" + hex.EncodeToString(hash[:])
	}
	cloneURL := gitLabProjectCloneURL(payload.Project.GitHTTPURL, payload.Project.HTTPURL)
	if cloneURL == "" {
		cloneURL = strings.TrimSpace(payload.Project.WebURL)
	}
	providerAPIBase := gitLabAPIBaseURL(cloneURL, payload.Project.PathWithNamespace)
	if payload.Project.ID == "" || payload.Project.PathWithNamespace == "" || providerAPIBase == "" || payload.ObjectAttributes.IID <= 0 || strings.TrimSpace(payload.ObjectAttributes.Title) == "" {
		return domain.ProviderIssueEvent{}, false, fmt.Errorf("GitLab issue payload is missing required fields")
	}
	labels := make([]string, 0, len(payload.Labels))
	for _, label := range payload.Labels {
		if value := strings.TrimSpace(label.Title); value != "" {
			labels = append(labels, value)
		}
	}
	return domain.ProviderIssueEvent{
		Provider: domain.ProviderGitLab, APIBaseURL: providerAPIBase, DeliveryID: deliveryID,
		EventName: "Issue Hook", InstallationExternalID: payload.Project.ID.String(), Repository: payload.Project.PathWithNamespace,
		IssueNumber: payload.ObjectAttributes.IID, Action: payload.ObjectAttributes.Action, Title: boundedRunes(payload.ObjectAttributes.Title, 500),
		Body: boundedRunes(payload.ObjectAttributes.Description, 30000), Author: boundedRunes(payload.User.Username, 255), AuthorType: "User",
		Labels: labels, Payload: append(json.RawMessage(nil), body...), ReceivedAt: now.UTC(),
	}, true, nil
}

func boundedRunes(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maximum {
		return string(runes[:maximum])
	}
	return value
}

// NormalizeGitLabEmoji accepts GitLab Emoji Hook reactions on finding notes.
// GitLab CE 17.11 emits an "award" event_type but leaves
// object_attributes.action empty for an added award; newer deployments may
// include the explicit award/revoke action. Unknown action shapes fail closed.
func NormalizeGitLabEmoji(deliveryID string, body []byte) (domain.FindingReaction, bool, error) {
	var payload struct {
		ObjectKind string `json:"object_kind"`
		EventType  string `json:"event_type"`
		User       struct {
			ID json.Number `json:"id"`
		} `json:"user"`
		Project struct {
			PathWithNamespace string `json:"path_with_namespace"`
		} `json:"project"`
		ObjectAttributes struct {
			ID            json.Number `json:"id"`
			Name          string      `json:"name"`
			AwardableType string      `json:"awardable_type"`
			Action        string      `json:"action"`
		} `json:"object_attributes"`
		Note struct {
			Note        string `json:"note"`
			Description string `json:"description"`
		} `json:"note"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.FindingReaction{}, false, fmt.Errorf("decode GitLab emoji payload: %w", err)
	}
	action, actionable := gitLabEmojiAction(payload.EventType, payload.ObjectAttributes.Action)
	if payload.ObjectKind != "emoji" || payload.ObjectAttributes.AwardableType != "Note" || !actionable {
		return domain.FindingReaction{}, false, nil
	}
	kind := ""
	switch payload.ObjectAttributes.Name {
	case "thumbsup", "+1":
		kind = "useful"
	case "thumbsdown", "-1":
		kind = "false_positive"
	default:
		return domain.FindingReaction{}, false, nil
	}
	noteBody := payload.Note.Note
	if noteBody == "" {
		noteBody = payload.Note.Description
	}
	marker := providerCommentMarker(noteBody, "open-review-platform:finding:")
	if deliveryID == "" {
		hash := sha256.Sum256(body)
		deliveryID = "body-sha256:" + hex.EncodeToString(hash[:])
	}
	if payload.User.ID.String() == "" || payload.Project.PathWithNamespace == "" || payload.ObjectAttributes.ID.String() == "" || marker == "" {
		return domain.FindingReaction{}, false, nil
	}
	return domain.FindingReaction{Provider: domain.ProviderGitLab, DeliveryID: deliveryID, ReactionExternalID: payload.ObjectAttributes.ID.String(), ActorExternalID: payload.User.ID.String(), Repository: payload.Project.PathWithNamespace, FindingMarker: marker, Kind: kind, Action: action}, true, nil
}

// NormalizeGitLabProviderIssueEmoji is the Issue-analysis counterpart of
// NormalizeGitLabEmoji. Award/revoke events become feedback only when the note
// contains an exact Open Review Issue-analysis marker.
func NormalizeGitLabProviderIssueEmoji(deliveryID string, body []byte) (domain.ProviderIssueReaction, bool, error) {
	var payload struct {
		ObjectKind string `json:"object_kind"`
		EventType  string `json:"event_type"`
		User       struct {
			ID json.Number `json:"id"`
		} `json:"user"`
		Project struct {
			PathWithNamespace string `json:"path_with_namespace"`
		} `json:"project"`
		ObjectAttributes struct {
			ID            json.Number `json:"id"`
			Name          string      `json:"name"`
			AwardableType string      `json:"awardable_type"`
			Action        string      `json:"action"`
		} `json:"object_attributes"`
		Note struct {
			Note        string `json:"note"`
			Description string `json:"description"`
		} `json:"note"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.ProviderIssueReaction{}, false, fmt.Errorf("decode GitLab provider Issue emoji payload: %w", err)
	}
	action, actionable := gitLabEmojiAction(payload.EventType, payload.ObjectAttributes.Action)
	if payload.ObjectKind != "emoji" || payload.ObjectAttributes.AwardableType != "Note" || !actionable {
		return domain.ProviderIssueReaction{}, false, nil
	}
	kind := ""
	switch payload.ObjectAttributes.Name {
	case "thumbsup", "+1":
		kind = "useful"
	case "thumbsdown", "-1":
		kind = "not_useful"
	default:
		return domain.ProviderIssueReaction{}, false, nil
	}
	noteBody := payload.Note.Note
	if noteBody == "" {
		noteBody = payload.Note.Description
	}
	marker := providerCommentMarker(noteBody, "open-review-platform:issue-triage:")
	if deliveryID == "" {
		hash := sha256.Sum256(body)
		deliveryID = "body-sha256:" + hex.EncodeToString(hash[:])
	}
	if payload.User.ID.String() == "" || payload.Project.PathWithNamespace == "" || payload.ObjectAttributes.ID.String() == "" || marker == "" {
		return domain.ProviderIssueReaction{}, false, nil
	}
	return domain.ProviderIssueReaction{
		Provider: domain.ProviderGitLab, DeliveryID: deliveryID,
		ReactionExternalID: payload.ObjectAttributes.ID.String(), ActorExternalID: payload.User.ID.String(),
		Repository: payload.Project.PathWithNamespace, AnalysisMarker: marker, Kind: kind, Action: action,
	}, true, nil
}

// gitLabEmojiAction accepts both GitLab webhook shapes observed in GitLab CE
// 17.11: event_type=award/revoke with an omitted action, and deployments that
// send the explicit award/revoke action. Any other shape still fails closed.
func gitLabEmojiAction(eventType, rawAction string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(rawAction)) {
	case "award", "create", "created":
		return "created", true
	case "revoke", "delete", "deleted":
		return "deleted", true
	}
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "award":
		return "created", true
	case "revoke":
		return "deleted", true
	}
	return "", false
}

func providerCommentMarker(body, prefix string) string {
	for _, token := range strings.Fields(body) {
		candidate := strings.TrimSuffix(strings.TrimSpace(token), "-->")
		if strings.HasPrefix(candidate, prefix) {
			return candidate
		}
	}
	return ""
}

func NormalizeGitLab(deliveryID, eventName string, body []byte, now time.Time) (domain.InboundEvent, bool, error) {
	if eventName != "Merge Request Hook" {
		return domain.InboundEvent{}, false, nil
	}
	var payload struct {
		Project struct {
			ID                json.Number `json:"id"`
			PathWithNamespace string      `json:"path_with_namespace"`
			GitHTTPURL        string      `json:"git_http_url"`
			HTTPURL           string      `json:"http_url"`
		} `json:"project"`
		ObjectAttributes struct {
			Action         string      `json:"action"`
			AuthorID       json.Number `json:"author_id"`
			Title          string      `json:"title"`
			IID            int         `json:"iid"`
			TargetBranch   string      `json:"target_branch"`
			SourceBranch   string      `json:"source_branch"`
			Draft          bool        `json:"draft"`
			WorkInProgress bool        `json:"work_in_progress"`
			LastCommit     struct {
				ID string `json:"id"`
			} `json:"last_commit"`
		} `json:"object_attributes"`
		User struct {
			ID       json.Number `json:"id"`
			Username string      `json:"username"`
		} `json:"user"`
		Labels []struct {
			Title string `json:"title"`
			Name  string `json:"name"`
		} `json:"labels"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return domain.InboundEvent{}, false, fmt.Errorf("decode GitLab payload: %w", err)
	}
	if payload.ObjectAttributes.Action != "open" && payload.ObjectAttributes.Action != "update" && payload.ObjectAttributes.Action != "reopen" {
		return domain.InboundEvent{}, false, nil
	}
	if deliveryID == "" {
		hash := sha256.Sum256(body)
		deliveryID = "body-sha256:" + hex.EncodeToString(hash[:])
	}
	cloneURL := gitLabProjectCloneURL(payload.Project.GitHTTPURL, payload.Project.HTTPURL)
	providerAPIBase := gitLabAPIBaseURL(cloneURL, payload.Project.PathWithNamespace)
	if payload.Project.ID == "" || payload.Project.PathWithNamespace == "" || providerAPIBase == "" || payload.ObjectAttributes.IID <= 0 || payload.ObjectAttributes.TargetBranch == "" || payload.ObjectAttributes.SourceBranch == "" || payload.ObjectAttributes.LastCommit.ID == "" {
		return domain.InboundEvent{}, false, fmt.Errorf("GitLab merge request payload is missing required review fields")
	}
	labels := make([]string, 0, len(payload.Labels))
	for _, label := range payload.Labels {
		name := strings.TrimSpace(label.Title)
		if name == "" {
			name = strings.TrimSpace(label.Name)
		}
		if name != "" {
			labels = append(labels, name)
		}
	}
	authorID := providerActorID(payload.ObjectAttributes.AuthorID)
	author := ""
	// GitLab's top-level user is the event actor. Its username identifies the
	// MR author only when the stable IDs match (for example, on an author push).
	if authorID != "" && authorID == providerActorID(payload.User.ID) {
		author = strings.TrimSpace(payload.User.Username)
	}
	return domain.InboundEvent{
		Provider:               domain.ProviderGitLab,
		APIBaseURL:             providerAPIBase,
		DeliveryID:             deliveryID,
		EventName:              eventName,
		InstallationExternalID: payload.Project.ID.String(),
		Repository:             payload.Project.PathWithNamespace,
		CloneURL:               cloneURL,
		ReviewNumber:           payload.ObjectAttributes.IID,
		BaseRef:                payload.ObjectAttributes.TargetBranch,
		HeadRef:                payload.ObjectAttributes.SourceBranch,
		HeadSHA:                payload.ObjectAttributes.LastCommit.ID,
		Payload:                append(json.RawMessage(nil), body...),
		ReceivedAt:             now.UTC(),
		Action:                 payload.ObjectAttributes.Action,
		IsDraft:                payload.ObjectAttributes.Draft || payload.ObjectAttributes.WorkInProgress,
		Title:                  strings.TrimSpace(payload.ObjectAttributes.Title),
		Author:                 author,
		AuthorExternalID:       authorID,
		Labels:                 labels,
	}, true, nil
}

// The webhook author ID is compared to an OAuth-bound provider identity.
// Reject exponent notation, zero and overflow instead of comparing display
// names or treating an updater's ID as the merge request author's ID.
func providerActorID(value json.Number) string {
	id, err := strconv.ParseUint(value.String(), 10, 64)
	if err != nil || id == 0 {
		return ""
	}
	return strconv.FormatUint(id, 10)
}

// GitLab documents git_http_url as the replacement for the deprecated
// http_url project field. Keep the latter only for older self-managed hooks.
func gitLabProjectCloneURL(gitHTTPURL, legacyHTTPURL string) string {
	if value := strings.TrimSpace(gitHTTPURL); value != "" {
		return value
	}
	return strings.TrimSpace(legacyHTTPURL)
}

// The provider API root includes a relative URL prefix when self-managed
// GitLab is mounted below the host root. Derive that prefix only by removing
// the exact webhook repository path; a different project path cannot choose
// an arbitrary API origin for admission.
func gitLabAPIBaseURL(cloneURL, repository string) string {
	parsed, err := url.Parse(strings.TrimSpace(cloneURL))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	projectPath := strings.Trim(strings.TrimSpace(repository), "/")
	clonePath := strings.TrimSuffix(parsed.Path, ".git")
	if projectPath == "" || path.Clean("/"+projectPath) != "/"+projectPath || path.Clean(clonePath) != clonePath || !strings.HasSuffix(clonePath, "/"+projectPath) {
		return ""
	}
	prefix := strings.TrimSuffix(clonePath, "/"+projectPath)
	return parsed.Scheme + "://" + parsed.Host + prefix + "/api/v4"
}

func githubAPIBaseURL(cloneURL string) string {
	parsed, err := url.Parse(cloneURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if strings.EqualFold(parsed.Host, "github.com") {
		return "https://api.github.com"
	}
	return parsed.Scheme + "://" + parsed.Host + "/api/v3"
}
