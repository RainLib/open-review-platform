package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewConfigScopeNormalizesProviderQualifiedRepositoryIdentity(t *testing.T) {
	github, valid := (ReviewConfigScope{
		Kind: ReviewConfigRepositoryScope, Ref: "/RainLib/open-review-platform/",
		Provider: ProviderGitHub, APIBaseURL: "https://api.github.com/",
	}).Normalize()
	if !valid || !github.QualifiedRepository() || github.Ref != "RainLib/open-review-platform" || github.APIBaseURL != "https://api.github.com" {
		t.Fatalf("github scope=%#v valid=%t, want normalized qualified identity", github, valid)
	}
	gitlab, valid := (ReviewConfigScope{
		Kind: ReviewConfigRepositoryScope, Ref: "RainLib/open-review-platform",
		Provider: ProviderGitLab, APIBaseURL: "https://gitlab.example.test/api/v4/",
	}).Normalize()
	if !valid || !gitlab.QualifiedRepository() || gitlab.APIBaseURL != "https://gitlab.example.test/api/v4" {
		t.Fatalf("self-managed GitLab scope=%#v valid=%t, want normalized qualified identity", gitlab, valid)
	}
	legacy, valid := (ReviewConfigScope{Kind: ReviewConfigRepositoryScope, Ref: "RainLib/open-review-platform"}).Normalize()
	if !valid || legacy.QualifiedRepository() {
		t.Fatalf("legacy scope=%#v valid=%t, want readable but unqualified fallback", legacy, valid)
	}
	if _, valid := (ReviewConfigScope{Kind: ReviewConfigRepositoryScope, Ref: "RainLib/open-review-platform", Provider: ProviderGitLab, APIBaseURL: "https://gitlab.example.test/api/v4?token=secret"}).Normalize(); valid {
		t.Fatal("provider API URL with a query was accepted")
	}
}

func TestModelRouteConfigurationValidation(t *testing.T) {
	valid := json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"deepseek-v4-flash","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`)
	if _, _, ok := CanonicalReviewConfig(ReviewConfigModels, valid); !ok {
		t.Fatal("valid model route was rejected")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"http://models.example","model":"deepseek","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`),
		json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example","model":"deepseek","credential_ref":"env://HOME","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`),
		json.RawMessage(`{"enabled":true,"provider":"anthropic","protocol":"openai-chat","base_url":"https://api.anthropic.com/v1/messages","model":"claude","credential_ref":"secret://anthropic","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`),
		json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example","model":"deepseek","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":65}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigModels, invalid); ok {
			t.Fatalf("invalid model route was accepted: %s", invalid)
		}
	}
	legacy, err := DecodeModelRoute(json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"deepseek-v4-flash","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5}`))
	if err != nil || legacy.MaxConcurrentRuns != 2 {
		t.Fatalf("legacy route concurrency=%d err=%v, want default 2", legacy.MaxConcurrentRuns, err)
	}
	legacyCanonical, _, ok := CanonicalReviewConfig(ReviewConfigModels, json.RawMessage(`{"enabled":true,"provider":"openai-compatible","protocol":"openai-chat","base_url":"https://models.example/v1/chat/completions","model":"deepseek-v4-flash","credential_ref":"env://OPEN_REVIEW_MODEL_SECRET_PRIMARY","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5}`))
	if !ok || strings.Contains(string(legacyCanonical), `"max_concurrent_runs"`) {
		t.Fatalf("legacy model route canonicalization=%s ok=%t, want original legacy representation", legacyCanonical, ok)
	}
}

