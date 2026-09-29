package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// IssueFormatTemplate is a workspace-owned reusable authoring contract. It is
// deliberately not a live binding: applying it only prepares a review-config
// draft, whose separately versioned save becomes execution authority.
type IssueFormatTemplate struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Revision      int             `json:"revision"`
	Content       json.RawMessage `json:"content"`
	ContentSHA256 string          `json:"content_sha256"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedBy     string          `json:"updated_by"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type IssueFormatTemplateInput struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	ExpectedRevision int             `json:"expected_revision"`
	Content          json.RawMessage `json:"content"`
}

func NormalizeIssueFormatTemplateInput(input IssueFormatTemplateInput, requireRevision bool) (IssueFormatTemplateInput, string, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if !validIssueFormatTemplateName(input.Name) || len([]rune(input.Description)) > 240 || strings.Contains(input.Description, "<!--") || strings.Contains(input.Description, "-->") {
		return IssueFormatTemplateInput{}, "", fmt.Errorf("invalid issue format template metadata")
	}
	if (requireRevision && input.ExpectedRevision < 1) || (!requireRevision && input.ExpectedRevision != 0) {
		return IssueFormatTemplateInput{}, "", fmt.Errorf("invalid issue format template revision")
	}
	canonical, hash, err := CanonicalIssueFormatTemplateContent(input.Content)
	if err != nil {
		return IssueFormatTemplateInput{}, "", err
	}
	input.Content = canonical
	return input, hash, nil
}

func CanonicalIssueFormatTemplateContent(content json.RawMessage) (json.RawMessage, string, error) {
	config, err := DecodeIssueTriageConfig(content)
	if err != nil {
		return nil, "", fmt.Errorf("invalid issue format template content")
	}
	config.CustomGuidance = strings.TrimSpace(config.CustomGuidance)
	value := struct {
		Preset                string   `json:"preset"`
		Language              string   `json:"language"`
		RequiredIssueSections []string `json:"required_issue_sections"`
		ResponseSections      []string `json:"response_sections"`
		CollapseSecondary     bool     `json:"collapse_secondary"`
		LinkFileReferences    bool     `json:"link_file_references"`
		ReactionFeedback      bool     `json:"reaction_feedback"`
		MaxItemsPerSection    int      `json:"max_items_per_section"`
		CustomGuidance        string   `json:"custom_guidance"`
	}{
		Preset: config.Preset, Language: config.Language,
		RequiredIssueSections: config.RequiredIssueSections,
		ResponseSections:      config.ResponseSections,
		CollapseSecondary:     config.CollapseSecondary,
		LinkFileReferences:    config.LinkFileReferences,
		ReactionFeedback:      config.ReactionFeedback,
		MaxItemsPerSection:    config.MaxItemsPerSection,
		CustomGuidance:        config.CustomGuidance,
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("encode issue format template: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

func IssueFormatTemplateNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func validIssueFormatTemplateName(name string) bool {
	runes := []rune(name)
	if len(runes) < 2 || len(runes) > 80 {
		return false
	}
	for _, value := range runes {
		if unicode.IsControl(value) {
			return false
		}
	}
	return true
}
