package rules

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OCRRuleFile is the narrow, trusted adapter format accepted by
// `ocr review --rule`. It is deliberately produced only from a canonical
// platform snapshot, never from a file in the pull-request head.
type OCRRuleFile struct {
	Rules []OCRRule `json:"rules"`
}

type OCRRule struct {
	Path            string `json:"path"`
	Rule            string `json:"rule"`
	MergeSystemRule bool   `json:"merge_system_rule"`
}

// OCRRuleFileForSnapshot converts all effective enterprise rules into one
// explicit OCR catch-all rule. OCR resolves entries first-match-wins, so a
// single deterministic aggregate is the only safe way to preserve all
// append/mandatory semantics in a single pass. Binding scope is resolved
// before the snapshot is created; each rule's prompt remains labelled with
// its immutable key, severity, enforcement, and source version.
func OCRRuleFileForSnapshot(snapshot Snapshot) (OCRRuleFile, error) {
	if snapshot.Engine != "ocr" {
		return OCRRuleFile{}, fmt.Errorf("unsupported rule snapshot engine %q", snapshot.Engine)
	}
	if len(snapshot.Rules) == 0 {
		return OCRRuleFile{}, nil
	}
	sections := make([]string, 0, len(snapshot.Rules))
	for _, effective := range snapshot.Rules {
		prompt, err := ocrPrompt(effective)
		if err != nil {
			return OCRRuleFile{}, err
		}
		sections = append(sections, fmt.Sprintf("## %s\n\nSeverity: %s\nEnforcement: %s\nSource version: %s\n\n%s", effective.Key, effective.Severity, effective.Enforcement, effective.SourceVersion, prompt))
	}
	return OCRRuleFile{Rules: []OCRRule{{
		Path:            "**/*",
		Rule:            "# Enterprise review rules\n\n" + strings.Join(sections, "\n\n---\n\n"),
		MergeSystemRule: snapshot.MergeSystemRule,
	}}}, nil
}

func ocrPrompt(effective EffectiveRule) (string, error) {
	var content struct {
		Prompt string `json:"prompt"`
		Rule   string `json:"rule"`
	}
	if err := json.Unmarshal(effective.Content, &content); err != nil {
		return "", fmt.Errorf("rule %q has invalid OCR content: %w", effective.Key, err)
	}
	prompt := strings.TrimSpace(content.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(content.Rule)
	}
	if prompt == "" {
		return "", fmt.Errorf("rule %q must provide content.prompt or content.rule for OCR", effective.Key)
	}
	return prompt, nil
}
