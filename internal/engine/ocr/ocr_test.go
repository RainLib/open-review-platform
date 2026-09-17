package ocr

import "testing"

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
