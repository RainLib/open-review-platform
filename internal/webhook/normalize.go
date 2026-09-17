package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
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
	if payload.Action != "opened" && payload.Action != "reopened" && payload.Action != "synchronize" {
		return domain.InboundEvent{}, false, nil
	}
	if deliveryID == "" || payload.Installation.ID == "" || payload.Repository.FullName == "" || payload.Repository.CloneURL == "" || payload.Number <= 0 || payload.PullRequest.Base.Ref == "" || payload.PullRequest.Head.SHA == "" {
		return domain.InboundEvent{}, false, fmt.Errorf("GitHub pull_request payload is missing required review fields")
	}
	cloneURL := payload.Repository.CloneURL
	if payload.PullRequest.Head.Repo.CloneURL != "" {
		cloneURL = payload.PullRequest.Head.Repo.CloneURL
	}
	return domain.InboundEvent{
		Provider:               domain.ProviderGitHub,
		APIBaseURL:             apiBaseURL(domain.ProviderGitHub, cloneURL),
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
	return domain.CommentEvent{Provider: domain.ProviderGitHub, APIBaseURL: apiBaseURL(domain.ProviderGitHub, payload.Repository.CloneURL), DeliveryID: deliveryID, InstallationExternalID: payload.Installation.ID.String(), Repository: payload.Repository.FullName, ReviewNumber: payload.Issue.Number, CommentExternalID: payload.Comment.ID.String(), ActorExternalID: payload.Comment.User.ID.String(), Body: payload.Comment.Body}, true, nil
}

// NormalizeGitLabNoteComment accepts notes authored on a merge request. It
// deliberately excludes issue, commit, and snippet notes so @openreview cannot
// start work outside a registered code-review target.
func NormalizeGitLabNoteComment(deliveryID string, body []byte) (domain.CommentEvent, bool, error) {
	var payload struct {
		Project struct {
			ID                json.Number `json:"id"`
			PathWithNamespace string      `json:"path_with_namespace"`
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
	if payload.Project.ID == "" || payload.Project.PathWithNamespace == "" || payload.Project.HTTPURL == "" || payload.MergeRequest.IID <= 0 || payload.ObjectAttributes.ID == "" || payload.User.ID == "" {
		return domain.CommentEvent{}, false, fmt.Errorf("GitLab note payload is missing required review fields")
	}
	return domain.CommentEvent{
		Provider: domain.ProviderGitLab, APIBaseURL: apiBaseURL(domain.ProviderGitLab, payload.Project.HTTPURL), DeliveryID: deliveryID,
		InstallationExternalID: payload.Project.ID.String(), Repository: payload.Project.PathWithNamespace, ReviewNumber: payload.MergeRequest.IID,
		CommentExternalID: payload.ObjectAttributes.ID.String(), ActorExternalID: payload.User.ID.String(), Body: payload.ObjectAttributes.Note,
	}, true, nil
}

func NormalizeGitLab(deliveryID, eventName string, body []byte, now time.Time) (domain.InboundEvent, bool, error) {
	if eventName != "Merge Request Hook" {
		return domain.InboundEvent{}, false, nil
	}
	var payload struct {
		Project struct {
			ID                json.Number `json:"id"`
			PathWithNamespace string      `json:"path_with_namespace"`
			HTTPURL           string      `json:"http_url"`
		} `json:"project"`
		ObjectAttributes struct {
			Action       string `json:"action"`
			IID          int    `json:"iid"`
			TargetBranch string `json:"target_branch"`
			SourceBranch string `json:"source_branch"`
			LastCommit   struct {
				ID string `json:"id"`
			} `json:"last_commit"`
		} `json:"object_attributes"`
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
	if payload.Project.ID == "" || payload.Project.PathWithNamespace == "" || payload.Project.HTTPURL == "" || payload.ObjectAttributes.IID <= 0 || payload.ObjectAttributes.TargetBranch == "" || payload.ObjectAttributes.SourceBranch == "" || payload.ObjectAttributes.LastCommit.ID == "" {
		return domain.InboundEvent{}, false, fmt.Errorf("GitLab merge request payload is missing required review fields")
	}
	return domain.InboundEvent{
		Provider:               domain.ProviderGitLab,
		APIBaseURL:             apiBaseURL(domain.ProviderGitLab, payload.Project.HTTPURL),
		DeliveryID:             deliveryID,
		EventName:              eventName,
		InstallationExternalID: payload.Project.ID.String(),
		Repository:             payload.Project.PathWithNamespace,
		CloneURL:               payload.Project.HTTPURL,
		ReviewNumber:           payload.ObjectAttributes.IID,
		BaseRef:                payload.ObjectAttributes.TargetBranch,
		HeadRef:                payload.ObjectAttributes.SourceBranch,
		HeadSHA:                payload.ObjectAttributes.LastCommit.ID,
		Payload:                append(json.RawMessage(nil), body...),
		ReceivedAt:             now.UTC(),
	}, true, nil
}

func apiBaseURL(provider domain.Provider, cloneURL string) string {
	parsed, err := url.Parse(cloneURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if provider == domain.ProviderGitHub {
		if strings.EqualFold(parsed.Host, "github.com") {
			return "https://api.github.com"
		}
		return parsed.Scheme + "://" + parsed.Host + "/api/v3"
	}
	return parsed.Scheme + "://" + parsed.Host + "/api/v4"
}
