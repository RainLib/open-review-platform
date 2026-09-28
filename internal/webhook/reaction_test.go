package webhook

import "testing"

func TestNormalizeGitHubReactionForFinding(t *testing.T) {
	body := []byte(`{"action":"created","repository":{"full_name":"RainLib/open-review-platform"},"comment":{"body":"Finding body\n<!-- open-review-platform:finding:abc123 -->"},"reaction":{"id":91,"content":"-1","user":{"id":42}}}`)
	reaction, accepted, err := NormalizeGitHubReaction("delivery-reaction-1", body)
	if err != nil || !accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
	if reaction.Kind != "false_positive" || reaction.FindingMarker != "open-review-platform:finding:abc123" || reaction.ActorExternalID != "42" {
		t.Fatalf("unexpected reaction: %#v", reaction)
	}
}

func TestNormalizeGitHubReactionIgnoresUnrelatedEmoji(t *testing.T) {
	body := []byte(`{"action":"created","repository":{"full_name":"RainLib/open-review-platform"},"comment":{"body":"<!-- open-review-platform:finding:abc123 -->"},"reaction":{"id":91,"content":"heart","user":{"id":42}}}`)
	if _, accepted, err := NormalizeGitHubReaction("delivery-reaction-1", body); err != nil || accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
}

func TestNormalizeGitHubProviderIssueReaction(t *testing.T) {
	body := []byte(`{"action":"created","repository":{"full_name":"RainLib/open-review-platform"},"comment":{"body":"Analysis\n<!-- open-review-platform:issue-triage:4362e8f7-68eb-4d09-b0a9-747577fb0514 -->"},"reaction":{"id":92,"content":"+1","user":{"id":43}}}`)
	reaction, accepted, err := NormalizeGitHubProviderIssueReaction("delivery-issue-reaction-1", body)
	if err != nil || !accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
	if reaction.Kind != "useful" || reaction.AnalysisMarker != "open-review-platform:issue-triage:4362e8f7-68eb-4d09-b0a9-747577fb0514" || reaction.ActorExternalID != "43" {
		t.Fatalf("unexpected reaction: %#v", reaction)
	}
}

func TestNormalizeGitHubProviderIssueReactionIgnoresEyesAcknowledgement(t *testing.T) {
	body := []byte(`{"action":"created","repository":{"full_name":"RainLib/open-review-platform"},"issue":{"body":"Issue body"},"reaction":{"id":92,"content":"eyes","user":{"id":43}}}`)
	if _, accepted, err := NormalizeGitHubProviderIssueReaction("delivery-issue-reaction-2", body); err != nil || accepted {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
}
