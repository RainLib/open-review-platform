package issuetriage

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

var affectedFileReference = regexp.MustCompile("^`?([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)(?::([0-9]+)(?:-([0-9]+))?)?`?(?:\\s*(?:—|–|-)\\s*(.+))?$")

func Acknowledgement(job domain.ProviderIssueAnalysisJob) string {
	return AcknowledgementWithLinks(job, FileLinkPolicy{})
}

func AcknowledgementWithLinks(job domain.ProviderIssueAnalysisJob, linkPolicy FileLinkPolicy) string {
	copy := issueTriageCopyFor(job)
	return fmt.Sprintf("## 👀 Open Review · %s\n\n> "+copy.ackReviewing+"\n\n| %s | %s | %s |\n| --- | --- | --- |\n| **%s** | `%d` | %s |\n\n%s%s", copy.ackStarted, job.Repository, job.IssueNumber, copy.status, copy.revision, copy.scope, copy.analyzing, job.Revision, copy.issueContext, copy.acknowledgementBoundary, issueConsoleLink(job, copy, linkPolicy))
}

func Report(job domain.ProviderIssueAnalysisJob, result Result) string {
	return ReportWithLinks(job, result, FileLinkPolicy{})
}

// ReportWithLinks renders one Issue-context-only result. The optional link
// policy changes only browser navigation; it never expands analysis evidence.
func ReportWithLinks(job domain.ProviderIssueAnalysisJob, result Result, linkPolicy FileLinkPolicy) string {
	config := effectiveIssueTriageConfig(job)
	copy := issueTriageCopyFor(job)
	result.limitLists(config.MaxItemsPerSection)
	quality := map[string]string{"sufficient": "✅ " + copy.sufficient, "partial": "⚠️ " + copy.partial, "insufficient": "⛔ " + copy.insufficient}[result.ContextQuality]
	title := "✅ Open Review · " + copy.completed
	if result.ContextQuality == "partial" {
		title = "⚠️ Open Review · " + copy.completedWithGaps
	} else if result.ContextQuality == "insufficient" {
		title = "⛔ Open Review · " + copy.moreContextRequired
	}
	var builder strings.Builder
	builder.WriteString("## " + title + "\n\n")
	builder.WriteString("| " + copy.signal + " | " + copy.result + " |\n| --- | --- |\n")
	builder.WriteString("| **" + copy.contextQuality + "** | " + quality + " |\n")
	builder.WriteString(fmt.Sprintf("| **%s** | `%d` |\n", copy.revision, job.Revision))
	builder.WriteString("| **" + copy.evidenceBoundary + "** | " + copy.evidenceScope + " |\n\n")
	if link := linkPolicy.ConsoleAnalysis(job); link != "" {
		builder.WriteString("[" + copy.viewAnalysis + "](" + link + ")\n\n")
	}
	if hasResponseSection(config, "assessment") {
		builder.WriteString("### 🧭 " + copy.assessment + "\n\n" + result.Summary + "\n\n")
	}
	if hasResponseSection(config, "missing_context") {
		writeSection(&builder, "### 🧩 "+copy.missingContext, result.MissingInformation, copy.noMissingContext)
	}
	if hasResponseSection(config, "acceptance_criteria") {
		writeSection(&builder, "### ✅ "+copy.acceptanceCriteria, result.AcceptanceCriteria, copy.defaultAcceptance)
	}
	if hasResponseSection(config, "risk") || hasResponseSection(config, "affected_areas") {
		openOptionalSection(&builder, config, "⚠️ "+copy.riskSummary)
		if hasResponseSection(config, "risk") {
			writeSection(&builder, optionalHeading(config, copy.riskAndImpact), result.RiskAssessment, noConcreteRiskClaim(copy.language))
		}
		if hasResponseSection(config, "affected_areas") {
			writeAffectedAreas(&builder, job, result.AffectedAreas, config.LinkFileReferences, config.CollapseSecondary, copy, linkPolicy)
		}
		closeOptionalSection(&builder, config)
	}
	if hasResponseSection(config, "next_steps") {
		openOptionalSection(&builder, config, "🚀 "+copy.nextSteps)
		if !config.CollapseSecondary {
			builder.WriteString("### 🚀 " + copy.nextSteps + "\n\n")
		}
		writeList(&builder, result.NextSteps, copy.defaultNextSteps)
		closeOptionalSection(&builder, config)
	}
	if hasResponseSection(config, "provenance") {
		openOptionalSection(&builder, config, copy.provenance)
		if !config.CollapseSecondary {
			builder.WriteString("### " + copy.provenance + "\n\n")
		}
		builder.WriteString(fmt.Sprintf("- %s: `%d`\n- %s: `%s`\n- %s: `%s`\n- %s: `%s`\n", copy.revision, job.Revision, copy.modelRoute, shortSHA(job.ModelRouteSHA256), copy.promptPolicy, shortSHA(job.PromptConfigSHA256), copy.issueFormatPolicy, shortSHA(job.IssueTriageConfigSHA256)))
		builder.WriteString("- " + copy.scope + ": " + copy.evidenceScope + "\n\n")
		closeOptionalSection(&builder, config)
	}
	if config.ReactionFeedback {
		builder.WriteString("\n---\n\n**" + copy.feedbackQuestion + "** " + copy.feedbackDetail + "\n")
	}
	return strings.TrimSpace(builder.String())
}

