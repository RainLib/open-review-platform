package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const MaxIssueFilterBytes = 8192

// IssueFilterExpression is either an AND/OR group or a single typed predicate.
// Limits apply equally to API queries and persisted saved-view definitions.
type IssueFilterExpression struct {
	Condition string                  `json:"condition,omitempty"`
	Items     []IssueFilterExpression `json:"items,omitempty"`
	Field     string                  `json:"field,omitempty"`
	Operator  string                  `json:"operator,omitempty"`
	Value     string                  `json:"value,omitempty"`
}

// MarshalJSON preserves the explicit discriminated union on the wire, including
// items:[] for the useful empty AND root (never null or an omitted field).
func (expression IssueFilterExpression) MarshalJSON() ([]byte, error) {
	if expression.Condition != "" {
		items := expression.Items
		if items == nil {
			items = []IssueFilterExpression{}
		}
		return json.Marshal(struct {
			Condition string                  `json:"condition"`
			Items     []IssueFilterExpression `json:"items"`
		}{expression.Condition, items})
	}
	return json.Marshal(struct {
		Field    string `json:"field"`
		Operator string `json:"operator"`
		Value    string `json:"value"`
	}{expression.Field, expression.Operator, expression.Value})
}

func (expression *IssueFilterExpression) UnmarshalJSON(raw []byte) error {
	if len(raw) > MaxIssueFilterBytes {
		return errors.New("issue filters exceed the JSON limit")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if condition, group := fields["condition"]; group {
		items, present := fields["items"]
		if len(fields) != 2 || !present || bytes.Equal(bytes.TrimSpace(items), []byte("null")) {
			return errors.New("filter groups require only condition and an items array")
		}
		var node IssueFilterExpression
		if err := json.Unmarshal(condition, &node.Condition); err != nil || node.Condition == "" {
			return errors.New("invalid filter condition")
		}
		if err := json.Unmarshal(items, &node.Items); err != nil {
			return err
		}
		*expression = node
		return nil
	}
	if len(fields) != 3 {
		return errors.New("filter predicates require field, operator and value")
	}
	var node IssueFilterExpression
	for key, target := range map[string]*string{"field": &node.Field, "operator": &node.Operator, "value": &node.Value} {
		value, present := fields[key]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("filter predicates require string values")
		}
		if err := json.Unmarshal(value, target); err != nil {
			return err
		}
	}
	*expression = node
	return nil
}

func ParseIssueFilterExpression(raw string) (*IssueFilterExpression, error) {
	if len(raw) > MaxIssueFilterBytes || strings.TrimSpace(raw) == "" {
		return nil, errors.New("issue filters exceed the bounded JSON contract")
	}
	var expression IssueFilterExpression
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expression); err != nil {
		return nil, errors.New("issue filters are invalid JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || !expression.Valid() {
		return nil, errors.New("issue filters are invalid")
	}
	return &expression, nil
}

func (expression IssueFilterExpression) Valid() bool {
	if expression.Condition == "" {
		return false // The root is always a group, including the empty AND view.
	}
	encoded, err := json.Marshal(expression)
	if err != nil || len(encoded) > MaxIssueFilterBytes {
		return false
	}
	count := 0
	var visit func(IssueFilterExpression, int) bool
	visit = func(node IssueFilterExpression, depth int) bool {
		if depth > 3 {
			return false
		}
		if node.Condition != "" {
			if (node.Condition != "and" && node.Condition != "or") || node.Field != "" || node.Operator != "" || node.Value != "" || len(node.Items) > 20 || (len(node.Items) == 0 && (depth != 1 || node.Condition != "and")) {
				return false
			}
			for _, item := range node.Items {
				if !visit(item, depth+1) {
					return false
				}
			}
			return true
		}
		count++
		if node.Items != nil || count > 20 || node.Value == "" || strings.TrimSpace(node.Value) != node.Value || len(node.Value) > 512 || strings.ContainsRune(node.Value, '\x00') {
			return false
		}
		if node.Field == "age" {
			return (node.Operator == "within" || node.Operator == "not_within") && IssueFilterAgeDuration(node.Value) > 0
		}
		equality := node.Operator == "is" || node.Operator == "is_not"
		switch node.Field {
		case "status":
			return equality && IssueStatus(node.Value).Valid()
		case "severity":
			return equality && (node.Value == "critical" || node.Value == "high" || node.Value == "medium" || node.Value == "low")
		case "provider":
			return equality && Provider(node.Value).Valid()
		case "assignee":
			return equality && (node.Value == "me" || node.Value == "unassigned")
		case "category", "repository", "api_base_url", "path", "rule", "query":
			return equality || node.Operator == "contains" || node.Operator == "not_contains"
		default:
			return false
		}
	}
	return visit(expression, 1)
}

func (expression IssueFilterExpression) CanonicalJSON() []byte {
	encoded, _ := json.Marshal(expression)
	return bytes.TrimSpace(encoded)
}

func IssueFilterAgeDuration(value string) time.Duration {
	switch value {
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}
