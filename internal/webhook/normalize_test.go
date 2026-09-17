package webhook

import (
	"testing"
	"time"
)

func TestNormalizeGitHubPullRequest(t *testing.T) {
	body := []byte(`{"action":"synchronize","number":42,"installation":{"id":123},"repository":{"full_name":"acme/api","clone_url":"https://github.com/acme/api.git"},"pull_request":{"base":{"ref":"main","sha":"base"},"head":{"ref":"feature","sha":"head"}}}`)
	event, accepted, err := NormalizeGitHub("delivery-1", "pull_request", body, time.Now())
	if err != nil || !accepted {
		t.Fatalf("expected accepted event, accepted=%v err=%v", accepted, err)
	}
	if event.InstallationExternalID != "123" || event.ReviewNumber != 42 || event.HeadSHA != "head" {
		t.Fatalf("unexpected normalized event: %#v", event)
	}
}

func TestNormalizeGitLabMergeRequest(t *testing.T) {
	body := []byte(`{"project":{"id":456,"path_with_namespace":"acme/api","http_url":"https://gitlab.com/acme/api.git"},"object_attributes":{"action":"update","iid":9,"target_branch":"main","source_branch":"feature","last_commit":{"id":"head"}}}`)
	event, accepted, err := NormalizeGitLab("delivery-1", "Merge Request Hook", body, time.Now())
	if err != nil || !accepted {
		t.Fatalf("expected accepted event, accepted=%v err=%v", accepted, err)
	}
	if event.InstallationExternalID != "456" || event.ReviewNumber != 9 || event.HeadSHA != "head" {
		t.Fatalf("unexpected normalized event: %#v", event)
	}
}
