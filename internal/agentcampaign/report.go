package agentcampaign

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type Report struct {
	Schema           string                     `json:"schema"`
	GeneratedAt      time.Time                  `json:"generated_at"`
	EvidenceBoundary string                     `json:"evidence_boundary"`
	Detail           domain.AgentCampaignDetail `json:"detail"`
}

func Export(d domain.AgentCampaignDetail, format string, now time.Time) ([]byte, string, error) {
	d.Summarize()
	r := Report{Schema: "open-review.campaign-report.v1", GeneratedAt: now.UTC(), EvidenceBoundary: "Scan evidence covers eligible text files in the frozen path scope; excluded files are counted. Draft delivery, review and CI are distinct from human requirement acceptance. This report is a timestamped observation, not a merge or deployment receipt.", Detail: d}
	switch format {
	case "json":
		raw, err := json.MarshalIndent(r, "", "  ")
		return raw, "application/json", err
	case "csv":
		var b bytes.Buffer
		w := csv.NewWriter(&b)
		_ = w.Write([]string{"campaign", "repository", "status", "base_sha", "scan_complete", "files_scanned", "files_excluded", "matches", "task", "head_sha", "draft_url", "acceptance", "error", "request_sha256", "campaign_revision", "generated_at", "campaign_closed", "conclusion", "acceptance_reason", "fixed_verifier_criteria_passed", "execution_attempts_used", "execution_attempts_limit", "successful_deliveries", "attention_attempts", "decided_by", "decided_at"})
		for _, t := range d.Targets {
			task, head, link, acceptance := "", "", "", ""
			reason, verified, used, limit, delivered, attention, decidedBy, decidedAt := "", "", "", "", "", "", "", ""
			if t.Detail != nil {
				task = t.Detail.Task.ID.String()
				if a := t.Detail.Acceptance; a != nil {
					head = a.HeadSHA
					acceptance = a.State
					reason, verified, decidedBy = a.Reason, fmt.Sprint(domain.CriteriaVerified(a.Criteria, a.VerificationCriteria)), a.DecidedBy
					if a.DecidedAt != nil {
						decidedAt = a.DecidedAt.UTC().Format(time.RFC3339)
					}
				}
				if budget := t.Detail.ExecutionBudget; budget != nil {
					used, limit = fmt.Sprint(budget.Used), fmt.Sprint(budget.Limit)
					delivered, attention = fmt.Sprint(budget.SuccessfulDeliveries), fmt.Sprint(budget.AttentionAttempts)
				}
				for _, a := range t.Detail.Attempts {
					if a.PullRequestURL != "" {
						link = a.PullRequestURL
						break
					}
				}
			}
			row := []string{d.Campaign.ID.String(), t.Repository, t.State, t.Scan.BaseSHA, fmt.Sprint(t.Scan.Complete), fmt.Sprint(t.Scan.FilesScanned), fmt.Sprint(t.Scan.FilesExcluded), fmt.Sprint(t.Scan.Matches), task, head, link, acceptance, t.ErrorCode, d.Campaign.RequestSHA256, fmt.Sprint(d.Campaign.Revision), r.GeneratedAt.Format(time.RFC3339), fmt.Sprint(d.Summary.Closed), d.Summary.Conclusion, reason, verified, used, limit, delivered, attention, decidedBy, decidedAt}
			for i, s := range row {
				if strings.ContainsAny(strings.TrimLeft(s, " \t\r\n")[:min(1, len(strings.TrimLeft(s, " \t\r\n")))], "=+-@") {
					row[i] = "'" + s
				}
			}
			_ = w.Write(row)
		}
		w.Flush()
		return b.Bytes(), "text/csv; charset=utf-8", w.Error()
	case "markdown":
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\nGenerated: %s\n\nCampaign: `%s` · request digest: `%s` · revision: %d\n\n%s\n\n%s\n\n| Repository | Status | Scan | Matches | Evidence |\n| --- | --- | --- | --- | --- |\n", md(d.Campaign.Input.Title), r.GeneratedAt.Format(time.RFC3339), d.Campaign.ID, d.Campaign.RequestSHA256, d.Campaign.Revision, d.Summary.Conclusion, r.EvidenceBoundary)
		for _, t := range d.Targets {
			e := "base: " + t.Scan.BaseSHA
			if t.Detail != nil {
				e += "; task: " + t.Detail.Task.ID.String()
				if a := t.Detail.Acceptance; a != nil {
					e += "; acceptance head: " + a.HeadSHA + "; acceptance: " + a.State + "; reason: " + a.Reason + "; decision: " + a.Decision
					e += fmt.Sprintf("; fixed verifier criteria passed: %t", domain.CriteriaVerified(a.Criteria, a.VerificationCriteria))
					if a.DecidedAt != nil {
						e += "; decided by: " + a.DecidedBy + "; decided at: " + a.DecidedAt.UTC().Format(time.RFC3339)
					}
				}
				if budget := t.Detail.ExecutionBudget; budget != nil {
					e += fmt.Sprintf("; executions: %d/%d (delivered: %d, attention: %d)", budget.Used, budget.Limit, budget.SuccessfulDeliveries, budget.AttentionAttempts)
				}
				for _, a := range t.Detail.Attempts {
					if a.PullRequestURL != "" {
						e += "; Draft: " + a.PullRequestURL
						break
					}
				}
			}
			fmt.Fprintf(&b, "| %s | %s | complete=%t, read=%d, excluded=%d | %d | %s |\n", md(t.Repository), md(t.State), t.Scan.Complete, t.Scan.FilesScanned, t.Scan.FilesExcluded, t.Scan.Matches, md(e))
			if t.ErrorCode != "" {
				fmt.Fprintf(&b, "\n%s: `%s`\n\n", md(t.Repository), md(t.ErrorCode))
			}
		}
		fmt.Fprintf(&b, "\n## Frozen requirements\n\n%s\n\n", md(d.Campaign.Input.Requirements))
		for _, criterion := range d.Campaign.Input.AcceptanceCriteria {
			fmt.Fprintf(&b, "- %s\n", md(criterion))
		}
		return []byte(b.String()), "text/markdown; charset=utf-8", nil
	default:
		return nil, "", fmt.Errorf("report format must be json, csv or markdown")
	}
}
func md(s string) string {
	return strings.NewReplacer("|", "\\|", "<", "&lt;", ">", "&gt;", "\r", " ", "\n", " ").Replace(s)
}