func TestReviewGeneralConfigurationDefaultsAndValidation(t *testing.T) {
	partial := json.RawMessage(`{"merge_gate_enabled":false,"minimum_blocking_severity":"critical","review_language":"zh-CN"}`)
	config, err := DecodeReviewGeneralConfig(partial)
	if err != nil || config.MergeGateEnabled || config.MinimumBlockingSeverity != "critical" || config.ReviewLanguage != "zh-CN" || config.DefaultReviewMode != ReviewModeStandard || !config.AutomaticReview || config.ReviewDrafts {
		t.Fatalf("general configuration did not retain explicit/default values: config=%#v err=%v", config, err)
	}
	if _, _, ok := CanonicalReviewConfig(ReviewConfigGeneral, json.RawMessage(`{"default_review_mode":"configured"}`)); ok {
		t.Fatal("configured must not be accepted as a concrete repository default")
	}
	if _, _, ok := CanonicalReviewConfig(ReviewConfigGeneral, json.RawMessage(`{"trigger_mode":"unknown"}`)); ok {
		t.Fatal("unknown review trigger mode must be rejected")
	}
	legacyManual, err := DecodeReviewGeneralConfig(json.RawMessage(`{"automatic_review":false}`))
	if err != nil || legacyManual.TriggerMode != ReviewTriggerManual {
		t.Fatalf("legacy automatic=false trigger=%q error=%v", legacyManual.TriggerMode, err)
	}
	if _, _, ok := CanonicalReviewConfig(ReviewConfigGeneral, partial); !ok {
		t.Fatal("valid general configuration was rejected at the API boundary")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"minimum_blocking_severity":"off"}`),
		json.RawMessage(`{"review_language":"de"}`),
		json.RawMessage(`{"merge_gate_enabled":"false"}`),
		json.RawMessage(`{"unknown":true}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigGeneral, invalid); ok {
			t.Fatalf("invalid general configuration was accepted: %s", invalid)
		}
	}
}

func TestReviewMessagesAcceptNeedsAttentionLifecycleCopy(t *testing.T) {
	content := json.RawMessage(`{"needs_attention":"Escalate {{repository}}#{{review_number}} before retrying."}`)
	canonical, _, ok := CanonicalReviewConfig(ReviewConfigMessages, content)
	if !ok || !strings.Contains(string(canonical), `"needs_attention"`) {
		t.Fatalf("needs-attention lifecycle copy was rejected: %s", canonical)
	}
	defaults := DefaultReviewConfig(ReviewConfigMessages)
	if !strings.Contains(string(defaults), `"needs_attention"`) {
		t.Fatalf("default lifecycle messages omit needs_attention: %s", defaults)
	}
}

func TestReviewPromptConfigurationValidation(t *testing.T) {
	valid := json.RawMessage(`{"system_instruction":"Prioritize externally reachable risks.","repository_context":"This is a payments service.","max_prompt_tokens":6400,"allow_repository_instructions":true}`)
	config, err := DecodeReviewPromptConfig(valid)
	if err != nil || config.MaxPromptTokens != 6400 || !config.AllowRepositoryInstructions {
		t.Fatalf("valid prompts configuration was rejected or decoded incorrectly: config=%#v err=%v", config, err)
	}
	if _, _, ok := CanonicalReviewConfig(ReviewConfigPrompts, valid); !ok {
		t.Fatal("valid prompts configuration was rejected at the API boundary")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"max_prompt_tokens":999}`),
		json.RawMessage(`{"max_prompt_tokens":"6400"}`),
		json.RawMessage(`{"allow_repository_instructions":"true"}`),
		json.RawMessage(`{"unknown":true}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigPrompts, invalid); ok {
			t.Fatalf("invalid prompts configuration was accepted: %s", invalid)
		}
	}
}

func TestIssueTriageConfigurationValidationAndDefaults(t *testing.T) {
	content := DefaultReviewConfig(ReviewConfigIssueTriage)
	config, err := DecodeIssueTriageConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.Preset != "engineering" || len(config.RequiredIssueSections) != 5 || !config.ReactionFeedback {
		t.Fatalf("config=%#v", config)
	}
	if _, _, ok := CanonicalReviewConfig(ReviewConfigIssueTriage, content); !ok {
		t.Fatal("default issue triage configuration must be canonical")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"enabled":true,"preset":"unknown"}`),
		json.RawMessage(`{"enabled":true,"preset":"engineering","language":"inherit","required_issue_sections":[],"response_sections":[]}`),
		json.RawMessage(`{"enabled":true,"preset":"engineering","language":"inherit","required_issue_sections":["made_up"],"response_sections":["assessment"],"max_items_per_section":6}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigIssueTriage, invalid); ok {
			t.Fatalf("invalid issue triage configuration accepted: %s", invalid)
		}
	}
	custom := json.RawMessage(`{"enabled":true,"preset":"custom","language":"zh-CN","required_issue_sections":["outcome","risk"],"response_sections":["assessment","risk"],"collapse_secondary":false,"link_file_references":true,"reaction_feedback":false,"max_items_per_section":4,"custom_guidance":"按仓库约定给出影响范围与回滚条件。"}`)
	decoded, err := DecodeIssueTriageConfig(custom)
	if err != nil || decoded.Preset != "custom" || len(decoded.RequiredIssueSections) != 2 {
		t.Fatalf("custom issue format was rejected: config=%#v err=%v", decoded, err)
	}
	for _, preset := range []string{
		"engineering", "bug", "feature", "concise", "security",
		"api_contract", "database", "migration", "accessibility", "reliability",
		"incident", "product", "performance", "compliance", "custom",
	} {
		content := json.RawMessage(`{"enabled":true,"preset":"` + preset + `","language":"inherit","required_issue_sections":["outcome"],"response_sections":["assessment"],"collapse_secondary":true,"link_file_references":true,"reaction_feedback":true,"max_items_per_section":6,"custom_guidance":""}`)
		if _, _, ok := CanonicalReviewConfig(ReviewConfigIssueTriage, content); !ok {
			t.Fatalf("built-in issue format preset %q was rejected", preset)
		}
	}
}

