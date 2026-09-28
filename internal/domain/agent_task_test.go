package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentTaskPlanSectionsFreezeOneCanonicalApprovalSummary(t *testing.T) {
	sections := AgentTaskPlanSections{
		Objective:    "  Correct the reported retry behavior without changing unrelated flows.  ",
		Scope:        "Only the review worker and its focused tests.",
		Verification: "Run focused tests and the full Go suite.",
		Risks:        "Low; no data migration.",
		Unknowns:     "None after source review.",
	}
	input := AgentTaskPlanInput{Sections: &sections}
	if !input.Valid() {
		t.Fatal("bounded structured plan should be valid")
	}
	if input.CanonicalSummary() != sections.Normalized().Summary() || strings.Contains(input.CanonicalSummary(), "  Correct") {
		t.Fatal("canonical summary must normalize the human-visible sections")
	}
	input.Summary = "A different instruction for the coding Agent."
	if input.Valid() {
		t.Fatal("structured sections and adapter summary cannot disagree")
	}
	input.Summary = ""
	sections.Verification = ""
	if input.Valid() {
		t.Fatal("verification boundary cannot be omitted")
	}
	legacy := AgentTaskPlanInput{Summary: "A previously stored free-text plan remains readable and approvable."}
	if !legacy.Valid() {
		t.Fatal("legacy API clients must remain compatible")
	}
}

func TestAgentTaskInputRequiresBoundedProviderOrigin(t *testing.T) {
	valid := AgentTaskInput{
		Provider: ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "RainLib/open-review-platform",
		OriginKind: "issue", OriginNumber: 7, OriginRevision: "issue-revision", Intent: "implement",
	}
	if !valid.Valid() {
		t.Fatal("expected bounded provider origin to be valid")
	}
	for name, input := range map[string]AgentTaskInput{
		"missing origin revision": func() AgentTaskInput { copy := valid; copy.OriginRevision = ""; return copy }(),
		"invalid origin kind":     func() AgentTaskInput { copy := valid; copy.OriginKind = "comment"; return copy }(),
		"direct pull request":     func() AgentTaskInput { copy := valid; copy.OriginKind = "pull_request"; return copy }(),
		"arbitrary intent":        func() AgentTaskInput { copy := valid; copy.Intent = "shell"; return copy }(),
		"missing repository":      func() AgentTaskInput { copy := valid; copy.Repository = " "; return copy }(),
	} {
		if input.Valid() {
			t.Fatalf("%s must be invalid", name)
		}
	}
}

