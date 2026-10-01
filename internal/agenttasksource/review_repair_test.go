package agenttasksource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestInternalReviewRepairStillRechecksDraftHeadAndTarget(t *testing.T) {
	head := strings.Repeat("a", 40)
	branch := "agent/" + uuid.NewString()
	runID := uuid.New()
	instruction := "Repair the bounded findings while retaining every original acceptance criterion."
	digest := sha256.Sum256([]byte(instruction))
	binding := domain.AgentTaskFeedbackBinding{SourceReviewRunID: &runID, SystemInstruction: instruction, CommentExternalID: "review:" + runID.String(), ActorExternalID: "system:review-worker", InstructionSHA256: hex.EncodeToString(digest[:]), TargetBranch: "main"}
	observed, base := head, "main"
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.URL.Path != "/projects/team/repo/merge_requests/4" {
			t.Errorf("unexpected provider operation %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad request", 400)
			return
		}
		fmt.Fprintf(w, `{"draft":true,"source_branch":%q,"target_branch":%q,"sha":%q}`, branch, base, observed)
	}))
	defer server.Close()
	task := domain.AgentTask{ID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: server.URL, Repository: "team/repo", OriginKind: "pull_request", OriginNumber: 4, OriginRevision: head, ExecutionBranch: branch}
	resolver := Resolver{Resolver: staticResolver("fixture-read"), AllowGitLabHTTP: true}
	snapshot, err := resolver.Resolve(context.Background(), domain.AgentTaskSourceTarget{Task: task, CredentialRef: "fixture", InstallationExternalID: "fixture", Feedback: &binding})
	if err != nil || snapshot.Feedback == nil || snapshot.Feedback.Instruction != instruction || reads != 1 {
		t.Fatalf("internal repair source: %+v %v reads=%d", snapshot, err, reads)
	}
	if err = resolver.VerifyOriginWithFeedback(context.Background(), task, &binding, "fixture-read"); err != nil {
		t.Fatal(err)
	}
	observed = strings.Repeat("b", 40)
	if err = resolver.VerifyOriginWithFeedback(context.Background(), task, &binding, "fixture-read"); err == nil {
		t.Fatal("moved Draft accepted")
	}
	observed = head
	base = "release"
	if err = resolver.VerifyOriginWithFeedback(context.Background(), task, &binding, "fixture-read"); err == nil {
		t.Fatal("changed target accepted")
	}
	base = "main"
	binding.SystemInstruction += " widen scope"
	if binding.Valid() {
		t.Fatal("tampered review diagnostics accepted")
	}
	if err = resolver.VerifyOriginWithFeedback(context.Background(), task, &binding, "fixture-read"); err == nil {
		t.Fatal("unsigned instruction accepted")
	}
}
