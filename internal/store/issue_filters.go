package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type issuePredicateBuilder struct{ args []any }

func (b *issuePredicateBuilder) bind(value any) string {
	b.args = append(b.args, value)
	return fmt.Sprintf("$%d", len(b.args))
}

func issueListPredicate(tenantID uuid.UUID, actor string, filter domain.IssueFilter) (string, []any) {
	b := &issuePredicateBuilder{}
	conditions := []string{"review_issues.tenant_id = " + b.bind(tenantID)}
	for _, pair := range []struct{ column, value string }{
		{"status", string(filter.Status)}, {"severity", strings.ToLower(strings.TrimSpace(filter.Severity))},
		{"repository", strings.TrimSpace(filter.Repository)}, {"assignee_subject", strings.TrimSpace(filter.AssigneeSubject)},
		{"category", strings.ToLower(strings.TrimSpace(filter.Category))},
	} {
		if pair.value != "" {
			conditions = append(conditions, "review_issues."+pair.column+" = "+b.bind(pair.value))
		}
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		// Legacy query semantics stay unchanged for existing bookmarked inboxes.
		conditions = append(conditions, "lower(concat_ws(' ',body_preview,category,repository,path)) LIKE '%' || lower("+b.bind(query)+") || '%'")
	}
	if filter.SeenAfter != nil {
		conditions = append(conditions, "last_seen_at >= "+b.bind(*filter.SeenAfter))
	}
	if filter.ActiveOnly {
		conditions = append(conditions, "status IN ('open','regressed') AND active_occurrence_count > 0")
	}
	switch filter.View {
	case "open", "regressed", "resolved", "suppressed":
		conditions = append(conditions, "status = "+b.bind(filter.View))
	case "critical":
		conditions = append(conditions, "status IN ('open','regressed') AND active_occurrence_count > 0 AND severity='critical'")
	case "assigned":
		conditions = append(conditions, "status IN ('open','regressed') AND active_occurrence_count > 0 AND assignee_subject = "+b.bind(actor))
	}
	if filter.Filters != nil {
		conditions = append(conditions, b.expression(*filter.Filters, actor, *filter.FilterTime))
	}
	return strings.Join(conditions, " AND "), b.args
}

func (b *issuePredicateBuilder) expression(node domain.IssueFilterExpression, actor string, anchor time.Time) string {
	if node.Condition != "" {
		if len(node.Items) == 0 {
			return "TRUE"
		}
		parts := make([]string, len(node.Items))
		for i, item := range node.Items {
			parts[i] = b.expression(item, actor, anchor)
		}
		return "(" + strings.Join(parts, " "+strings.ToUpper(node.Condition)+" ") + ")"
	}
	negative := node.Operator == "is_not" || node.Operator == "not_contains" || node.Operator == "not_within"
	var predicate string
	if node.Field == "age" {
		predicate = "(review_issues.last_seen_at >= " + b.bind(anchor.Add(-domain.IssueFilterAgeDuration(node.Value))) + " AND review_issues.last_seen_at <= " + b.bind(anchor) + ")"
	} else {
		columns := map[string]string{"status": "status", "severity": "severity", "category": "category", "repository": "repository", "provider": "provider", "api_base_url": "api_base_url", "path": "path", "assignee": "assignee_subject"}
		column := "review_issues." + columns[node.Field]
		value := node.Value
		if node.Field == "assignee" {
			if value == "me" {
				value = actor
			} else {
				value = ""
			}
		}
		if node.Field == "query" {
			column = "concat_ws(' ',review_issues.body_preview,review_issues.category,review_issues.repository,review_issues.path)"
		}
		if node.Field == "rule" {
			column = "attribution.rule_key"
		}
		if node.Operator == "contains" || node.Operator == "not_contains" {
			value = "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(value) + "%"
			predicate = "lower(" + column + ") LIKE lower(" + b.bind(value) + ") ESCAPE E'\\\\'"
		} else if node.Field == "assignee" {
			// OIDC subjects are opaque case-sensitive identities.
			predicate = column + " = " + b.bind(value)
		} else {
			predicate = "lower(" + column + ") = lower(" + b.bind(value) + ")"
		}
		if node.Field == "rule" {
			predicate = "EXISTS (SELECT 1 FROM review_issue_occurrences occurrence JOIN review_finding_rule_attributions attribution ON attribution.finding_id=occurrence.finding_id WHERE occurrence.issue_id=review_issues.id AND " + predicate + ")"
		}
	}
	if negative {
		return "NOT (" + predicate + ")"
	}
	return "(" + predicate + ")"
}
