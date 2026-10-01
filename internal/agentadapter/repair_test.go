package agentadapter

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestBoundedRepairsReverifyEveryPatchAndStopOnInfrastructureFailures(t *testing.T) {
	for _, scenario := range []string{"repair succeeds", "budget exhausted", "infrastructure failure", "verifier changes patch", "repair escapes scope", "verifier changes metadata", "legacy no repairs", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			workspace := t.TempDir()
			for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-q", "-m", "base"}, {"switch", "-q", "-c", "agent/test-task"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = workspace
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, out)
				}
			}
			pipeline := Pipeline{AllowedPaths: []string{"README.md"}, MaxChangedFiles: 2, MaxDiffBytes: 4096}
			base, err := pipeline.git(context.Background(), workspace, noGitCredential, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			config, err := trustedGitConfig(workspace)
			if err != nil {
				t.Fatal(err)
			}
			var submission Submission
			submission.Task.BranchName = "agent/test-task"
			submission.Task.SourceBaseSHA = strings.TrimSpace(base)
			submission.Limits.Workflow = domain.AgentWorkflowPolicy{Enabled: true, MaxRepairCycles: 2, MaxTaskAttempts: 3}
			write := func(file, body string) {
				if err := os.WriteFile(filepath.Join(workspace, file), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("README.md", "broken change\n")
			files, size, sha, err := pipeline.validatePatch(context.Background(), workspace, submission)
			if err != nil {
				t.Fatal(err)
			}
			verifies, repairs := 0, 0
			pipeline.verifyCommand = func(_ context.Context, _ string, _ VerificationProfile) (VerificationEvidence, error) {
				verifies++
				switch scenario {
				case "infrastructure failure":
					return VerificationEvidence{}, errors.New("sandbox cleanup failed")
				case "verifier changes patch":
					write("README.md", "untrusted verifier edit\n")
				case "verifier changes metadata":
					write(".git/config", "tampered")
				}
				if scenario == "repair succeeds" && repairs == 1 {
					return VerificationEvidence{ProfileSHA256: strings.Repeat("b", 64), OutputSHA256: strings.Repeat("c", 64)}, nil
				}
				return VerificationEvidence{}, &verificationFailure{exit: 1, output: "assertion failed"}
			}
			pipeline.repairAgent = func(_ context.Context, _ string, _ Submission, diagnostics string) error {
				repairs++
				if !strings.Contains(diagnostics, "untrusted data") {
					t.Fatal("diagnostics lost trust boundary")
				}
				if scenario == "repair escapes scope" {
					write("outside.txt", "escaped\n")
				} else {
					write("README.md", "fixed change\n")
				}
				return nil
			}
			if scenario == "legacy no repairs" {
				submission.Limits.Workflow = domain.AgentWorkflowPolicy{}
			}
			ctx := context.Background()
			if scenario == "deadline" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			evidence, _, _, _, err := pipeline.verifyAndRepair(ctx, workspace, submission, testVerificationProfile(), config, files, size, sha)
			if scenario == "repair succeeds" {
				if err != nil || evidence == nil || verifies != 2 || repairs != 1 {
					t.Fatalf("repair loop: checks=%d repairs=%d evidence=%v err=%v", verifies, repairs, evidence, err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe/exhausted result accepted")
			}
			want := 0
			if scenario == "budget exhausted" {
				want = 2
			}
			if scenario == "repair escapes scope" {
				want = 1
			}
			if repairs != want {
				t.Fatalf("repair count=%d want=%d error=%v", repairs, want, err)
			}
		})
	}
}
