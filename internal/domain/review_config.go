package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type ReviewConfigSection string

const (
	ReviewConfigGeneral     ReviewConfigSection = "general"
	ReviewConfigCategories  ReviewConfigSection = "categories"
	ReviewConfigFilters     ReviewConfigSection = "filters"
	ReviewConfigPrompts     ReviewConfigSection = "prompts"
	ReviewConfigSummary     ReviewConfigSection = "summary"
	ReviewConfigMessages    ReviewConfigSection = "messages"
	ReviewConfigModels      ReviewConfigSection = "models"
	ReviewConfigIssueTriage ReviewConfigSection = "issue-triage"
)

func (section ReviewConfigSection) Valid() bool {
	return section == ReviewConfigGeneral || section == ReviewConfigCategories || section == ReviewConfigFilters || section == ReviewConfigPrompts || section == ReviewConfigSummary || section == ReviewConfigMessages || section == ReviewConfigModels || section == ReviewConfigIssueTriage
}

type ReviewConfigScopeKind string

const (
	ReviewConfigTenantScope     ReviewConfigScopeKind = "tenant"
	ReviewConfigRepositoryScope ReviewConfigScopeKind = "repository"
)

// ReviewConfigScope identifies a versioned policy target. A repository name
// is not globally unique in a multi-provider workspace: GitHub, GitLab.com,
// and a self-managed GitLab instance can each expose the same namespace.
// Provider and API base URL therefore form part of every new repository
// override's identity. Empty provider metadata is retained solely to read
// configurations created before provider-qualified scopes were introduced.
type ReviewConfigScope struct {
	Kind       ReviewConfigScopeKind `json:"scope_kind"`
	Ref        string                `json:"scope_ref,omitempty"`
	Provider   Provider              `json:"scope_provider,omitempty"`
	APIBaseURL string                `json:"scope_api_base_url,omitempty"`
}

