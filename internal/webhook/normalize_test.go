package webhook

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestNormalizeGitHubPullRequest(t *testing.T) {
	for _, action := range []string{"opened", "reopened", "synchronize", "ready_for_review"} {
		t.Run(action, func(t *testing.T) {
			body := []byte(`{"action":"` + action + `","number":42,"installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"pull_request":{"title":"  Tighten redirect validation  ","draft":true,"user":{"id":987,"login":"dependabot[bot]"},"labels":[{"name":"review-ready"}],"base":{"ref":"main","sha":"base"},"head":{"ref":"feature","sha":"head"}}}`)
			event, accepted, err := NormalizeGitHub("delivery-1", "pull_request", body, time.Now())
			if err != nil || !accepted {
				t.Fatalf("expected accepted event, accepted=%v err=%v", accepted, err)
			}
			if event.InstallationExternalID != "123" || event.ReviewNumber != 42 || event.HeadSHA != "head" || event.Action != action || !event.IsDraft || event.Title != "Tighten redirect validation" || event.Author != "dependabot[bot]" || event.AuthorExternalID != "987" || len(event.Labels) != 1 || event.Labels[0] != "review-ready" {
				t.Fatalf("unexpected normalized event: %#v", event)
			}
		})
	}
}

func TestNormalizeGitLabMergeRequest(t *testing.T) {
	body := []byte(`{"user":{"id":321,"username":"renovate"},"labels":[{"title":"review-ready"}],"project":{"id":456,"path_with_namespace":"acme/api","http_url":"https://gitlab.com/acme/api.git"},"object_attributes":{"action":"update","author_id":321,"title":"Refactor webhook intake","draft":true,"iid":9,"target_branch":"main","source_branch":"feature","last_commit":{"id":"head"}}}`)
	event, accepted, err := NormalizeGitLab("delivery-1", "Merge Request Hook", body, time.Now())
	if err != nil || !accepted {
		t.Fatalf("expected accepted event, accepted=%v err=%v", accepted, err)
	}
	if event.InstallationExternalID != "456" || event.ReviewNumber != 9 || event.HeadSHA != "head" || event.Action != "update" || !event.IsDraft || event.Title != "Refactor webhook intake" || event.Author != "renovate" || event.AuthorExternalID != "321" || len(event.Labels) != 1 || event.Labels[0] != "review-ready" {
		t.Fatalf("unexpected normalized event: %#v", event)
	}
}

func TestNormalizeGitLabMergeRequestDoesNotAttributeUpdaterAsAuthor(t *testing.T) {
	body := []byte(`{"user":{"id":999,"username":"reviewer"},"project":{"id":456,"path_with_namespace":"acme/api","http_url":"https://gitlab.com/acme/api.git"},"object_attributes":{"action":"update","author_id":321,"title":"Updated merge request","iid":9,"target_branch":"main","source_branch":"feature","last_commit":{"id":"head"}}}`)
	event, accepted, err := NormalizeGitLab("delivery-2", "Merge Request Hook", body, time.Now())
	if err != nil || !accepted || event.Author != "" || event.AuthorExternalID != "321" {
		t.Fatalf("updater must not be presented as MR author: event=%#v accepted=%t err=%v", event, accepted, err)
	}
}

