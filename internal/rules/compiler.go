// Package rules compiles governance-controlled rule versions into a stable,
// runner-safe snapshot. It deliberately has no provider or database dependency
// so the same input always produces the same payload and SHA.
package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Enforcement string

const (
	Mandatory Enforcement = "mandatory"
	Advisory  Enforcement = "advisory"
)

type MergeBehavior string

const (
	Replace      MergeBehavior = "replace"
	Append       MergeBehavior = "append"
	DenyOverride MergeBehavior = "deny_override"
)

// Rule is the normalized, engine-neutral representation of one review rule.
// Content must be JSON so it can be handed to a trusted OCR rule file without
// interpolating provider-controlled text into a shell command.
type Rule struct {
	Key           string          `json:"key"`
	Enforcement   Enforcement     `json:"enforcement"`
	MergeBehavior MergeBehavior   `json:"merge_behavior"`
	Severity      string          `json:"severity"`
	Content       json.RawMessage `json:"content"`
	Disabled      bool            `json:"disabled,omitempty"`
}

// Source is one immutable published rule version at a governance precedence.
// Lower precedence is applied first. Equal precedence with the same key is an
// explicit conflict, never an implicit last-write-wins result.
type Source struct {
	VersionID  string
	Precedence int
	Rules      []Rule
	// Include and Exclude are trusted file-selection globs contributed by an
	// active binding. They are compiled into the immutable snapshot rather
	// than consulted again by the runner.
	Include []string
	Exclude []string
}

type EffectiveRule struct {
	Key           string          `json:"key"`
	SourceVersion string          `json:"source_version"`
	Enforcement   Enforcement     `json:"enforcement"`
	MergeBehavior MergeBehavior   `json:"merge_behavior"`
	Severity      string          `json:"severity"`
	Content       json.RawMessage `json:"content"`
}

type Snapshot struct {
	SchemaVersion   int             `json:"schema_version"`
	Engine          string          `json:"engine"`
	MergeSystemRule bool            `json:"merge_system_rule"`
	Rules           []EffectiveRule `json:"rules"`
	// Include and Exclude use OCR's native file-filter semantics: includes are
	// unioned and excludes always win. An empty Include means no additional
	// include restriction.
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

type Compiled struct {
	Snapshot  Snapshot
	Canonical []byte
	SHA256    string
}

// Compile validates and merges trusted published rule versions. A result can
// be serialized directly to a runner-owned file and is stable across process
// restarts, input ordering, and map iteration.
func Compile(sources []Source) (Compiled, error) {
	sources = append([]Source(nil), sources...)
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Precedence != sources[j].Precedence {
			return sources[i].Precedence < sources[j].Precedence
		}
		return sources[i].VersionID < sources[j].VersionID
	})

	byKey := make(map[string][]EffectiveRule)
	seenAtPrecedence := make(map[string]string)
	include := make(map[string]struct{})
	exclude := make(map[string]struct{})
	for _, source := range sources {
		if strings.TrimSpace(source.VersionID) == "" {
			return Compiled{}, fmt.Errorf("rule source version id is required")
		}
		source.VersionID = strings.TrimSpace(source.VersionID)
		for _, pattern := range source.Include {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				include[pattern] = struct{}{}
			}
		}
		for _, pattern := range source.Exclude {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				exclude[pattern] = struct{}{}
			}
		}
		local := make(map[string]struct{})
		for _, rule := range source.Rules {
			rule.Key = strings.TrimSpace(rule.Key)
			rule.Severity = strings.ToLower(strings.TrimSpace(rule.Severity))
			if err := validate(rule); err != nil {
				return Compiled{}, fmt.Errorf("rule source %s: %w", source.VersionID, err)
			}
			if _, duplicate := local[rule.Key]; duplicate {
				return Compiled{}, fmt.Errorf("rule source %s contains duplicate key %q", source.VersionID, rule.Key)
			}
			local[rule.Key] = struct{}{}
			precedenceKey := fmt.Sprintf("%d:%s", source.Precedence, rule.Key)
			if prior, collision := seenAtPrecedence[precedenceKey]; collision {
				return Compiled{}, fmt.Errorf("rule key %q conflicts at precedence %d between %s and %s", rule.Key, source.Precedence, prior, source.VersionID)
			}
			seenAtPrecedence[precedenceKey] = source.VersionID

			current := byKey[rule.Key]
			if rule.Disabled {
				if len(current) == 0 {
					continue
				}
				if hasProtected(current) {
					return Compiled{}, fmt.Errorf("rule %q is mandatory and cannot be disabled", rule.Key)
				}
				delete(byKey, rule.Key)
				continue
			}

			effective := EffectiveRule{Key: rule.Key, SourceVersion: source.VersionID, Enforcement: rule.Enforcement, MergeBehavior: rule.MergeBehavior, Severity: rule.Severity, Content: canonicalContent(rule.Content)}
			if len(current) == 0 {
				byKey[rule.Key] = []EffectiveRule{effective}
				continue
			}
			if hasProtected(current) {
				if rule.Enforcement != Mandatory || severityRank(rule.Severity) < protectedSeverity(current) {
					return Compiled{}, fmt.Errorf("rule %q is mandatory and can only be tightened", rule.Key)
				}
				if current[len(current)-1].MergeBehavior == Append || rule.MergeBehavior == Append {
					byKey[rule.Key] = append(current, effective)
				} else {
					byKey[rule.Key] = []EffectiveRule{effective}
				}
				continue
			}
			if rule.MergeBehavior == Append {
				byKey[rule.Key] = append(current, effective)
			} else {
				byKey[rule.Key] = []EffectiveRule{effective}
			}
		}
	}

	snapshot := Snapshot{SchemaVersion: 2, Engine: "ocr", MergeSystemRule: true, Include: sortedPatterns(include), Exclude: sortedPatterns(exclude)}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		snapshot.Rules = append(snapshot.Rules, byKey[key]...)
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return Compiled{}, fmt.Errorf("marshal canonical rule snapshot: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return Compiled{Snapshot: snapshot, Canonical: canonical, SHA256: hex.EncodeToString(digest[:])}, nil
}