func TestAgentTaskPolicyHasNoAutomaticExecutionMode(t *testing.T) {
	valid := AgentTaskPolicyInput{Provider: ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4", Repository: "platform/review", Mode: "manual", MaxAttempts: 1, MaxExecutionSeconds: 1800, ExecutorProfile: "codex", AutoAdmissionLabel: "openreview:implement"}
	if !valid.Valid() {
		t.Fatal("manual policy should be valid")
	}
	for _, mode := range []string{"disabled", "suggest"} {
		copy := valid
		copy.Mode = mode
		if !copy.Valid() {
			t.Fatalf("%s policy should be valid", mode)
		}
	}
	copy := valid
	copy.Mode = "auto"
	if copy.Valid() {
		t.Fatal("automatic execution mode must not exist in P0")
	}
	copy = valid
	copy.MaxAttempts = 4
	if copy.Valid() {
		t.Fatal("more than three execution attempts must be rejected")
	}
	copy = valid
	copy.MaxExecutionSeconds = 7201
	if copy.Valid() {
		t.Fatal("an unbounded execution time must be rejected")
	}
	copy = valid
	copy.ExecutorProfile = "claude"
	if !copy.Valid() {
		t.Fatal("a pinned Claude executor policy should be valid")
	}
	copy = valid
	copy.ExecutorProfile = "unreviewed-agent"
	if copy.Valid() {
		t.Fatal("an unknown executor profile must be rejected")
	}
	copy = valid
	copy.AutoAdmissionLabel = "\n"
	if copy.Valid() {
		t.Fatal("an empty automatic-admission label must be rejected")
	}
}

func TestAgentTaskAdapterEventKeepsDraftPROutOfFailureStates(t *testing.T) {
	base := AgentTaskAdapterEvent{AttemptID: uuid.New(), AdapterJobID: "adapter-job", DeliveryID: "delivery", Kind: "failed", Summary: "sandbox stopped"}
	if !base.Valid() {
		t.Fatal("expected bounded failure event to be valid")
	}
	base.PullRequestURL = "https://github.com/RainLib/open-review-platform/pull/1"
	if base.Valid() {
		t.Fatal("failed event must not carry draft PR evidence")
	}
	base.Kind = "completed"
	base.BranchName = "agent/" + uuid.NewString()
	base.HeadSHA = "deadbee"
	base.PullRequestNumber = 1
	if !base.Valid() {
		t.Fatal("completed event may carry draft PR evidence before store validates its exact branch")
	}
	base.PatchSHA256 = strings.Repeat("a", 64)
	base.ChangedFileCount = 2
	base.DiffBytes = 512
	if !base.Valid() {
		t.Fatal("completed event rejected bounded patch evidence")
	}
	base.PatchSHA256 = "not-a-digest"
	if base.Valid() {
		t.Fatal("completed event accepted an invalid patch digest")
	}
	base.PatchSHA256 = strings.Repeat("a", 64)
	base.ErrorCode = "not-allowed"
	if base.Valid() {
		t.Fatal("completed event must not carry an error code")
	}
}

func TestAgentPublicationCheckpointIsPrePushEvidenceOnly(t *testing.T) {
	event := AgentTaskAdapterEvent{
		AttemptID: uuid.New(), AdapterJobID: "adapter-job", DeliveryID: "checkpoint-1",
		Kind: "publication_checkpoint", BranchName: "agent/approved-task",
		HeadSHA: strings.Repeat("a", 40), PatchSHA256: strings.Repeat("b", 64),
		ChangedFileCount: 2, DiffBytes: 128,
	}
	if !event.Valid() {
		t.Fatal("bounded pre-push checkpoint was rejected")
	}
	for _, mutate := range []func(*AgentTaskAdapterEvent){
		func(value *AgentTaskAdapterEvent) { value.PullRequestNumber = 7 },
		func(value *AgentTaskAdapterEvent) { value.PullRequestURL = "https://github.com/acme/api/pull/7" },
		func(value *AgentTaskAdapterEvent) { value.Summary = "published" },
		func(value *AgentTaskAdapterEvent) { value.HeadSHA = "deadbee" },
		func(value *AgentTaskAdapterEvent) { value.PatchSHA256 = strings.Repeat("A", 64) },
		func(value *AgentTaskAdapterEvent) { value.DiffBytes = 0 },
	} {
		invalid := event
		mutate(&invalid)
		if invalid.Valid() {
			t.Fatalf("checkpoint accepted terminal or malformed evidence: %+v", invalid)
		}
	}
}

func TestAgentVerificationEvidenceIsBoundedAndOnlyAllowedOnPublication(t *testing.T) {
	checkpoint := AgentTaskAdapterEvent{
		AttemptID: uuid.New(), AdapterJobID: "adapter-job", DeliveryID: "checkpoint-verification",
		Kind: "publication_checkpoint", BranchName: "agent/approved-task",
		HeadSHA: strings.Repeat("a", 40), PatchSHA256: strings.Repeat("b", 64),
		ChangedFileCount: 1, DiffBytes: 128,
		VerificationProfileSHA256: strings.Repeat("c", 64), VerificationOutputSHA256: strings.Repeat("d", 64), VerificationOutputBytes: 42,
	}
	if !checkpoint.Valid() {
		t.Fatal("bounded verification checkpoint was rejected")
	}
	for _, mutate := range []func(*AgentTaskAdapterEvent){
		func(value *AgentTaskAdapterEvent) { value.VerificationOutputSHA256 = "" },
		func(value *AgentTaskAdapterEvent) { value.VerificationProfileSHA256 = strings.Repeat("A", 64) },
		func(value *AgentTaskAdapterEvent) { value.VerificationOutputBytes = (1 << 20) + 1 },
	} {
		invalid := checkpoint
		mutate(&invalid)
		if invalid.Valid() {
			t.Fatalf("malformed verification evidence was accepted: %+v", invalid)
		}
	}
	heartbeat := checkpoint
	heartbeat.Kind = "heartbeat"
	heartbeat.BranchName, heartbeat.HeadSHA, heartbeat.PatchSHA256 = "", "", ""
	heartbeat.ChangedFileCount, heartbeat.DiffBytes = 0, 0
	if heartbeat.Valid() {
		t.Fatal("heartbeat carried a verification success claim")
	}
}
