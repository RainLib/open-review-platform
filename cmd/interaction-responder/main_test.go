package main

import (
	"context"
	"errors"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type recordingInteractionPublisher struct {
	events *[]string
	err    error
}

func (p recordingInteractionPublisher) PublishInteractionResponse(_ context.Context, _ domain.InteractionResponse) error {
	*p.events = append(*p.events, "published")
	return p.err
}

func (p recordingInteractionPublisher) PrepareInteractionResponse(_ context.Context, response domain.InteractionResponse) (func(context.Context) error, error) {
	*p.events = append(*p.events, "prepared")
	return func(ctx context.Context) error { return p.PublishInteractionResponse(ctx, response) }, nil
}

type recordingInteractionStore struct {
	events     *[]string
	skipSource bool
}

func (s recordingInteractionStore) ReleaseInitialInteractionAdmission(_ context.Context, _ domain.InteractionAdmission) error {
	*s.events = append(*s.events, "admission-released")
	return nil
}

func (s recordingInteractionStore) ReleaseAcknowledgedRun(_ context.Context, _ uuid.UUID) error {
	*s.events = append(*s.events, "run-released")
	return nil
}

func (s recordingInteractionStore) WithAgentTaskAcknowledgementFence(ctx context.Context, _ domain.InteractionResponse, publish func(context.Context) error) error {
	*s.events = append(*s.events, "agent-ack-fenced")
	if s.skipSource {
		return nil
	}
	if err := publish(ctx); err != nil {
		return err
	}
	*s.events = append(*s.events, "agent-source-released")
	return nil
}

func (s recordingInteractionStore) AgentTaskSourceStatusCurrent(_ context.Context, _ uuid.UUID, _ int) (bool, error) {
	*s.events = append(*s.events, "source-current")
	return !s.skipSource, nil
}

func (s recordingInteractionStore) WithAgentTaskSourcePublicationFence(ctx context.Context, _ uuid.UUID, _ int, publish func(context.Context) error) error {
	*s.events = append(*s.events, "source-fenced")
	if s.skipSource {
		return nil
	}
	return publish(ctx)
}

func TestResponseFromPayloadPreservesAcknowledgementBarrier(t *testing.T) {
	runID := uuid.New()
	markerSince := "2026-09-21T07:50:50.742754Z"
	response, err := responseFromPayload(map[string]any{
		"provider":                 "github",
		"api_base_url":             "https://api.github.com",
		"installation_external_id": "42",
		"credential_ref":           "github-app",
		"repository":               "RainLib/demo",
		"review_number":            float64(4),
		"comment_external_id":      "99",
		"reaction":                 "eyes",
		"release_run_id":           runID.String(),
		"body":                     "Review is queued.",
		"marker":                   "open-review-platform:interaction:test",
		"marker_since":             markerSince,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Reaction != domain.InteractionReactionEyes || response.ReleaseRunID == nil || *response.ReleaseRunID != runID || response.MarkerSince.IsZero() {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestResponseFromPayloadAllowsOnlyReviewReactionAcknowledgement(t *testing.T) {
	interactionID := uuid.New()
	base := map[string]any{
		"provider": "github", "api_base_url": "https://api.github.com", "installation_external_id": "42", "credential_ref": "github-app",
		"repository": "RainLib/demo", "resource_kind": "merge_request", "review_number": float64(4), "comment_external_id": "99",
		"reaction": "eyes", "reaction_only": true, "body": "", "marker": "open-review-platform:interaction:" + interactionID.String(),
	}
	response, err := responseFromPayload(base)
	if err != nil || !response.ReactionOnly || response.Body != "" {
		t.Fatalf("reaction-only response=%#v error=%v", response, err)
	}
	for _, change := range []func(map[string]any){
		func(p map[string]any) { p["resource_kind"] = "issue" },
		func(p map[string]any) { p["reaction"] = "confused" },
		func(p map[string]any) { p["body"] = "queued" },
		func(p map[string]any) { p["comment_external_id"] = "" },
		func(p map[string]any) { p["marker"] = "open-review-platform:interaction:not-a-uuid" },
		func(p map[string]any) { p["reaction_only"] = "true" },
	} {
		invalid := make(map[string]any, len(base))
		for key, value := range base {
			invalid[key] = value
		}
		change(invalid)
		if _, err := responseFromPayload(invalid); err == nil {
			t.Fatalf("invalid reaction-only response accepted: %#v", invalid)
		}
	}
}

func TestResponseFromPayloadAcceptsIssueResourceWithoutAdmissionCapability(t *testing.T) {
	response, err := responseFromPayload(map[string]any{
		"provider": "gitlab", "api_base_url": "https://gitlab.example/api/v4", "installation_external_id": "42", "credential_ref": "gitlab-token",
		"repository": "acme/demo", "resource_kind": "issue", "review_number": float64(19), "comment_external_id": "91", "reaction": "eyes",
		"body": "Agent task received.", "marker": "open-review-platform:agent-task:test",
	})
	if err != nil || response.ResourceKind != "issue" || response.Admission != nil || response.ReleaseRunID != nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestAgentSourceReleaseRequiresAProviderAcknowledgement(t *testing.T) {
	taskID, tenantID := uuid.New(), uuid.New()
	payload := map[string]any{
		"tenant_id": tenantID.String(), "provider": "github", "api_base_url": "https://api.github.com",
		"installation_external_id": "42", "credential_ref": "github-app", "repository": "RainLib/demo",
		"resource_kind": "issue", "review_number": float64(19), "comment_external_id": "91", "reaction": "eyes",
		"body": "Agent task received.", "marker": "open-review-platform:agent-task:auto:" + taskID.String(),
		"release_agent_task_source_id": taskID.String(), "release_agent_task_source_revision": float64(1),
	}
	response, err := responseFromPayload(payload)
	if err != nil || response.SourceRelease == nil || response.SourceRelease.TaskID != taskID || response.SourceRelease.Revision != 1 {
		t.Fatalf("source release=%#v error=%v", response.SourceRelease, err)
	}
	for _, mutate := range []func(map[string]any){
		func(p map[string]any) { p["release_agent_task_source_revision"] = float64(0) },
		func(p map[string]any) { p["release_agent_task_source_id"] = "invalid" },
		func(p map[string]any) { p["marker"] = "open-review-platform:agent-task:auto:" + uuid.NewString() },
		func(p map[string]any) { p["tenant_id"] = "" },
		func(p map[string]any) { p["resource_kind"] = "merge_request" },
		func(p map[string]any) { p["release_run_id"] = uuid.NewString() },
	} {
		invalid := make(map[string]any, len(payload))
		for key, value := range payload {
			invalid[key] = value
		}
		mutate(invalid)
		if _, err := responseFromPayload(invalid); err == nil {
			t.Fatalf("invalid source release accepted: %#v", invalid)
		}
	}
	delete(payload, "release_agent_task_source_id")
	if _, err := responseFromPayload(payload); err == nil {
		t.Fatal("source revision without a task ID was accepted")
	}
}

func TestAgentSourceReleaseFollowsProviderPublication(t *testing.T) {
	response := domain.InteractionResponse{SourceRelease: &domain.AgentTaskSourceRelease{TaskID: uuid.New(), Revision: 1}}
	events := []string{}
	if err := publishAndReleaseInteraction(context.Background(), recordingInteractionPublisher{events: &events, err: errors.New("provider unavailable")}, recordingInteractionStore{events: &events}, response); err == nil {
		t.Fatal("failed provider publication released Agent source")
	}
	if len(events) != 2 || events[0] != "agent-ack-fenced" || events[1] != "published" {
		t.Fatalf("failed publication events=%v", events)
	}
	events = nil
	if err := publishAndReleaseInteraction(context.Background(), recordingInteractionPublisher{events: &events}, recordingInteractionStore{events: &events}, response); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0] != "agent-ack-fenced" || events[1] != "published" || events[2] != "agent-source-released" {
		t.Fatalf("Agent source release order=%v", events)
	}
	events = nil
	if err := publishAndReleaseInteraction(context.Background(), recordingInteractionPublisher{events: &events}, recordingInteractionStore{events: &events, skipSource: true}, response); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "agent-ack-fenced" {
		t.Fatalf("stale Agent acknowledgement reached provider: %v", events)
	}
}

func TestResponseFromPayloadAcceptsOnlyVersionedAgentSourceStatus(t *testing.T) {
	taskID := uuid.New()
	payload := map[string]any{
		"provider": "github", "api_base_url": "https://api.github.com", "installation_external_id": "42", "credential_ref": "github-app",
		"repository": "RainLib/demo", "resource_kind": "issue", "review_number": float64(19), "body": "Source verified.",
		"marker": "open-review-platform:agent-task-source:" + taskID.String(), "status_version": float64(4),
	}
	response, err := responseFromPayload(payload)
	if err != nil || response.StatusVersion != 4 {
		t.Fatalf("source version=%d error=%v", response.StatusVersion, err)
	}
	for _, invalid := range []any{float64(0), float64(-1), float64(2.5), "4"} {
		payload["status_version"] = invalid
		if _, err := responseFromPayload(payload); err == nil {
			t.Fatalf("invalid status version %#v was accepted", invalid)
		}
	}
	payload["status_version"] = float64(4)
	payload["marker"] = "open-review-platform:interaction:test"
	if _, err := responseFromPayload(payload); err == nil {
		t.Fatal("non-source status accepted a version fence")
	}
}

func TestAgentSourceStatusIsFencedBeforeProviderWrite(t *testing.T) {
	taskID := uuid.New()
	response := domain.InteractionResponse{ResourceKind: "issue", Marker: "open-review-platform:agent-task-source:" + taskID.String(), StatusVersion: 4}
	events := []string{}
	if err := publishAndReleaseInteraction(context.Background(), recordingInteractionPublisher{events: &events}, recordingInteractionStore{events: &events}, response); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0] != "source-current" || events[1] != "prepared" || events[2] != "source-fenced" || events[3] != "published" {
		t.Fatalf("source publication ordering=%v", events)
	}
	events = nil
	if err := publishAndReleaseInteraction(context.Background(), recordingInteractionPublisher{events: &events}, recordingInteractionStore{events: &events, skipSource: true}, response); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "source-current" {
		t.Fatalf("superseded source status reached provider: %v", events)
	}
}

func TestResponseFromPayloadRejectsInvalidMarkerBoundary(t *testing.T) {
	_, err := responseFromPayload(map[string]any{
		"provider": "github", "api_base_url": "https://api.github.com", "installation_external_id": "42", "credential_ref": "github-app",
		"repository": "RainLib/demo", "review_number": float64(4), "comment_external_id": "99", "body": "Review is queued.",
		"marker": "open-review-platform:interaction:test", "marker_since": "not-a-timestamp",
	})
	if err == nil {
		t.Fatal("invalid marker boundary must be rejected")
	}
}

func TestResponseFromPayloadRejectsInvalidAcknowledgementBarrier(t *testing.T) {
	_, err := responseFromPayload(map[string]any{
		"provider":                 "github",
		"api_base_url":             "https://api.github.com",
		"installation_external_id": "42",
		"credential_ref":           "github-app",
		"repository":               "RainLib/demo",
		"review_number":            float64(4),
		"comment_external_id":      "99",
		"reaction":                 "eyes",
		"release_run_id":           "not-a-uuid",
		"body":                     "Review is queued.",
		"marker":                   "open-review-platform:interaction:test",
	})
	if err == nil {
		t.Fatal("expected invalid release run id to be rejected")
	}
}

func TestResponseFromPayloadRequiresTenantForGitLabOAuthCredential(t *testing.T) {
	payload := map[string]any{
		"provider": "gitlab", "api_base_url": "https://gitlab.example/api/v4", "installation_external_id": "42",
		"credential_ref": "secret://provider/gitlab-oauth/11111111-1111-1111-1111-111111111111",
		"repository":     "acme/demo", "review_number": float64(4), "body": "Review is queued.",
		"marker": "open-review-platform:interaction:test",
	}
	if _, err := responseFromPayload(payload); err == nil {
		t.Fatal("GitLab OAuth response without tenant boundary was accepted")
	}
	tenantID := uuid.New()
	payload["tenant_id"] = tenantID.String()
	response, err := responseFromPayload(payload)
	if err != nil || response.TenantID != tenantID {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestResponseFromPayloadCarriesInitialAdmissionOnlyAfterPublishing(t *testing.T) {
	interactionID := uuid.New()
	ruleSetID := uuid.New()
	response, err := responseFromPayload(map[string]any{
		"provider": "gitlab", "api_base_url": "https://gitlab.example/api/v4", "installation_external_id": "42", "credential_ref": "gitlab-token",
		"repository": "acme/demo", "review_number": float64(4), "comment_external_id": "note-9", "reaction": "",
		"body": "Review request acknowledged.", "marker": "open-review-platform:interaction:test",
		"admission_interaction_id": interactionID.String(), "admission_clone_url": "https://gitlab.example/acme/demo.git",
		"admission_actor_external_id": "99", "admission_actor_subject": "reviewer", "admission_mode": "deep", "admission_rule_set_id": ruleSetID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Admission == nil || response.Admission.InteractionID != interactionID || response.Admission.Event.Provider != domain.ProviderGitLab || response.Admission.Event.CloneURL == "" || response.Admission.Mode != domain.ReviewModeDeep || response.Admission.RuleSetID == nil || *response.Admission.RuleSetID != ruleSetID {
		t.Fatalf("unexpected admission response: %#v", response)
	}
}

func TestPublishAndReleaseInteractionDoesNotReleaseWhenProviderPublishFails(t *testing.T) {
	events := make([]string, 0, 2)
	admission := domain.InteractionAdmission{InteractionID: uuid.New(), Event: domain.CommentEvent{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", InstallationExternalID: "42", Repository: "RainLib/demo", CloneURL: "https://github.com/RainLib/demo.git", ReviewNumber: 4, CommentExternalID: "99", ActorExternalID: "77"}, Mode: domain.ReviewModeConfigured, ActorSubject: "reviewer", CredentialRef: "github-app"}
	err := publishAndReleaseInteraction(context.Background(), recordingInteractionPublisher{events: &events, err: errors.New("provider unavailable")}, recordingInteractionStore{events: &events}, domain.InteractionResponse{Admission: &admission})
	if err == nil || len(events) != 1 || events[0] != "published" {
		t.Fatalf("failed publication must not release work, events=%v error=%v", events, err)
	}
}

func TestPublishAndReleaseInteractionReleasesOnlyAfterProviderPublication(t *testing.T) {
	events := make([]string, 0, 2)
	admission := domain.InteractionAdmission{InteractionID: uuid.New(), Event: domain.CommentEvent{Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4", InstallationExternalID: "42", Repository: "acme/demo", CloneURL: "https://gitlab.example/acme/demo.git", ReviewNumber: 4, CommentExternalID: "note-1", ActorExternalID: "77"}, Mode: domain.ReviewModeDeep, ActorSubject: "reviewer", CredentialRef: "gitlab-token"}
	if err := publishAndReleaseInteraction(context.Background(), recordingInteractionPublisher{events: &events}, recordingInteractionStore{events: &events}, domain.InteractionResponse{Admission: &admission}); err != nil {
		t.Fatal(err)
	}
	if got, want := len(events), 2; got != want || events[0] != "published" || events[1] != "admission-released" {
		t.Fatalf("admission release order=%v", events)
	}
}