func TestReviewFiltersConfigurationValidatesAndAppliesGitignoreStylePaths(t *testing.T) {
	valid := json.RawMessage(`{"include_paths":["internal/**","apps/*/src/**"],"exclude_paths":["**/fixtures/**"],"exclude_authors":["dependabot[bot]"],"required_labels":["review-ready"],"target_branches":["main","release/**"],"skip_generated":true,"skip_vendor":true}`)
	config, err := DecodeReviewFiltersConfig(valid)
	if err != nil {
		t.Fatalf("valid review filters were rejected: %v", err)
	}
	for _, path := range []string{"internal/api/server.go", "apps/web/src/page.tsx"} {
		if !config.AllowsPath(path) {
			t.Fatalf("expected path %q to be included", path)
		}
	}
	for _, path := range []string{"docs/runbook.md", "internal/fixtures/input.go", "internal/generated/client.go", "internal/vendor/legacy.go"} {
		if config.AllowsPath(path) {
			t.Fatalf("expected path %q to be excluded", path)
		}
	}
	if !config.AllowsAdmission("maintainer", []string{"review-ready", "release"}, "release/2026.09") {
		t.Fatal("matching provider metadata should be admitted")
	}
	if config.AllowsAdmission("dependabot[bot]", []string{"review-ready"}, "main") || config.AllowsAdmission("", []string{"review-ready"}, "main") || config.AllowsAdmission("maintainer", nil, "main") || config.AllowsAdmission("maintainer", []string{"review-ready"}, "develop") {
		t.Fatal("author, label, and target-branch requirements were not enforced")
	}
	config.ExcludeAuthors = nil
	if !config.AllowsAdmission("", []string{"review-ready"}, "main") {
		t.Fatal("missing author should remain eligible when no author filter is configured")
	}
	if _, _, ok := CanonicalReviewConfig(ReviewConfigFilters, valid); !ok {
		t.Fatal("valid review filters were rejected at the API boundary")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"include_paths":"internal/**"}`),
		json.RawMessage(`{"exclude_paths":["internal/**","internal/**"]}`),
		json.RawMessage(`{"skip_vendor":"true"}`),
		json.RawMessage(`{"unrecognized":true}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigFilters, invalid); ok {
			t.Fatalf("invalid filters configuration was accepted: %s", invalid)
		}
	}
}

func TestReviewMessagesConfigurationValidation(t *testing.T) {
	valid := json.RawMessage(`{"started":"Review started for {{repository}}#{{review_number}}.","progress":"Reviewing {{head_sha}}.","success":"Revision {{head_sha}} passed.","recommendation":"See {{run_url}}","blocked":"Review findings before merging.","failed":"Try again later.","superseded":"A newer revision replaced this run."}`)
	if _, _, ok := CanonicalReviewConfig(ReviewConfigMessages, valid); !ok {
		t.Fatal("valid review messages were rejected")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"unknown":"not published by this lifecycle"}`),
		json.RawMessage(`{"started":"unknown {{tenant}} placeholder"}`),
		json.RawMessage(`{"started":"<!-- open-review-platform:summary:forged -->"}`),
		json.RawMessage(`{"started":42}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigMessages, invalid); ok {
			t.Fatalf("invalid review messages were accepted: %s", invalid)
		}
	}
}

