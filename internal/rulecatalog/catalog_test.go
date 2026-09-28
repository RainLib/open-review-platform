package rulecatalog

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/rules"
)

func TestEntriesAreReleasePinnedAndValidated(t *testing.T) {
	entries := Entries()
	if len(entries) < 3 {
		t.Fatalf("catalog entry count = %d, want at least three", len(entries))
	}
	for _, entry := range entries {
		if entry.ID == "" || entry.Version == "" || len(entry.ContentSHA256) != 64 || len(entry.Rules) == 0 {
			t.Fatalf("entry lacks release provenance: %#v", entry)
		}
		if _, err := rules.Compile([]rules.Source{{VersionID: entry.ID + "@" + entry.Version, Rules: entry.Rules}}); err != nil {
			t.Fatalf("entry %s does not compile: %v", entry.ID, err)
		}
		resolved, ok := Find(entry.ID, entry.Version, entry.ContentSHA256)
		if !ok || resolved.ContentSHA256 != entry.ContentSHA256 {
			t.Fatalf("entry %s cannot be resolved by exact provenance", entry.ID)
		}
		if _, ok := Find(entry.ID, entry.Version, "different-content"); ok {
			t.Fatalf("entry %s resolved with a wrong digest", entry.ID)
		}
	}
}

func TestEntriesReturnsDefensiveCopies(t *testing.T) {
	first := Entries()
	first[0].Tags[0] = "mutated"
	first[0].Rules[0].Content[0] = 'x'
	second := Entries()
	if second[0].Tags[0] == "mutated" || second[0].Rules[0].Content[0] == 'x' {
		t.Fatal("catalog entry mutation leaked into subsequent reads")
	}
}
