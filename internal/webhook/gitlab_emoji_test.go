package webhook

import "testing"

func TestNormalizeGitLabEmojiFindingFeedback(t *testing.T) {
	body := []byte(`{"object_kind":"emoji","event_type":"award","user":{"id":42},"project":{"path_with_namespace":"RainLib/open-review-platform"},"object_attributes":{"id":91,"name":"thumbsup","awardable_type":"Note","action":"award"},"note":{"note":"Finding\n<!-- open-review-platform:finding:abc123 -->"}}`)
	reaction, accepted, err := NormalizeGitLabEmoji("event-uuid", body)
	if err != nil || !accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
	if reaction.Kind != "useful" || reaction.Action != "created" || reaction.Provider != "gitlab" || reaction.FindingMarker != "open-review-platform:finding:abc123" {
		t.Fatalf("unexpected reaction: %#v", reaction)
	}
}

func TestNormalizeGitLabEmojiRevoke(t *testing.T) {
	body := []byte(`{"object_kind":"emoji","event_type":"award","user":{"id":42},"project":{"path_with_namespace":"RainLib/open-review-platform"},"object_attributes":{"id":91,"name":"thumbsdown","awardable_type":"Note","action":"revoke"},"note":{"description":"<!-- open-review-platform:finding:abc123 -->"}}`)
	reaction, accepted, err := NormalizeGitLabEmoji("event-uuid", body)
	if err != nil || !accepted || reaction.Kind != "false_positive" || reaction.Action != "deleted" {
		t.Fatalf("reaction=%#v accepted=%v err=%v", reaction, accepted, err)
	}
}

func TestNormalizeGitLabEmojiUsesGitLabCERevokeEventWhenActionIsOmitted(t *testing.T) {
	// GitLab CE 17.11 emits event_type=revoke and action=null for a removed award.
	body := []byte(`{"object_kind":"emoji","event_type":"revoke","user":{"id":42},"project":{"path_with_namespace":"RainLib/open-review-platform"},"object_attributes":{"id":91,"name":"thumbsdown","awardable_type":"Note"},"note":{"description":"<!-- open-review-platform:finding:abc123 -->"}}`)
	reaction, accepted, err := NormalizeGitLabEmoji("event-uuid", body)
	if err != nil || !accepted || reaction.Kind != "false_positive" || reaction.Action != "deleted" {
		t.Fatalf("reaction=%#v accepted=%v err=%v", reaction, accepted, err)
	}
}

func TestNormalizeGitLabProviderIssueEmojiFeedback(t *testing.T) {
	body := []byte(`{"object_kind":"emoji","event_type":"award","user":{"id":42},"project":{"path_with_namespace":"RainLib/open-review-platform"},"object_attributes":{"id":93,"name":"thumbsdown","awardable_type":"Note","action":"award"},"note":{"note":"Issue analysis\n<!-- open-review-platform:issue-triage:4362e8f7-68eb-4d09-b0a9-747577fb0514 -->"}}`)
	reaction, accepted, err := NormalizeGitLabProviderIssueEmoji("event-issue-emoji", body)
	if err != nil || !accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
	if reaction.Kind != "not_useful" || reaction.Provider != "gitlab" || reaction.AnalysisMarker != "open-review-platform:issue-triage:4362e8f7-68eb-4d09-b0a9-747577fb0514" {
		t.Fatalf("unexpected reaction: %#v", reaction)
	}
}

func TestNormalizeGitLabProviderIssueEmojiUsesGitLabCEAwardEventWhenActionIsOmitted(t *testing.T) {
	// GitLab CE 17.11 emits event_type=award and action=null for a new award.
	body := []byte(`{"object_kind":"emoji","event_type":"award","user":{"id":42},"project":{"path_with_namespace":"RainLib/open-review-platform"},"object_attributes":{"id":93,"name":"thumbsup","awardable_type":"Note"},"note":{"note":"Issue analysis\n<!-- open-review-platform:issue-triage:4362e8f7-68eb-4d09-b0a9-747577fb0514 -->"}}`)
	reaction, accepted, err := NormalizeGitLabProviderIssueEmoji("event-issue-emoji", body)
	if err != nil || !accepted || reaction.Kind != "useful" || reaction.Action != "created" {
		t.Fatalf("reaction=%#v accepted=%v err=%v", reaction, accepted, err)
	}
}

func TestNormalizeGitLabProviderIssueEmojiUsesGitLabCERevokeEventWhenActionIsOmitted(t *testing.T) {
	// GitLab CE 17.11 emits event_type=revoke and action=null for a removed award.
	body := []byte(`{"object_kind":"emoji","event_type":"revoke","user":{"id":42},"project":{"path_with_namespace":"RainLib/open-review-platform"},"object_attributes":{"id":93,"name":"thumbsup","awardable_type":"Note"},"note":{"note":"Issue analysis\n<!-- open-review-platform:issue-triage:4362e8f7-68eb-4d09-b0a9-747577fb0514 -->"}}`)
	reaction, accepted, err := NormalizeGitLabProviderIssueEmoji("event-issue-emoji", body)
	if err != nil || !accepted || reaction.Kind != "useful" || reaction.Action != "deleted" {
		t.Fatalf("reaction=%#v accepted=%v err=%v", reaction, accepted, err)
	}
}
