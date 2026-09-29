// Package rulecatalog owns the release-bundled Open Review policy catalog.
//
// The catalog is deliberately local to the control-plane release: it never
// downloads provider content or turns repository text into policy. Every entry
// has a deterministic payload digest and is validated through the same OCR
// adapter used for customer-authored rules before it can be installed.
package rulecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/RainLib/open-review-platform/internal/rules"
)

const Origin = "open-review://catalog/core"

// Entry is a release-pinned template. Rules are intentionally available only
// to the control plane; browser clients receive the public metadata and must
// name the exact version and digest to install it.
type Entry struct {
	ID            string
	Version       string
	Title         string
	Description   string
	Tags          []string
	Rules         []rules.Rule
	ContentSHA256 string
}

var entries = mustBuild([]Entry{
	{
		ID:          "core.webhook-trust-boundary",
		Version:     "1.0.0",
		Title:       "Webhook trust boundary",
		Description: "Protect callback ingestion before untrusted provider input can create work or alter state.",
		Tags:        []string{"security", "webhooks", "trust-boundary"},
		Rules: []rules.Rule{{
			Key:           "security.webhook-trust-boundary",
			Enforcement:   rules.Mandatory,
			MergeBehavior: rules.DenyOverride,
			Severity:      "critical",
			Content:       json.RawMessage(`{"prompt":"For any changed webhook, callback, or external-event ingress, verify authentication or signature before parsing trusted fields, recording a side effect, enqueueing work, or making a provider call. Enforce a bounded payload size and preserve a safe reject path. Report only concrete regressions in the changed code."}`),
		}},
	},
	{
		ID:          "core.durable-idempotency",
		Version:     "1.0.0",
		Title:       "Durable idempotency",
		Description: "Keep retried deliveries and asynchronous workers from duplicating externally visible effects.",
		Tags:        []string{"reliability", "queues", "idempotency"},
		Rules: []rules.Rule{{
			Key:           "reliability.durable-idempotency",
			Enforcement:   rules.Mandatory,
			MergeBehavior: rules.DenyOverride,
			Severity:      "high",
			Content:       json.RawMessage(`{"prompt":"For changed asynchronous work, retries, outbox/inbox handling, or provider writes, require a stable idempotency key and a durable state transition that prevents duplicate effects. Check that retries are safe after process failure and that a stale worker cannot finalize a newer attempt."}`),
		}},
	},
	{
		ID:          "core.error-contracts",
		Version:     "1.0.0",
		Title:       "Explicit error contracts",
		Description: "Keep validation, authorization, conflict, and transient failures distinguishable at service boundaries.",
		Tags:        []string{"maintainability", "api", "error-handling"},
		Rules: []rules.Rule{{
			Key:           "quality.explicit-error-contracts",
			Enforcement:   rules.Advisory,
			MergeBehavior: rules.Replace,
			Severity:      "medium",
			Content:       json.RawMessage(`{"prompt":"For changed API, worker, or persistence boundaries, preserve typed or sentinel error semantics for validation, authorization, not-found, conflict, and transient failure. Do not classify an error by matching its display text when a stable contract is available. Flag only a changed path that can return the wrong status, retry behavior, or caller guidance."}`),
		}},
	},
})

// Entries returns a defensive copy ordered by stable catalog identifier.
func Entries() []Entry {
	result := make([]Entry, len(entries))
	for index, entry := range entries {
		result[index] = clone(entry)
	}
	return result
}

// Find resolves only one exact release-pinned catalog payload. Callers must
// supply the digest received from Entries so a stale browser cannot install a
// different template under the same friendly name.
func Find(id, version, contentSHA256 string) (Entry, bool) {
	for _, entry := range entries {
		if entry.ID == id && entry.Version == version && entry.ContentSHA256 == contentSHA256 {
			return clone(entry), true
		}
	}
	return Entry{}, false
}

func mustBuild(source []Entry) []Entry {
	result := make([]Entry, 0, len(source))
	seen := make(map[string]struct{}, len(source))
	for _, entry := range source {
		if entry.ID == "" || entry.Version == "" || entry.Title == "" || entry.Description == "" || len(entry.Rules) == 0 {
			panic("invalid built-in rule catalog entry")
		}
		identity := entry.ID + "@" + entry.Version
		if _, duplicate := seen[identity]; duplicate {
			panic("duplicate built-in rule catalog entry: " + identity)
		}
		seen[identity] = struct{}{}
		compiled, err := rules.Compile([]rules.Source{{VersionID: identity, Rules: entry.Rules}})
		if err != nil {
			panic(fmt.Sprintf("compile built-in rule catalog entry %s: %v", identity, err))
		}
		if _, err := rules.OCRRuleFileForSnapshot(compiled.Snapshot); err != nil {
			panic(fmt.Sprintf("validate built-in rule catalog entry %s for OCR: %v", identity, err))
		}
		canonicalRules, err := json.Marshal(entry.Rules)
		if err != nil {
			panic(fmt.Sprintf("encode built-in rule catalog entry %s: %v", identity, err))
		}
		digest := sha256.Sum256(canonicalRules)
		entry.ContentSHA256 = hex.EncodeToString(digest[:])
		result = append(result, clone(entry))
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func clone(entry Entry) Entry {
	entry.Tags = append([]string(nil), entry.Tags...)
	entry.Rules = append([]rules.Rule(nil), entry.Rules...)
	for index := range entry.Rules {
		entry.Rules[index].Content = append(json.RawMessage(nil), entry.Rules[index].Content...)
	}
	return entry
}