func (scope ReviewConfigScope) Normalize() (ReviewConfigScope, bool) {
	scope.Ref = strings.Trim(strings.TrimSpace(scope.Ref), "/")
	scope.APIBaseURL = strings.TrimSuffix(strings.TrimSpace(scope.APIBaseURL), "/")
	if scope.Kind == ReviewConfigTenantScope {
		return scope, scope.Ref == "" && scope.Provider == "" && scope.APIBaseURL == ""
	}
	if scope.Kind != ReviewConfigRepositoryScope || scope.Ref == "" || len(scope.Ref) > 255 {
		return ReviewConfigScope{}, false
	}
	// Legacy repository policy remains readable during migration. New writes
	// explicitly reject it at the store boundary.
	if scope.Provider == "" && scope.APIBaseURL == "" {
		return scope, true
	}
	if !scope.Provider.Valid() || scope.APIBaseURL == "" {
		return ReviewConfigScope{}, false
	}
	parsed, err := url.Parse(scope.APIBaseURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return ReviewConfigScope{}, false
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawPath = ""
	scope.APIBaseURL = parsed.String()
	return scope, true
}

func (scope ReviewConfigScope) QualifiedRepository() bool {
	return scope.Kind == ReviewConfigRepositoryScope && scope.Provider.Valid() && scope.APIBaseURL != ""
}

func (scope ReviewConfigScope) Valid() bool {
	_, valid := scope.Normalize()
	return valid
}

// Valid is kept for established call sites that only need to validate legacy
// tenant/repository selectors. New configuration writes must use
// ReviewConfigScope.QualifiedRepository for repository targets.
func (scope ReviewConfigScopeKind) Valid(ref string) bool {
	return (ReviewConfigScope{Kind: scope, Ref: ref}).Valid()
}

type ReviewConfigView struct {
	Section             ReviewConfigSection   `json:"section"`
	RequestedScopeKind  ReviewConfigScopeKind `json:"requested_scope_kind"`
	RequestedScopeRef   string                `json:"requested_scope_ref,omitempty"`
	RequestedProvider   Provider              `json:"requested_scope_provider,omitempty"`
	RequestedAPIBaseURL string                `json:"requested_scope_api_base_url,omitempty"`
	OriginScopeKind     string                `json:"origin_scope_kind"`
	OriginScopeRef      string                `json:"origin_scope_ref,omitempty"`
	OriginProvider      Provider              `json:"origin_scope_provider,omitempty"`
	OriginAPIBaseURL    string                `json:"origin_scope_api_base_url,omitempty"`
	Inherited           bool                  `json:"inherited"`
	Revision            int                   `json:"revision"`
	ContentSHA256       string                `json:"content_sha256"`
	Content             json.RawMessage       `json:"content"`
	UpdatedBy           string                `json:"updated_by,omitempty"`
	UpdatedAt           *time.Time            `json:"updated_at,omitempty"`
}

// ReviewConfigVersion is immutable provenance for a persisted configuration.
// Content is intentionally omitted: a version timeline proves governance
// changes without broadening the read surface for model route metadata.
type ReviewConfigVersion struct {
	Revision      int       `json:"revision"`
	ContentSHA256 string    `json:"content_sha256"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

type ReviewConfigHistory struct {
	RequestedScopeKind  ReviewConfigScopeKind `json:"requested_scope_kind"`
	RequestedScopeRef   string                `json:"requested_scope_ref,omitempty"`
	RequestedProvider   Provider              `json:"requested_scope_provider,omitempty"`
	RequestedAPIBaseURL string                `json:"requested_scope_api_base_url,omitempty"`
	OriginScopeKind     string                `json:"origin_scope_kind"`
	OriginScopeRef      string                `json:"origin_scope_ref,omitempty"`
	OriginProvider      Provider              `json:"origin_scope_provider,omitempty"`
	OriginAPIBaseURL    string                `json:"origin_scope_api_base_url,omitempty"`
	Inherited           bool                  `json:"inherited"`
	Versions            []ReviewConfigVersion `json:"versions"`
}

// ReviewConfigSnapshot is the immutable, run-scoped configuration provenance
// resolved inside the same transaction that admits a review.
type ReviewConfigSnapshot struct {
	Section          ReviewConfigSection `json:"section"`
	OriginScopeKind  string              `json:"origin_scope_kind"`
	OriginScopeRef   string              `json:"origin_scope_ref,omitempty"`
	OriginProvider   Provider            `json:"origin_scope_provider,omitempty"`
	OriginAPIBaseURL string              `json:"origin_scope_api_base_url,omitempty"`
	OriginRevision   int                 `json:"origin_revision"`
	ContentSHA256    string              `json:"content_sha256"`
	Content          json.RawMessage     `json:"content"`
	CreatedAt        time.Time           `json:"created_at"`
}

func ReviewConfigSections() []ReviewConfigSection {
	return []ReviewConfigSection{
		ReviewConfigGeneral,
		ReviewConfigCategories,
		ReviewConfigFilters,
		ReviewConfigPrompts,
		ReviewConfigSummary,
		ReviewConfigMessages,
		ReviewConfigModels,
		ReviewConfigIssueTriage,
	}
}

type ReviewConfigInput struct {
	Section          ReviewConfigSection
	ScopeKind        ReviewConfigScopeKind
	ScopeRef         string
	ScopeProvider    Provider
	ScopeAPIBaseURL  string
	ExpectedRevision int
	Content          json.RawMessage
}

func (input ReviewConfigInput) Scope() ReviewConfigScope {
	return ReviewConfigScope{
		Kind: input.ScopeKind, Ref: input.ScopeRef,
		Provider: input.ScopeProvider, APIBaseURL: input.ScopeAPIBaseURL,
	}
}

// ReviewTriggerMode is the repository-level admission contract. Manual keeps
// explicit CLI and comment requests available, while off disables new review
// work at every entry point for that repository.
type ReviewTriggerMode string

const (
	ReviewTriggerOff       ReviewTriggerMode = "off"
	ReviewTriggerManual    ReviewTriggerMode = "manual"
	ReviewTriggerAutomatic ReviewTriggerMode = "automatic"
)

func (mode ReviewTriggerMode) Valid() bool {
	return mode == ReviewTriggerOff || mode == ReviewTriggerManual || mode == ReviewTriggerAutomatic
}

func CanonicalReviewConfig(section ReviewConfigSection, content json.RawMessage) (json.RawMessage, string, bool) {
	if !section.Valid() || len(content) < 2 || len(content) > 64*1024 {
		return nil, "", false
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil || value == nil {
		return nil, "", false
	}
	if section == ReviewConfigModels {
		// DecodeModelRoute owns compatibility defaults. Validate through it, but
		// preserve the submitted representation: old immutable snapshots that do
		// not have a concurrency key must remain auditable as they were stored.
		if _, err := DecodeModelRoute(content); err != nil {
			return nil, "", false
		}
	}
	if section == ReviewConfigGeneral && !validReviewGeneral(value) {
		return nil, "", false
	}
	if section == ReviewConfigFilters && !validReviewFilters(value) {
		return nil, "", false
	}
	if section == ReviewConfigPrompts && !validReviewPrompts(value) {
		return nil, "", false
	}
	if section == ReviewConfigMessages && !validReviewMessages(value) {
		return nil, "", false
	}
	if section == ReviewConfigCategories && !validReviewCategories(value) {
		return nil, "", false
	}
	if section == ReviewConfigSummary && !validReviewSummary(value) {
		return nil, "", false
	}
	if section == ReviewConfigIssueTriage && !validIssueTriageConfig(value) {
		return nil, "", false
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", false
	}
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), true
}

func DefaultReviewConfig(section ReviewConfigSection) json.RawMessage {
	defaults := map[ReviewConfigSection]string{
		ReviewConfigGeneral:     `{"automatic_review":true,"review_drafts":false,"rereview_on_push":true,"merge_gate_enabled":true,"trigger_mode":"automatic","default_review_mode":"standard","review_language":"en","minimum_blocking_severity":"high"}`,
		ReviewConfigCategories:  `{"bug":{"enabled":true,"minimum_severity":"medium"},"security":{"enabled":true,"minimum_severity":"medium"},"performance":{"enabled":true,"minimum_severity":"high"},"maintainability":{"enabled":true,"minimum_severity":"medium"}}`,
		ReviewConfigFilters:     `{"include_paths":[],"exclude_paths":[],"exclude_authors":[],"required_labels":[],"target_branches":["main"],"skip_generated":true,"skip_vendor":true}`,
		ReviewConfigPrompts:     `{"system_instruction":"Focus on actionable defects supported by the changed code.","repository_context":"","max_prompt_tokens":12000,"allow_repository_instructions":false}`,
		ReviewConfigSummary:     `{"sections":["outcome","scope","risk","acceptance","invariants","verification","rollout","rollback","provenance"],"max_characters":4000,"include_change_contract":true,"include_verification_evidence":true}`,
		ReviewConfigMessages:    `{"started":"Review started for {{repository}}#{{review_number}}.","progress":"Reviewing revision {{head_sha}} now.","success":"Review passed for revision {{head_sha}}.","recommendation":"Review completed with recommendations for revision {{head_sha}}.","blocked":"Merge gate blocked: review the actionable findings.","failed":"Review could not complete. Open the run detail for the safe error summary.","needs_attention":"Review requires human attention before trustworthy findings can be published.","superseded":"This review was superseded by a newer revision."}`,
		ReviewConfigModels:      `{"enabled":false,"provider":"deployment","protocol":"deployment","base_url":"","model":"","credential_ref":"","effort":"low","max_prompt_tokens":8000,"token_budget":128000,"subtask_timeout_minutes":5,"max_concurrent_runs":2}`,
		ReviewConfigIssueTriage: `{"enabled":true,"preset":"engineering","language":"inherit","required_issue_sections":["outcome","reproduction","expected_behavior","evidence","acceptance_criteria"],"response_sections":["assessment","missing_context","acceptance_criteria","risk","affected_areas","next_steps","provenance"],"collapse_secondary":true,"link_file_references":true,"reaction_feedback":true,"max_items_per_section":6,"custom_guidance":""}`,
	}
	return json.RawMessage(defaults[section])
}

var reviewMessageKeys = map[string]struct{}{
	"started":         {},
	"progress":        {},
	"success":         {},
	"recommendation":  {},
	"blocked":         {},
	"failed":          {},
	"needs_attention": {},
	"superseded":      {},
	// completed is accepted for the first shipped configuration format. New
	// defaults separate a clean pass from a passed review with recommendations.
	"completed": {},
}

func validReviewMessages(messages map[string]any) bool {
	for key, raw := range messages {
		if _, known := reviewMessageKeys[key]; !known {
			return false
		}
		value, ok := raw.(string)
		if !ok || len([]rune(value)) > 2000 || strings.Contains(value, "<!--") || strings.Contains(value, "-->") || strings.Contains(value, "open-review-platform:") {
			return false
		}
		withoutSupportedVariables := strings.NewReplacer(
			"{{repository}}", "",
			"{{review_number}}", "",
			"{{head_sha}}", "",
			"{{run_url}}", "",
		).Replace(value)
		if strings.Contains(withoutSupportedVariables, "{{") || strings.Contains(withoutSupportedVariables, "}}") {
			return false
		}
	}
	return true
}

// ReviewGeneralConfig contains the run-time subset of General settings. All
// fields are defaulted during decoding because early workspace overrides were
// allowed to specify only the values they changed.
type ReviewGeneralConfig struct {
	AutomaticReview  bool              `json:"automatic_review"`
	ReviewDrafts     bool              `json:"review_drafts"`
	ReReviewOnPush   bool              `json:"rereview_on_push"`
	MergeGateEnabled bool              `json:"merge_gate_enabled"`
	TriggerMode      ReviewTriggerMode `json:"trigger_mode"`
	// DefaultReviewMode resolves an omitted or `configured` review command at
	// admission. It is part of the versioned repository policy, rather than a
	// mutable worker environment setting, so the selected scope is retained on
	// every run.
	DefaultReviewMode       ReviewMode `json:"default_review_mode"`
	ReviewLanguage          string     `json:"review_language"`
	MinimumBlockingSeverity string     `json:"minimum_blocking_severity"`
}

func DefaultReviewGeneralConfig() ReviewGeneralConfig {
	return ReviewGeneralConfig{
		AutomaticReview:         true,
		ReviewDrafts:            false,
		ReReviewOnPush:          true,
		MergeGateEnabled:        true,
		TriggerMode:             ReviewTriggerAutomatic,
		DefaultReviewMode:       ReviewModeStandard,
		ReviewLanguage:          "en",
		MinimumBlockingSeverity: "high",
	}
}

func DecodeReviewGeneralConfig(content json.RawMessage) (ReviewGeneralConfig, error) {
	config := DefaultReviewGeneralConfig()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil || len(fields) == 0 {
		return ReviewGeneralConfig{}, fmt.Errorf("invalid review general configuration")
	}
	triggerModeExplicit := false
	for key, encoded := range fields {
		switch key {
		case "automatic_review":
			if err := json.Unmarshal(encoded, &config.AutomaticReview); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid automatic review setting")
			}
		case "review_drafts":
			if err := json.Unmarshal(encoded, &config.ReviewDrafts); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid review drafts setting")
			}
		case "rereview_on_push":
			if err := json.Unmarshal(encoded, &config.ReReviewOnPush); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid re-review setting")
			}
		case "merge_gate_enabled":
			if err := json.Unmarshal(encoded, &config.MergeGateEnabled); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid merge gate setting")
			}
		case "trigger_mode":
			if err := json.Unmarshal(encoded, &config.TriggerMode); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid review trigger mode")
			}
			triggerModeExplicit = true
		case "default_review_mode":
			if err := json.Unmarshal(encoded, &config.DefaultReviewMode); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid default review mode")
			}
		case "review_language":
			if err := json.Unmarshal(encoded, &config.ReviewLanguage); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid review language")
			}
		case "minimum_blocking_severity":
			if err := json.Unmarshal(encoded, &config.MinimumBlockingSeverity); err != nil {
				return ReviewGeneralConfig{}, fmt.Errorf("invalid minimum blocking severity")
			}
		default:
			return ReviewGeneralConfig{}, fmt.Errorf("unknown review general setting %q", key)
		}
	}
	config.ReviewLanguage = strings.TrimSpace(config.ReviewLanguage)
	config.MinimumBlockingSeverity = strings.ToLower(strings.TrimSpace(config.MinimumBlockingSeverity))
	// Older immutable revisions did not carry trigger_mode. Preserve their
	// meaning: automatic_review=false has always meant explicit requests only.
	if !triggerModeExplicit && !config.AutomaticReview {
		config.TriggerMode = ReviewTriggerManual
	}
	if !config.TriggerMode.Valid() || !config.DefaultReviewMode.Selectable() || !validReviewLanguage(config.ReviewLanguage) || !validReviewBlockingSeverity(config.MinimumBlockingSeverity) {
		return ReviewGeneralConfig{}, fmt.Errorf("invalid review general configuration")
	}
	return config, nil
}

func validReviewGeneral(general map[string]any) bool {
	content, err := json.Marshal(general)
	if err != nil {
		return false
	}
	_, err = DecodeReviewGeneralConfig(content)
	return err == nil
}

func validReviewLanguage(value string) bool {
	return value == "en" || value == "zh-CN" || value == "ja" || value == "es"
}

func validReviewBlockingSeverity(value string) bool {
	return value == "low" || value == "medium" || value == "high" || value == "critical"
}

// ReviewFiltersConfig contains the path-level policy enforced by the runner
// as well as webhook-admission fields that remain part of the same immutable
// configuration contract. Empty includes mean all changed paths are eligible;
// an exclude always wins.
type ReviewFiltersConfig struct {
	IncludePaths   []string `json:"include_paths"`
	ExcludePaths   []string `json:"exclude_paths"`
	ExcludeAuthors []string `json:"exclude_authors"`
	RequiredLabels []string `json:"required_labels"`
	TargetBranches []string `json:"target_branches"`
	SkipGenerated  bool     `json:"skip_generated"`
	SkipVendor     bool     `json:"skip_vendor"`
}

func DefaultReviewFiltersConfig() ReviewFiltersConfig {
	return ReviewFiltersConfig{
		IncludePaths:   []string{},
		ExcludePaths:   []string{},
		ExcludeAuthors: []string{},
		RequiredLabels: []string{},
		TargetBranches: []string{"main"},
		SkipGenerated:  true,
		SkipVendor:     true,
	}
}

func DecodeReviewFiltersConfig(content json.RawMessage) (ReviewFiltersConfig, error) {
	config := DefaultReviewFiltersConfig()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil || len(fields) == 0 {
		return ReviewFiltersConfig{}, fmt.Errorf("invalid review filters configuration")
	}
	for key, encoded := range fields {
		switch key {
		case "include_paths":
			value, err := decodeReviewFilterStrings(encoded, key)
			if err != nil {
				return ReviewFiltersConfig{}, err
			}
			config.IncludePaths = value
		case "exclude_paths":
			value, err := decodeReviewFilterStrings(encoded, key)
			if err != nil {
				return ReviewFiltersConfig{}, err
			}
			config.ExcludePaths = value
		case "exclude_authors":
			value, err := decodeReviewFilterStrings(encoded, key)
			if err != nil {
				return ReviewFiltersConfig{}, err
			}
			config.ExcludeAuthors = value
		case "required_labels":
			value, err := decodeReviewFilterStrings(encoded, key)
			if err != nil {
				return ReviewFiltersConfig{}, err
			}
			config.RequiredLabels = value
		case "target_branches":
			value, err := decodeReviewFilterStrings(encoded, key)
			if err != nil {
				return ReviewFiltersConfig{}, err
			}
			config.TargetBranches = value
		case "skip_generated":
			if err := json.Unmarshal(encoded, &config.SkipGenerated); err != nil {
				return ReviewFiltersConfig{}, fmt.Errorf("invalid skip generated setting")
			}
		case "skip_vendor":
			if err := json.Unmarshal(encoded, &config.SkipVendor); err != nil {
				return ReviewFiltersConfig{}, fmt.Errorf("invalid skip vendor setting")
			}
		default:
			return ReviewFiltersConfig{}, fmt.Errorf("unknown review filters setting %q", key)
		}
	}
	return config, nil
}

func (config ReviewFiltersConfig) AllowsPath(value string) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "./")
	if value == "" {
		return false
	}
	if len(config.IncludePaths) > 0 && !reviewPathMatchesAny(config.IncludePaths, value) {
		return false
	}
	excludes := config.ExcludePaths
	if config.SkipGenerated {
		excludes = append(excludes, "**/generated/**")
	}
	if config.SkipVendor {
		excludes = append(excludes, "**/vendor/**", "**/node_modules/**")
	}
	return !reviewPathMatchesAny(excludes, value)
}

// AllowsAdmission applies the provider metadata policy before a durable
// automatic review is created. Empty author/label/branch values never bypass a
// configured requirement: incomplete provider metadata should result in a
// visible policy skip, not a review whose governance scope is ambiguous.
func (config ReviewFiltersConfig) AllowsAdmission(author string, labels []string, targetBranch string) bool {
	author = strings.ToLower(strings.TrimSpace(author))
	if author == "" && len(config.ExcludeAuthors) > 0 {
		return false
	}
	for _, excluded := range config.ExcludeAuthors {
		if author == strings.ToLower(strings.TrimSpace(excluded)) {
			return false
		}
	}
	if len(config.RequiredLabels) > 0 {
		available := make(map[string]struct{}, len(labels))
		for _, label := range labels {
			if normalized := strings.ToLower(strings.TrimSpace(label)); normalized != "" {
				available[normalized] = struct{}{}
			}
		}
		for _, required := range config.RequiredLabels {
			if _, ok := available[strings.ToLower(strings.TrimSpace(required))]; !ok {
				return false
			}
		}
	}
	return reviewPathMatchesAny(config.TargetBranches, strings.TrimSpace(targetBranch))
}

func validReviewFilters(filters map[string]any) bool {
	content, err := json.Marshal(filters)
	if err != nil {
		return false
	}
	_, err = DecodeReviewFiltersConfig(content)
	return err == nil
}

func decodeReviewFilterStrings(encoded json.RawMessage, field string) ([]string, error) {
	var values []string
	if err := json.Unmarshal(encoded, &values); err != nil || len(values) > 64 {
		return nil, fmt.Errorf("invalid %s", field)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("invalid %s", field)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("duplicate %s", field)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func reviewPathMatchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if reviewPathGlobMatches(pattern, value) {
			return true
		}
	}
	return false
}

// reviewPathGlobMatches implements the small, gitignore-shaped pattern subset
// exposed by the Console. `**` crosses directories, while `*` and `?` stay in
// one segment. Every other character is quoted so a workspace-provided pattern
// cannot become an arbitrary regular expression.
func reviewPathGlobMatches(pattern, value string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	if pattern == "" {
		return false
	}
	var expression strings.Builder
	expression.WriteString("^")
	characters := []rune(pattern)
	for index := 0; index < len(characters); index++ {
		switch characters[index] {
		case '*':
			if index+1 < len(characters) && characters[index+1] == '*' {
				index++
				if index+1 < len(characters) && characters[index+1] == '/' {
					index++
					expression.WriteString("(?:.*/)?")
				} else {
					expression.WriteString(".*")
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(string(characters[index])))
		}
	}
	expression.WriteString("$")
	matched, err := regexp.MatchString(expression.String(), value)
	return err == nil && matched
}

// ReviewPromptConfig is control-plane-authored input to the model. It never
// stores a provider credential or executes a repository file by default.
// Repository instruction imports require an explicit opt-in because those
// files live on the untrusted pull-request head.
type ReviewPromptConfig struct {
	SystemInstruction           string `json:"system_instruction"`
	RepositoryContext           string `json:"repository_context"`
	MaxPromptTokens             int    `json:"max_prompt_tokens"`
	AllowRepositoryInstructions bool   `json:"allow_repository_instructions"`
}

func DefaultReviewPromptConfig() ReviewPromptConfig {
	return ReviewPromptConfig{
		SystemInstruction:           "Focus on actionable defects supported by the changed code.",
		RepositoryContext:           "",
		MaxPromptTokens:             12000,
		AllowRepositoryInstructions: false,
	}
}

func DecodeReviewPromptConfig(content json.RawMessage) (ReviewPromptConfig, error) {
	config := DefaultReviewPromptConfig()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil || len(fields) == 0 {
		return ReviewPromptConfig{}, fmt.Errorf("invalid review prompts configuration")
	}
	for key, encoded := range fields {
		switch key {
		case "system_instruction":
			if err := json.Unmarshal(encoded, &config.SystemInstruction); err != nil {
				return ReviewPromptConfig{}, fmt.Errorf("invalid system instruction")
			}
		case "repository_context":
			if err := json.Unmarshal(encoded, &config.RepositoryContext); err != nil {
				return ReviewPromptConfig{}, fmt.Errorf("invalid repository context")
			}
		case "max_prompt_tokens":
			if err := json.Unmarshal(encoded, &config.MaxPromptTokens); err != nil {
				return ReviewPromptConfig{}, fmt.Errorf("invalid max prompt tokens")
			}
		case "allow_repository_instructions":
			if err := json.Unmarshal(encoded, &config.AllowRepositoryInstructions); err != nil {
				return ReviewPromptConfig{}, fmt.Errorf("invalid repository instruction setting")
			}
		default:
			return ReviewPromptConfig{}, fmt.Errorf("unknown review prompts setting %q", key)
		}
	}
	if len([]rune(config.SystemInstruction)) > 8000 || len([]rune(config.RepositoryContext)) > 8000 || config.MaxPromptTokens < 1000 || config.MaxPromptTokens > 100000 {
		return ReviewPromptConfig{}, fmt.Errorf("invalid review prompts configuration")
	}
	return config, nil
}

func validReviewPrompts(prompts map[string]any) bool {
	content, err := json.Marshal(prompts)
	if err != nil {
		return false
	}
	_, err = DecodeReviewPromptConfig(content)
	return err == nil
}

// IssueTriageConfig controls the repository-specific contract used for
// user-authored Issue analysis. It is intentionally declarative: operators
// select bounded sections and trusted guidance instead of injecting an
// arbitrary Markdown template into provider comments.
type IssueTriageConfig struct {
	Enabled               bool     `json:"enabled"`
	Preset                string   `json:"preset"`
	Language              string   `json:"language"`
	RequiredIssueSections []string `json:"required_issue_sections"`
	ResponseSections      []string `json:"response_sections"`
	CollapseSecondary     bool     `json:"collapse_secondary"`
	LinkFileReferences    bool     `json:"link_file_references"`
	ReactionFeedback      bool     `json:"reaction_feedback"`
	MaxItemsPerSection    int      `json:"max_items_per_section"`
	CustomGuidance        string   `json:"custom_guidance"`
}

var issueTriagePresets = map[string]struct{}{
	"engineering": {}, "bug": {}, "feature": {}, "concise": {}, "security": {},
	"api_contract": {}, "database": {}, "migration": {}, "accessibility": {}, "reliability": {},
	"incident": {}, "product": {}, "performance": {}, "compliance": {}, "custom": {},
}

var issueTriageRequiredSections = map[string]struct{}{
	"outcome": {}, "reproduction": {}, "expected_behavior": {}, "observed_behavior": {},
	"evidence": {}, "acceptance_criteria": {}, "risk": {}, "security_impact": {},
	"impact": {}, "timeline": {}, "detection": {}, "mitigation": {}, "non_goals": {},
}

var issueTriageResponseSections = map[string]struct{}{
	"assessment": {}, "missing_context": {}, "acceptance_criteria": {}, "risk": {},
	"affected_areas": {}, "next_steps": {}, "provenance": {},
}

func DefaultIssueTriageConfig() IssueTriageConfig {
	return IssueTriageConfig{
		Enabled: true, Preset: "engineering", Language: "inherit",
		RequiredIssueSections: []string{"outcome", "reproduction", "expected_behavior", "evidence", "acceptance_criteria"},
		ResponseSections:      []string{"assessment", "missing_context", "acceptance_criteria", "risk", "affected_areas", "next_steps", "provenance"},
		CollapseSecondary:     true, LinkFileReferences: true, ReactionFeedback: true,
		MaxItemsPerSection: 6,
	}
}

func DecodeIssueTriageConfig(content json.RawMessage) (IssueTriageConfig, error) {
	config := DefaultIssueTriageConfig()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(content, &fields); err != nil || len(fields) == 0 {
		return IssueTriageConfig{}, fmt.Errorf("invalid issue triage configuration")
	}
	for key, encoded := range fields {
		switch key {
		case "enabled":
			if err := json.Unmarshal(encoded, &config.Enabled); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage enabled setting")
			}
		case "preset":
			if err := json.Unmarshal(encoded, &config.Preset); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage preset")
			}
		case "language":
			if err := json.Unmarshal(encoded, &config.Language); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage language")
			}
		case "required_issue_sections":
			if err := json.Unmarshal(encoded, &config.RequiredIssueSections); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid required issue sections")
			}
		case "response_sections":
			if err := json.Unmarshal(encoded, &config.ResponseSections); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage response sections")
			}
		case "collapse_secondary":
			if err := json.Unmarshal(encoded, &config.CollapseSecondary); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage collapse setting")
			}
		case "link_file_references":
			if err := json.Unmarshal(encoded, &config.LinkFileReferences); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage link setting")
			}
		case "reaction_feedback":
			if err := json.Unmarshal(encoded, &config.ReactionFeedback); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage feedback setting")
			}
		case "max_items_per_section":
			if err := json.Unmarshal(encoded, &config.MaxItemsPerSection); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage item limit")
			}
		case "custom_guidance":
			if err := json.Unmarshal(encoded, &config.CustomGuidance); err != nil {
				return IssueTriageConfig{}, fmt.Errorf("invalid issue triage guidance")
			}
		default:
			return IssueTriageConfig{}, fmt.Errorf("unknown issue triage setting %q", key)
		}
	}
	config.Preset = strings.ToLower(strings.TrimSpace(config.Preset))
	config.Language = strings.TrimSpace(config.Language)
	if _, ok := issueTriagePresets[config.Preset]; !ok || !validIssueTriageLanguage(config.Language) ||
		!validUniqueChoiceList(config.RequiredIssueSections, issueTriageRequiredSections, 13, true) ||
		!validUniqueChoiceList(config.ResponseSections, issueTriageResponseSections, 7, false) ||
		config.MaxItemsPerSection < 1 || config.MaxItemsPerSection > 10 || len([]rune(config.CustomGuidance)) > 4000 ||
		strings.Contains(config.CustomGuidance, "<!--") || strings.Contains(config.CustomGuidance, "-->") {
		return IssueTriageConfig{}, fmt.Errorf("invalid issue triage configuration")
	}
	return config, nil
}

func validIssueTriageLanguage(value string) bool {
	return value == "inherit" || value == "en" || value == "zh-CN" || value == "ja" || value == "es"
}

func validUniqueChoiceList(values []string, allowed map[string]struct{}, maximum int, emptyAllowed bool) bool {
	if len(values) > maximum || (!emptyAllowed && len(values) == 0) {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validIssueTriageConfig(value map[string]any) bool {
	content, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = DecodeIssueTriageConfig(content)
	return err == nil
}

// ReviewCategoryPolicy is the review-result policy for one finding category.
// It is applied after the engine returns findings and before they are stored,
// published, or evaluated by the merge gate. The policy is intentionally
// separate from prompt/rule selection: disabling a category is an explicit
// operator decision not to surface that category's findings for a run.
type ReviewCategoryPolicy struct {
	Enabled         bool   `json:"enabled"`
	MinimumSeverity string `json:"minimum_severity"`
}

// ReviewCategoriesConfig permits standard and enterprise-specific category
// names. Unknown finding categories remain visible, rather than being silently
// suppressed by a configuration that does not mention them.
type ReviewCategoriesConfig map[string]ReviewCategoryPolicy

func DecodeReviewCategoriesConfig(content json.RawMessage) (ReviewCategoriesConfig, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil || len(raw) == 0 || len(raw) > 32 {
		return nil, fmt.Errorf("invalid review categories configuration")
	}
	config := make(ReviewCategoriesConfig, len(raw))
	for name, encoded := range raw {
		name = strings.ToLower(strings.TrimSpace(name))
		if !validReviewCategoryName(name) {
			return nil, fmt.Errorf("invalid review category")
		}
		if _, duplicate := config[name]; duplicate {
			return nil, fmt.Errorf("duplicate review category %q", name)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil || len(fields) != 2 {
			return nil, fmt.Errorf("invalid policy for review category %q", name)
		}
		enabled, enabledOK := fields["enabled"]
		minimum, minimumOK := fields["minimum_severity"]
		if !enabledOK || !minimumOK {
			return nil, fmt.Errorf("incomplete policy for review category %q", name)
		}
		var policy ReviewCategoryPolicy
		if err := json.Unmarshal(enabled, &policy.Enabled); err != nil {
			return nil, fmt.Errorf("invalid enabled flag for review category %q", name)
		}
		if err := json.Unmarshal(minimum, &policy.MinimumSeverity); err != nil {
			return nil, fmt.Errorf("invalid minimum severity for review category %q", name)
		}
		policy.MinimumSeverity = strings.ToLower(strings.TrimSpace(policy.MinimumSeverity))
		if !validReviewFindingSeverity(policy.MinimumSeverity) {
			return nil, fmt.Errorf("invalid minimum severity for review category %q", name)
		}
		config[name] = policy
	}
	return config, nil
}

// FilterFindings preserves every finding in a category not governed by this
// snapshot. This avoids an upgrade or a model emitting a new category from
// making potentially important findings disappear. Configured categories are
// suppressed only when explicitly disabled or below their configured threshold.
func (config ReviewCategoriesConfig) FilterFindings(findings []Finding) (kept []Finding, suppressed int) {
	kept = make([]Finding, 0, len(findings))
	for _, finding := range findings {
		policy, configured := config[strings.ToLower(strings.TrimSpace(finding.Category))]
		if !configured {
			kept = append(kept, finding)
			continue
		}
		if !policy.Enabled || reviewFindingSeverityRank(finding.Severity) < reviewFindingSeverityRank(policy.MinimumSeverity) {
			suppressed++
			continue
		}
		kept = append(kept, finding)
	}
	return kept, suppressed
}

// FilterFindingsByPublicationMinimum applies the installation floor frozen
// when this run was admitted. A finding that blocks the independently
// configured merge gate is always published so reviewers can fix it. An empty
// publication value belongs only to historical runs that predate the snapshot.
// Unknown severities remain visible rather than being silently hidden.
func FilterFindingsByPublicationMinimum(findings []Finding, minimum, blockingMinimum string) ([]Finding, int, error) {
	if minimum == "" {
		return findings, 0, nil
	}
	if !validReviewFindingSeverity(minimum) {
		return nil, 0, fmt.Errorf("invalid publication minimum severity %q", minimum)
	}
	if blockingMinimum != "" && blockingMinimum != "off" && !validReviewFindingSeverity(blockingMinimum) {
		return nil, 0, fmt.Errorf("invalid blocking minimum severity %q", blockingMinimum)
	}
	kept := make([]Finding, 0, len(findings))
	suppressed := 0
	minimumRank := reviewFindingSeverityRank(minimum)
	blockingRank := reviewFindingSeverityRank(blockingMinimum)
	for _, finding := range findings {
		rank := reviewFindingSeverityRank(finding.Severity)
		if rank != 0 && rank < minimumRank && (blockingRank == 0 || rank < blockingRank) {
			suppressed++
			continue
		}
		kept = append(kept, finding)
	}
	return kept, suppressed, nil
}

func validReviewCategories(categories map[string]any) bool {
	content, err := json.Marshal(categories)
	if err != nil {
		return false
	}
	_, err = DecodeReviewCategoriesConfig(content)
	return err == nil
}

func validReviewCategoryName(value string) bool {
	if value == "" || len(value) > 48 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func validReviewFindingSeverity(value string) bool {
	return reviewFindingSeverityRank(value) > 0
}

func reviewFindingSeverityRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

var reviewSummarySectionKeys = map[string]struct{}{
	"outcome":      {},
	"scope":        {},
	"risk":         {},
	"acceptance":   {},
	"invariants":   {},
	"verification": {},
	"rollout":      {},
	"rollback":     {},
	"provenance":   {},
}

// ReviewSummaryConfig controls optional evidence sections only. The publisher
// always renders the canonical merge-gate verdict and finding index first.
type ReviewSummaryConfig struct {
	Sections                    []string `json:"sections"`
	MaxCharacters               int      `json:"max_characters"`
	IncludeChangeContract       bool     `json:"include_change_contract"`
	IncludeVerificationEvidence bool     `json:"include_verification_evidence"`
}

func DefaultReviewSummaryConfig() ReviewSummaryConfig {
	return ReviewSummaryConfig{
		Sections:                    []string{"outcome", "scope", "risk", "acceptance", "invariants", "verification", "rollout", "rollback", "provenance"},
		MaxCharacters:               4000,
		IncludeChangeContract:       true,
		IncludeVerificationEvidence: true,
	}
}

func DecodeReviewSummaryConfig(content json.RawMessage) (ReviewSummaryConfig, error) {
	var config ReviewSummaryConfig
	if err := json.Unmarshal(content, &config); err != nil || !config.Valid() {
		return ReviewSummaryConfig{}, fmt.Errorf("invalid review summary configuration")
	}
	return config, nil
}

func (config ReviewSummaryConfig) Valid() bool {
	if config.MaxCharacters < 500 || config.MaxCharacters > 20000 || len(config.Sections) == 0 || len(config.Sections) > len(reviewSummarySectionKeys) {
		return false
	}
	seen := make(map[string]struct{}, len(config.Sections))
	for _, section := range config.Sections {
		if _, allowed := reviewSummarySectionKeys[section]; !allowed {
			return false
		}
		if _, duplicate := seen[section]; duplicate {
			return false
		}
		seen[section] = struct{}{}
	}
	return true
}

func (config ReviewSummaryConfig) Includes(section string) bool {
	for _, configured := range config.Sections {
		if configured == section {
			return true
		}
	}
	return false
}

func validReviewSummary(summary map[string]any) bool {
	content, err := json.Marshal(summary)
	if err != nil {
		return false
	}
	config, err := DecodeReviewSummaryConfig(content)
	return err == nil && config.Valid()
}

// ModelRouteConfig contains only routing metadata and an opaque credential
// reference. Credential values are resolved by the worker immediately before
// launching OCR and are never stored in this structure.
type ModelRouteConfig struct {
	Enabled               bool   `json:"enabled"`
	Provider              string `json:"provider"`
	Protocol              string `json:"protocol"`
	BaseURL               string `json:"base_url"`
	Model                 string `json:"model"`
	CredentialRef         string `json:"credential_ref"`
	Effort                string `json:"effort"`
	MaxPromptTokens       int    `json:"max_prompt_tokens"`
	TokenBudget           int    `json:"token_budget"`
	SubtaskTimeoutMinutes int    `json:"subtask_timeout_minutes"`
	// MaxConcurrentRuns is a tenant-scope execution ceiling, not a
	// worker-process setting. Model snapshots retain it for provenance, but
	// admission always uses the active tenant-scope value so repository route
	// overrides cannot silently alter workspace capacity.
	MaxConcurrentRuns int `json:"max_concurrent_runs"`
}

func (route ModelRouteConfig) Valid() bool {
	if !route.Enabled {
		return route.Provider == "deployment" && route.Protocol == "deployment" && route.BaseURL == "" && route.Model == "" && route.CredentialRef == "" && validModelLimits(route)
	}
	if route.Provider != "openai-compatible" && route.Provider != "anthropic" {
		return false
	}
	if route.Protocol != "openai-chat" && route.Protocol != "anthropic-messages" {
		return false
	}
	if (route.Provider == "anthropic") != (route.Protocol == "anthropic-messages") {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(route.BaseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	credentialRef := strings.TrimSpace(route.CredentialRef)
	if !strings.HasPrefix(credentialRef, "secret://") && !strings.HasPrefix(credentialRef, "env://OPEN_REVIEW_MODEL_SECRET_") {
		return false
	}
	return strings.TrimSpace(route.Model) != "" && len(route.Model) <= 160 && validModelLimits(route)
}

func validModelLimits(route ModelRouteConfig) bool {
	return (route.Effort == "low" || route.Effort == "medium" || route.Effort == "high") &&
		route.MaxPromptTokens >= 1000 && route.MaxPromptTokens <= 1000000 &&
		route.TokenBudget >= route.MaxPromptTokens && route.TokenBudget <= 10000000 &&
		route.SubtaskTimeoutMinutes >= 1 && route.SubtaskTimeoutMinutes <= 120 &&
		route.MaxConcurrentRuns >= 1 && route.MaxConcurrentRuns <= 64
}

func DecodeModelRoute(content json.RawMessage) (ModelRouteConfig, error) {
	var route ModelRouteConfig
	if err := json.Unmarshal(content, &route); err != nil {
		return ModelRouteConfig{}, err
	}
	// Routes created before tenant concurrency existed retain the initial safe
	// default. A future tenant-scope save includes the explicit field.
	if route.MaxConcurrentRuns == 0 {
		route.MaxConcurrentRuns = 2
	}
	if !route.Valid() {
		return ModelRouteConfig{}, fmt.Errorf("model route configuration is invalid")
	}
	return route, nil
}