func Failure(job domain.ProviderIssueAnalysisJob) string {
	return FailureWithLinks(job, FileLinkPolicy{})
}

func FailureWithLinks(job domain.ProviderIssueAnalysisJob, linkPolicy FileLinkPolicy) string {
	copy := issueTriageCopyFor(job)
	return fmt.Sprintf("## ⚠️ Open Review · %s\n\n"+copy.failureDetail+"\n\n%s%s", copy.failureTitle, job.Repository, job.IssueNumber, copy.failureBoundary, issueConsoleLink(job, copy, linkPolicy))
}

func issueConsoleLink(job domain.ProviderIssueAnalysisJob, copy issueTriageCopy, linkPolicy FileLinkPolicy) string {
	if link := linkPolicy.ConsoleAnalysis(job); link != "" {
		return "\n\n[" + copy.viewAnalysis + "](" + link + ")"
	}
	return ""
}

func writeSection(builder *strings.Builder, heading string, values []string, empty string) {
	builder.WriteString(heading + "\n\n")
	writeList(builder, values, empty)
}

func writeList(builder *strings.Builder, values []string, empty string) {
	if len(values) == 0 {
		builder.WriteString(empty + "\n\n")
		return
	}
	for _, value := range values {
		builder.WriteString("- " + value + "\n")
	}
	builder.WriteString("\n")
}

func writeAffectedAreas(builder *strings.Builder, job domain.ProviderIssueAnalysisJob, values []string, linkFiles, nested bool, copy issueTriageCopy, policy FileLinkPolicy) {
	heading := "### " + copy.affectedAreas
	if nested {
		heading = "#### " + copy.affectedAreas
	}
	builder.WriteString(heading + "\n\n")
	if len(values) == 0 {
		builder.WriteString(copy.noAffectedAreas + "\n\n")
		return
	}
	linked := false
	for _, value := range values {
		if linkFiles {
			withLink := linkedAffectedAreaWithPolicy(job, value, policy)
			linked = linked || withLink != value
			value = withLink
		}
		builder.WriteString("- " + value + "\n")
	}
	builder.WriteString("\n")
	if linked {
		builder.WriteString("_" + copy.fileLinkBoundary + "_\n\n")
	}
}

func hasResponseSection(config domain.IssueTriageConfig, wanted string) bool {
	for _, section := range config.ResponseSections {
		if section == wanted {
			return true
		}
	}
	return false
}

func openOptionalSection(builder *strings.Builder, config domain.IssueTriageConfig, title string) {
	if config.CollapseSecondary {
		builder.WriteString("<details>\n<summary><strong>" + title + "</strong></summary>\n\n")
	}
}

func closeOptionalSection(builder *strings.Builder, config domain.IssueTriageConfig) {
	if config.CollapseSecondary {
		builder.WriteString("</details>\n\n")
	}
}

func optionalHeading(config domain.IssueTriageConfig, title string) string {
	if config.CollapseSecondary {
		return "#### " + title
	}
	return "### " + title
}

func linkedAffectedArea(job domain.ProviderIssueAnalysisJob, value string) string {
	return linkedAffectedAreaWithPolicy(job, value, FileLinkPolicy{})
}

func linkedAffectedAreaWithPolicy(job domain.ProviderIssueAnalysisJob, value string, policy FileLinkPolicy) string {
	match := affectedFileReference.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 0 {
		return value
	}
	file := strings.TrimSpace(match[1])
	if file == "" || strings.HasPrefix(file, ".") || strings.Contains(file, "..") ||
		(!strings.Contains(file, "/") && !strings.Contains(file, ".") && file != "Dockerfile" && file != "Makefile") {
		return value
	}
	start, _ := strconv.Atoi(match[2])
	end, _ := strconv.Atoi(match[3])
	deepLink := policy.File(job, file, start, end)
	if deepLink == "" {
		return value
	}
	label := "`" + file
	if start > 0 {
		label += ":" + strconv.Itoa(start)
		if end > start {
			label += "-" + strconv.Itoa(end)
		}
	}
	label += "`"
	result := "[" + label + "](" + deepLink + ")"
	if description := strings.TrimSpace(match[4]); description != "" {
		result += " — " + description
	}
	return result
}

func providerFileDeepLink(job domain.ProviderIssueAnalysisJob, file string, start, end int) string {
	return (FileLinkPolicy{}).File(job, file, start, end)
}

func shortSHA(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
