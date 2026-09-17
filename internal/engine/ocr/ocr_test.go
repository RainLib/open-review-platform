package ocr

import (
	"os"
	"testing"
)

func TestParseFindingsNormalizesOCRComments(t *testing.T) {
	findings, err := ParseFindings([]byte(`{"comments":[{"path":"src/handler.go","content":"nil dereference","suggestion_code":"if value == nil { return }","start_line":8,"end_line":8,"severity":"high","category":"bug"},{"path":"../../etc/passwd","content":"unsafe path","severity":"unknown"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings", len(findings))
	}
	if findings[0].Path != "src/handler.go" || findings[0].Severity != "high" || findings[0].Suggestion == "" {
		t.Fatalf("unexpected first finding: %#v", findings[0])
	}
	if findings[1].Path != "" || findings[1].Severity != "medium" {
		t.Fatalf("expected unsafe path to lose inline location: %#v", findings[1])
	}
}

func TestWithGitBinaryPathPrependsConfiguredDirectory(t *testing.T) {
	environment := withGitBinaryPath([]string{"PATH=/usr/bin", "OTHER=value"}, "/opt/git/bin/git")
	if environment[0] != "PATH=/opt/git/bin"+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("unexpected PATH: %q", environment[0])
	}
	if environment[1] != "OTHER=value" {
		t.Fatalf("unrelated environment entry changed: %q", environment[1])
	}
}