func TestGitLabModernProjectURLAcrossReviewAndAgentWebhooks(t *testing.T) {
	project := `"project":{"id":77,"path_with_namespace":"acme/api","git_http_url":"https://gitlab.example/gitlab/acme/api.git"}`
	wantAPI := "https://gitlab.example/gitlab/api/v4"
	wantClone := "https://gitlab.example/gitlab/acme/api.git"

	mergeRequest := []byte(`{` + project + `,"user":{"id":42,"username":"author"},"object_attributes":{"action":"open","author_id":42,"title":"Validate URL","iid":9,"target_branch":"main","source_branch":"feature","last_commit":{"id":"head"}}}`)
	review, accepted, err := NormalizeGitLab("mr", "Merge Request Hook", mergeRequest, time.Now())
	if err != nil || !accepted || review.APIBaseURL != wantAPI || review.CloneURL != wantClone {
		t.Fatalf("modern GitLab MR URL: event=%#v accepted=%t err=%v", review, accepted, err)
	}
	mergeRequestWithLegacy := []byte(strings.ReplaceAll(string(mergeRequest), `"git_http_url":"https://gitlab.example/gitlab/acme/api.git"`, `"git_http_url":"https://gitlab.example/gitlab/acme/api.git","http_url":"https://obsolete.example/acme/api.git"`))
	review, accepted, err = NormalizeGitLab("mr-both", "Merge Request Hook", mergeRequestWithLegacy, time.Now())
	if err != nil || !accepted || review.APIBaseURL != wantAPI || review.CloneURL != wantClone {
		t.Fatalf("GitLab MR should prefer git_http_url: event=%#v accepted=%t err=%v", review, accepted, err)
	}
	wrongProjectURL := []byte(strings.ReplaceAll(string(mergeRequest), "/gitlab/acme/api.git", "/gitlab/other/api.git"))
	_, accepted, err = NormalizeGitLab("mr-wrong-project", "Merge Request Hook", wrongProjectURL, time.Now())
	if err == nil || accepted {
		t.Fatalf("different GitLab project path must be rejected: accepted=%t err=%v", accepted, err)
	}

	comment := []byte(`{` + project + `,"merge_request":{"iid":9},"object_attributes":{"action":"create","id":81,"note":"@openreview review","noteable_type":"MergeRequest"},"user":{"id":42}}`)
	reviewComment, accepted, err := NormalizeGitLabNoteComment("comment", comment)
	if err != nil || !accepted || reviewComment.APIBaseURL != wantAPI || reviewComment.CloneURL != wantClone {
		t.Fatalf("modern GitLab review comment URL: event=%#v accepted=%t err=%v", reviewComment, accepted, err)
	}
	feedback, accepted, err := NormalizeGitLabAgentTaskMergeRequestNote("feedback", comment)
	if err != nil || !accepted || feedback.APIBaseURL != wantAPI {
		t.Fatalf("modern GitLab feedback URL: event=%#v accepted=%t err=%v", feedback, accepted, err)
	}

	issue := []byte(`{"object_kind":"issue",` + project + `,"object_attributes":{"action":"open","iid":8,"title":"Issue with modern project URL","description":"Steps to reproduce"},"user":{"username":"author"}}`)
	providerIssue, accepted, err := NormalizeGitLabIssue("issue", issue, time.Now())
	if err != nil || !accepted || providerIssue.APIBaseURL != wantAPI {
		t.Fatalf("modern GitLab Issue URL: event=%#v accepted=%t err=%v", providerIssue, accepted, err)
	}

	issueComment := []byte(`{` + project + `,"issue":{"iid":8,"title":"Issue with modern project URL","description":"Steps to reproduce"},"object_attributes":{"action":"create","id":82,"note":"@openreview implement","noteable_type":"Issue"},"user":{"id":42}}`)
	command, accepted, err := NormalizeGitLabAgentTaskIssueNote("issue-comment", issueComment)
	if err != nil || !accepted || command.APIBaseURL != wantAPI {
		t.Fatalf("modern GitLab Issue command URL: event=%#v accepted=%t err=%v", command, accepted, err)
	}
}

func TestGitLabAPIBaseURLRequiresExactRepositorySuffix(t *testing.T) {
	for _, test := range []struct {
		cloneURL, repository, want string
	}{
		{"https://gitlab.example/acme/api.git", "acme/api", "https://gitlab.example/api/v4"},
		{"https://gitlab.example/gitlab/acme/api.git", "acme/api", "https://gitlab.example/gitlab/api/v4"},
		{"https://gitlab.example/foo/bar/gitlab/acme/api", "acme/api", "https://gitlab.example/foo/bar/gitlab/api/v4"},
		{"https://gitlab.example/gitlab/other/api.git", "acme/api", ""},
		{"https://gitlab.example/gitlab/acme/api-extra.git", "acme/api", ""},
		{"https://user:secret@gitlab.example/gitlab/acme/api.git", "acme/api", ""},
		{"https://gitlab.example/gitlab/acme/api.git?token=secret", "acme/api", ""},
		{"https://gitlab.example/gitlab/../acme/api.git", "acme/api", ""},
	} {
		if got := gitLabAPIBaseURL(test.cloneURL, test.repository); got != test.want {
			t.Fatalf("clone=%q repository=%q API base=%q, want %q", test.cloneURL, test.repository, got, test.want)
		}
	}
}

func TestProviderActorIDRejectsUnstableOrInvalidValues(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"", ""}, {"0", ""}, {"-1", ""}, {"1e3", ""},
		{"18446744073709551616", ""}, {"00042", "42"}, {"42", "42"},
	} {
		if got := providerActorID(json.Number(test.value)); got != test.want {
			t.Fatalf("providerActorID(%q)=%q, want %q", test.value, got, test.want)
		}
	}
}

func TestNormalizeGitHubIssueAcceptsUsersAndRejectsBots(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"action":"opened","installation":{"id":123},"repository":{"full_name":"RainLib/open-review-platform","clone_url":"https://github.com/RainLib/open-review-platform.git"},"issue":{"number":9,"title":"Webhook retries lose state","body":"Reproduction steps","user":{"login":"contributor","type":"User"},"labels":[{"name":"bug"}]}}`)
	event, accepted, err := NormalizeGitHubIssue("issue-delivery", body, now)
	if err != nil || !accepted {
		t.Fatalf("accepted=%t error=%v", accepted, err)
	}
	if event.Provider != domain.ProviderGitHub || event.IssueNumber != 9 || event.Author != "contributor" || len(event.Labels) != 1 || event.Labels[0] != "bug" {
		t.Fatalf("event=%#v", event)
	}
	body = []byte(`{"action":"opened","installation":{"id":123},"repository":{"full_name":"RainLib/open-review-platform","clone_url":"https://github.com/RainLib/open-review-platform.git"},"issue":{"number":9,"title":"Generated","body":"<!-- open-review-platform:external-issue:id -->","user":{"login":"open-review[bot]","type":"Bot"}}}`)
	_, accepted, err = NormalizeGitHubIssue("bot-delivery", body, now)
	if err != nil || accepted {
		t.Fatalf("bot accepted=%t error=%v", accepted, err)
	}
}

func TestNormalizeGitLabIssueUsesDeterministicDeliveryFallback(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"object_kind":"issue","project":{"id":77,"path_with_namespace":"rainlib/platform","web_url":"https://gitlab.example/rainlib/platform"},"object_attributes":{"action":"open","iid":12,"title":"Queue is stuck","description":"The worker stopped."},"user":{"username":"alice","bot":false},"labels":[{"title":"incident"}]}`)
	event, accepted, err := NormalizeGitLabIssue("", body, now)
	if err != nil || !accepted {
		t.Fatalf("accepted=%t error=%v", accepted, err)
	}
	if event.Provider != domain.ProviderGitLab || event.IssueNumber != 12 || !strings.HasPrefix(event.DeliveryID, "body-sha256:") || event.APIBaseURL != "https://gitlab.example/api/v4" {
		t.Fatalf("event=%#v", event)
	}
}

