package publisher

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

type ContractSection string

const (
	ContractOutcome           ContractSection = "outcome"
	ContractScope             ContractSection = "scope"
	ContractRisk              ContractSection = "risk"
	ContractAcceptanceMapping ContractSection = "acceptance mapping"
	ContractInvariants        ContractSection = "invariants"
	ContractVerification      ContractSection = "verification"
	ContractRollout           ContractSection = "rollout"
	ContractRollback          ContractSection = "rollback"
	ContractProvenance        ContractSection = "provenance"
)

type ContractQuality string

const (
	ContractEmpty    ContractQuality = "empty"
	ContractMinimal  ContractQuality = "minimal"
	ContractPartial  ContractQuality = "partial"
	ContractComplete ContractQuality = "complete"
)

type ChangeContract struct {
	Quality  ContractQuality
	Sections map[ContractSection]string
	Missing  []ContractSection
}

var markdownHeading = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

// ParseChangeContract reads only explicit PR/MR description sections. The
// submitted text is untrusted descriptive evidence: it can improve the report,
// but it never changes rules, model permissions, or the merge threshold.
func ParseChangeContract(description string) ChangeContract {
	contract := ChangeContract{Quality: ContractEmpty, Sections: make(map[ContractSection]string)}
	if strings.TrimSpace(description) == "" {
		contract.Missing = requiredContractSections()
		return contract
	}
	var current ContractSection
	var content strings.Builder
	flush := func() {
		if current == "" {
			content.Reset()
			return
		}
		value := strings.TrimSpace(content.String())
		if value != "" {
			contract.Sections[current] = value
		}
		content.Reset()
	}
	for _, line := range strings.Split(strings.ReplaceAll(description, "\r\n", "\n"), "\n") {
		match := markdownHeading.FindStringSubmatch(line)
		if len(match) == 2 {
			if next := recognizedContractSection(match[1]); next != "" {
				flush()
				current = next
				continue
			}
		}
		if current != "" {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	flush()
	contract.Missing = missingContractSections(contract.Sections)
	switch {
	case len(contract.Sections) == 0:
		contract.Quality = ContractMinimal
	case len(contract.Missing) == 0:
		contract.Quality = ContractComplete
	default:
		contract.Quality = ContractPartial
	}
	return contract
}

func recognizedContractSection(value string) ContractSection {
	normalized := normalizeHeading(value)
	switch normalized {
	case "outcome", "result", "goal", "目标", "结果":
		return ContractOutcome
	case "scope", "change scope", "范围", "改动范围":
		return ContractScope
	case "risk", "risks", "风险":
		return ContractRisk
	case "acceptance", "acceptance criteria", "acceptance mapping", "验收", "验收标准", "验收映射":
		return ContractAcceptanceMapping
	case "invariant", "invariants", "系统不变量", "不变量":
		return ContractInvariants
	case "verification", "validation", "test evidence", "验证", "验证证据":
		return ContractVerification
	case "rollout", "release", "灰度", "发布", "发布计划":
		return ContractRollout
	case "rollback", "回滚", "回滚计划":
		return ContractRollback
	case "provenance", "traceability", "来源", "追溯":
		return ContractProvenance
	default:
		return ""
	}
}

func normalizeHeading(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.Join(strings.Fields(value), " ")
}

func requiredContractSections() []ContractSection {
	return []ContractSection{ContractOutcome, ContractScope, ContractRisk, ContractAcceptanceMapping, ContractInvariants, ContractVerification, ContractRollout, ContractRollback}
}

func missingContractSections(sections map[ContractSection]string) []ContractSection {
	missing := make([]ContractSection, 0)
	for _, section := range requiredContractSections() {
		if strings.TrimSpace(sections[section]) == "" {
			missing = append(missing, section)
		}
	}
	return missing
}

func contractQualityComponents(contract ChangeContract) []MarkdownComponent {
	switch contract.Quality {
	case ContractEmpty:
		return []MarkdownComponent{
			BadgeRow{Badges: []Badge{{Label: "task context", Value: "missing", Color: "d1242f"}}},
			Heading{Level: 3, Text: "🤔 Task context needed"},
			Paragraph{Text: "The PR/MR description is empty. Add the expected outcome, scope boundaries, acceptance mapping, invariants, verification evidence, rollout, and rollback information so business-level validation can be grounded."},
		}
	case ContractMinimal:
		return []MarkdownComponent{
			BadgeRow{Badges: []Badge{{Label: "task context", Value: "insufficient", Color: "bf8700"}}},
			Heading{Level: 3, Text: "🤔 Insufficient task context"},
			Paragraph{Text: "A description exists, but it does not use recognizable change-contract sections. Code findings can still be produced, but business acceptance, rollout, and rollback cannot be verified."},
		}
	case ContractPartial:
		missing := make([]string, 0, len(contract.Missing))
		for _, section := range contract.Missing {
			missing = append(missing, "`"+string(section)+"`")
		}
		return []MarkdownComponent{
			BadgeRow{Badges: []Badge{{Label: "task context", Value: "partial", Color: "bf8700"}}},
			Heading{Level: 3, Text: "🧩 Limited task context"},
			Paragraph{Text: "Missing sections: " + strings.Join(missing, ", ") + ". Missing declarations are reported as unknown and never invented by the reviewer."},
		}
	default:
		return []MarkdownComponent{
			BadgeRow{Badges: []Badge{{Label: "task context", Value: "complete", Color: "2da44e"}}},
			Paragraph{Text: "All required change-contract sections were detected. Their contents are treated as author-declared, untrusted evidence until verified."},
		}
	}
}

func declaredEvidence(contract ChangeContract, section ContractSection) MarkdownComponent {
	value := strings.TrimSpace(contract.Sections[section])
	if value == "" {
		return nil
	}
	const limit = 1000
	if len(value) > limit {
		value = value[:limit] + "\n… (truncated)"
	}
	escaped := html.EscapeString(value)
	lines := strings.Split(escaped, "\n")
	for index := range lines {
		lines[index] = "> " + lines[index]
	}
	return Details{
		Summary: "Author-declared " + string(section) + " (unverified)",
		Components: []MarkdownComponent{
			Paragraph{Text: strings.Join(lines, "\n")},
		},
	}
}
