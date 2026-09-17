package webhook

import "testing"

func TestNormalizeGitHubIssueCommentForPullRequest(t *testing.T) {
	body := []byte(`{"action":"created","installation":{"id":123},"repository":{"full_name":"RainLib/open-review-platform"},"issue":{"number":2,"pull_request":{"url":"https://api.github.com/repos/RainLib/open-review-platform/pulls/2"}},"comment":{"id":99,"body":"@openreview review --mode=deep","user":{"id":42}}}`)
	event, accepted, err := NormalizeGitHubIssueComment("delivery-42", body)
	if err != nil || !accepted {
		t.Fatalf("expected accepted comment: %#v accepted=%v err=%v", event, accepted, err)
	}
	if event.Repository != "RainLib/open-review-platform" || event.ReviewNumber != 2 || event.ActorExternalID != "42" {
		t.Fatalf("unexpected normalized event: %#v", event)
	}
}

func TestNormalizeGitHubIssueCommentIgnoresIssues(t *testing.T) {
	body := []byte(`{"action":"created","installation":{"id":123},"repository":{"full_name":"RainLib/open-review-platform"},"issue":{"number":2},"comment":{"id":99,"body":"@openreview review","user":{"id":42}}}`)
	if _, accepted, err := NormalizeGitHubIssueComment("delivery-42", body); err != nil || accepted {
		t.Fatalf("issue comment must be ignored: accepted=%v err=%v", accepted, err)
	}
}