func TestNormalizeAgentTaskCommandsCarryOnlyIssueSnapshot(t *testing.T) {
	github := []byte(`{"action":"created","installation":{"id":123},"repository":{"full_name":"RainLib/open-review-platform","clone_url":"https://github.com/RainLib/open-review-platform.git"},"issue":{"number":9,"title":"Worker retry loses state","body":"Observed behavior: the retry duplicates work. Expected behavior: one result. Acceptance criteria: a regression test proves it.","updated_at":"2026-09-23T01:02:03Z","labels":[{"name":"bug"}]},"comment":{"id":44,"body":"@openreview implement","user":{"id":55}}}`)
	event, accepted, err := NormalizeGitHubAgentTaskIssueComment("agent-command", github)
	if err != nil || !accepted || event.IssueTitle != "Worker retry loses state" || len(event.IssueLabels) != 1 || event.IssueLabels[0] != "bug" || event.IssueRevision != domain.AgentIssueRevision(event.Provider, event.APIBaseURL, event.Repository, event.IssueNumber, event.IssueTitle, event.IssueBody) {
		t.Fatalf("github event=%#v accepted=%t error=%v", event, accepted, err)
	}
	gitlab := []byte(`{"project":{"id":77,"path_with_namespace":"rainlib/platform","http_url":"https://gitlab.example/rainlib/platform.git"},"issue":{"iid":12,"title":"Queue stalls","description":"Observed behavior: the worker stalls. Expected result: work resumes. Acceptance criteria: a focused test covers recovery.","updated_at":"2026-09-23T01:02:03Z","labels":[{"title":"openreview:implement"}]},"object_attributes":{"action":"create","id":88,"note":"@openreview implement","noteable_type":"Issue"},"user":{"id":99}}`)
	event, accepted, err = NormalizeGitLabAgentTaskIssueNote("agent-note", gitlab)
	if err != nil || !accepted || event.IssueTitle != "Queue stalls" || !strings.Contains(event.IssueBody, "Acceptance criteria") || len(event.IssueLabels) != 1 || event.IssueLabels[0] != "openreview:implement" || event.IssueRevision != domain.AgentIssueRevision(event.Provider, event.APIBaseURL, event.Repository, event.IssueNumber, event.IssueTitle, event.IssueBody) {
		t.Fatalf("gitlab event=%#v accepted=%t error=%v", event, accepted, err)
	}
}

func TestNormalizeAgentTaskFeedbackUsesOnlyPullRequestCommentIdentity(t *testing.T) {
	github := []byte(`{"action":"created","installation":{"id":123},"repository":{"full_name":"RainLib/open-review-platform","clone_url":"https://github.com/RainLib/open-review-platform.git"},"issue":{"number":7,"pull_request":{}},"comment":{"id":44,"body":"@openreview revise Handle the empty response safely.","user":{"id":55}}}`)
	event, accepted, err := NormalizeGitHubAgentTaskPullRequestComment("feedback-command", github)
	if err != nil || !accepted || event.PullRequestNumber != 7 || event.CommentExternalID != "44" || event.ActorExternalID != "55" {
		t.Fatalf("github feedback=%#v accepted=%t error=%v", event, accepted, err)
	}
	gitlab := []byte(`{"project":{"id":77,"path_with_namespace":"rainlib/platform","http_url":"https://gitlab.example/rainlib/platform.git"},"merge_request":{"iid":8},"object_attributes":{"action":"create","id":88,"note":"@openreview revise Preserve the existing Draft MR and test retry recovery.","noteable_type":"MergeRequest"},"user":{"id":99}}`)
	event, accepted, err = NormalizeGitLabAgentTaskMergeRequestNote("feedback-note", gitlab)
	if err != nil || !accepted || event.PullRequestNumber != 8 || event.CommentExternalID != "88" || event.ActorExternalID != "99" {
		t.Fatalf("gitlab feedback=%#v accepted=%t error=%v", event, accepted, err)
	}
}