func sortedPatterns(patterns map[string]struct{}) []string {
	if len(patterns) == 0 {
		return nil
	}
	result := make([]string, 0, len(patterns))
	for pattern := range patterns {
		result = append(result, pattern)
	}
	sort.Strings(result)
	return result
}

func validate(rule Rule) error {
	if rule.Key == "" {
		return fmt.Errorf("rule key is required")
	}
	if rule.Enforcement != Mandatory && rule.Enforcement != Advisory {
		return fmt.Errorf("rule %q has invalid enforcement", rule.Key)
	}
	if rule.MergeBehavior != Replace && rule.MergeBehavior != Append && rule.MergeBehavior != DenyOverride {
		return fmt.Errorf("rule %q has invalid merge behavior", rule.Key)
	}
	if severityRank(rule.Severity) == 0 {
		return fmt.Errorf("rule %q has invalid severity", rule.Key)
	}
	if !rule.Disabled && !json.Valid(rule.Content) {
		return fmt.Errorf("rule %q content must be valid JSON", rule.Key)
	}
	if rule.MergeBehavior == DenyOverride && rule.Enforcement != Mandatory {
		return fmt.Errorf("rule %q deny_override requires mandatory enforcement", rule.Key)
	}
	return nil
}

func canonicalContent(raw json.RawMessage) json.RawMessage {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return canonical
}

func hasProtected(rules []EffectiveRule) bool {
	for _, rule := range rules {
		if rule.Enforcement == Mandatory || rule.MergeBehavior == DenyOverride {
			return true
		}
	}
	return false
}

func protectedSeverity(rules []EffectiveRule) int {
	rank := 0
	for _, rule := range rules {
		if rule.Enforcement == Mandatory || rule.MergeBehavior == DenyOverride {
			rank = max(rank, severityRank(rule.Severity))
		}
	}
	return rank
}

func severityRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	default:
		return 0
	}
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