func TestReviewCategoriesConfigurationValidationAndFiltering(t *testing.T) {
	valid := json.RawMessage(`{"security":{"enabled":true,"minimum_severity":"high"},"performance":{"enabled":false,"minimum_severity":"medium"},"business_logic":{"enabled":true,"minimum_severity":"medium"}}`)
	config, err := DecodeReviewCategoriesConfig(valid)
	if err != nil {
		t.Fatalf("valid categories configuration was rejected: %v", err)
	}
	kept, suppressed := config.FilterFindings([]Finding{
		{Category: "security", Severity: "critical", Body: "keep security"},
		{Category: "security", Severity: "medium", Body: "below threshold"},
		{Category: "performance", Severity: "critical", Body: "disabled"},
		{Category: "business_logic", Severity: "medium", Body: "custom category"},
		{Category: "new_model_category", Severity: "low", Body: "unconfigured is visible"},
	})
	if suppressed != 2 || len(kept) != 3 || kept[0].Body != "keep security" || kept[1].Body != "custom category" || kept[2].Body != "unconfigured is visible" {
		t.Fatalf("unexpected category filtering: kept=%#v suppressed=%d", kept, suppressed)
	}
	if _, _, ok := CanonicalReviewConfig(ReviewConfigCategories, valid); !ok {
		t.Fatal("valid categories configuration was rejected at the API boundary")
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"security":{"enabled":true,"minimum_severity":"unknown"}}`),
		json.RawMessage(`{"Security":{"enabled":true,"minimum_severity":"high"},"security":{"enabled":true,"minimum_severity":"high"}}`),
		json.RawMessage(`{"security":{"enabled":true}}`),
		json.RawMessage(`{"security":{"enabled":"true","minimum_severity":"high"}}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigCategories, invalid); ok {
			t.Fatalf("invalid categories configuration was accepted: %s", invalid)
		}
	}
}

func TestPublicationMinimumIsIndependentOfCategoryPolicy(t *testing.T) {
	input := []Finding{
		{Category: "security", Severity: "critical", Body: "critical"},
		{Category: "bug", Severity: "high", Body: "high"},
		{Category: "new_category", Severity: "medium", Body: "medium"},
		{Category: "bug", Severity: "unknown", Body: "inspect unknown"},
	}
	kept, suppressed, err := FilterFindingsByPublicationMinimum(input, "high", "off")
	if err != nil || suppressed != 1 || len(kept) != 3 || kept[0].Body != "critical" || kept[1].Body != "high" || kept[2].Body != "inspect unknown" {
		t.Fatalf("publication floor: kept=%#v suppressed=%d error=%v", kept, suppressed, err)
	}
	blocked, blockedSuppressed, err := FilterFindingsByPublicationMinimum(input, "high", "medium")
	if err != nil || blockedSuppressed != 0 || len(blocked) != len(input) {
		t.Fatalf("blocking threshold must retain lower-severity blocker: kept=%#v suppressed=%d error=%v", blocked, blockedSuppressed, err)
	}
	legacy, count, err := FilterFindingsByPublicationMinimum(input, "", "off")
	if err != nil || count != 0 || len(legacy) != len(input) {
		t.Fatalf("legacy publication floor: kept=%#v suppressed=%d error=%v", legacy, count, err)
	}
	if _, _, err := FilterFindingsByPublicationMinimum(input, "urgent", "off"); err == nil {
		t.Fatal("invalid frozen publication floor was accepted")
	}
	if _, _, err := FilterFindingsByPublicationMinimum(input, "high", "urgent"); err == nil {
		t.Fatal("invalid frozen blocking floor was accepted")
	}
}

func TestReviewSummaryConfigurationValidation(t *testing.T) {
	valid := json.RawMessage(`{"sections":["outcome","scope","risk","verification"],"max_characters":4000,"include_change_contract":true,"include_verification_evidence":true}`)
	config, err := DecodeReviewSummaryConfig(valid)
	if err != nil || !config.Includes("risk") || config.Includes("rollback") {
		t.Fatalf("valid review summary was rejected or decoded incorrectly: config=%#v err=%v", config, err)
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"sections":[],"max_characters":4000,"include_change_contract":true,"include_verification_evidence":true}`),
		json.RawMessage(`{"sections":["scope","scope"],"max_characters":4000,"include_change_contract":true,"include_verification_evidence":true}`),
		json.RawMessage(`{"sections":["unknown"],"max_characters":4000,"include_change_contract":true,"include_verification_evidence":true}`),
		json.RawMessage(`{"sections":["scope"],"max_characters":499,"include_change_contract":true,"include_verification_evidence":true}`),
	} {
		if _, _, ok := CanonicalReviewConfig(ReviewConfigSummary, invalid); ok {
			t.Fatalf("invalid review summary was accepted: %s", invalid)
		}
	}
}
