package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCompactModeRequiresExplicitDevelopmentOptIn(t *testing.T) {
	for _, values := range []map[string]string{
		{"ENVIRONMENT": "production", "OPEN_REVIEW_COMPACT_REVIEW": "true"},
		{"ENVIRONMENT": "development"},
		{"OPEN_REVIEW_COMPACT_REVIEW": "true"},
	} {
		if compactAllowed(func(name string) string { return values[name] }) {
			t.Fatalf("allowed unsafe configuration: %v", values)
		}
	}
	if !compactAllowed(func(name string) string {
		return map[string]string{"ENVIRONMENT": "development", "OPEN_REVIEW_COMPACT_REVIEW": "true"}[name]
	}) {
		t.Fatal("explicit development opt-in rejected")
	}
}

func TestCompactReviewIncludesIssueFeedbackOnce(t *testing.T) {
	seen := make(map[string]bool, len(reviewRoles))
	for _, role := range reviewRoles {
		if seen[role] {
			t.Fatalf("duplicate compact worker role %q", role)
		}
		seen[role] = true
	}
	for _, required := range []string{"issue-triager", "model-prober", "provider-feedback-poller", "review-scheduler", "rule-exception-expirer", "rule-rollout-monitor", "sso-prober"} {
		if !seen[required] {
			t.Fatalf("compact mode lacks %s: %v", required, reviewRoles)
		}
	}
}

func TestCompactNotificationsRequireExplicitOptIn(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		roles, err := selectedRoles(func(name string) string {
			if name == "OPEN_REVIEW_COMPACT_NOTIFICATIONS" {
				return value
			}
			return ""
		})
		if err != nil {
			t.Fatalf("select roles for %q: %v", value, err)
		}
		wantNotifier := value == "true"
		gotNotifier := roles[len(roles)-1] == "notifier"
		if gotNotifier != wantNotifier {
			t.Fatalf("value %q: notifier selected=%t", value, gotNotifier)
		}
	}
	if _, err := selectedRoles(func(string) string { return "yes" }); err == nil {
		t.Fatal("ambiguous notifier opt-in accepted")
	}
}

func TestCompactDataGovernanceRequiresExplicitOptInAndArtifactKey(t *testing.T) {
	for _, value := range []string{"", "false"} {
		roles, err := selectedRoles(func(name string) string {
			if name == "OPEN_REVIEW_COMPACT_DATA_GOVERNANCE" {
				return value
			}
			return ""
		})
		if err != nil || len(roles) != len(reviewRoles) {
			t.Fatalf("data governance enabled for %q: roles=%v err=%v", value, roles, err)
		}
	}
	if _, err := selectedRoles(func(name string) string {
		if name == "OPEN_REVIEW_COMPACT_DATA_GOVERNANCE" {
			return "true"
		}
		return ""
	}); err == nil {
		t.Fatal("data governance accepted without an artifact key")
	}
	if _, err := selectedRoles(func(name string) string {
		if name == "OPEN_REVIEW_COMPACT_DATA_GOVERNANCE" {
			return "yes"
		}
		return ""
	}); err == nil {
		t.Fatal("ambiguous data-governance opt-in accepted")
	}
	roles, err := selectedRoles(func(name string) string {
		return map[string]string{
			"OPEN_REVIEW_COMPACT_NOTIFICATIONS":   "true",
			"OPEN_REVIEW_COMPACT_DATA_GOVERNANCE": "true",
			"DATA_GOVERNANCE_ARTIFACT_KEY":        "test-key",
		}[name]
	})
	if err != nil || len(roles) != len(reviewRoles)+2 || roles[len(roles)-2] != "notifier" || roles[len(roles)-1] != "data-governance-worker" {
		t.Fatalf("explicit optional roles not selected: roles=%v err=%v", roles, err)
	}
}

func TestCompactAgentSourceRequiresExplicitOptInAndJevKey(t *testing.T) {
	for _, value := range []string{"", "false"} {
		roles, err := selectedRoles(func(name string) string {
			if name == "OPEN_REVIEW_COMPACT_AGENT_SOURCE" {
				return value
			}
			return ""
		})
		if err != nil || len(roles) != len(reviewRoles) {
			t.Fatalf("Agent source enabled for %q: roles=%v err=%v", value, roles, err)
		}
	}
	if _, err := selectedRoles(func(name string) string {
		if name == "OPEN_REVIEW_COMPACT_AGENT_SOURCE" {
			return "true"
		}
		return ""
	}); err == nil {
		t.Fatal("Agent source accepted without a Jev key")
	}
	if _, err := selectedRoles(func(name string) string {
		if name == "OPEN_REVIEW_COMPACT_AGENT_SOURCE" {
			return "yes"
		}
		return ""
	}); err == nil {
		t.Fatal("ambiguous Agent source opt-in accepted")
	}
	roles, err := selectedRoles(func(name string) string {
		return map[string]string{
			"OPEN_REVIEW_COMPACT_NOTIFICATIONS":   "true",
			"OPEN_REVIEW_COMPACT_DATA_GOVERNANCE": "true",
			"DATA_GOVERNANCE_ARTIFACT_KEY":        "test-key",
			"OPEN_REVIEW_COMPACT_AGENT_SOURCE":    "true",
			"AGENT_DECISION_JEV_API_KEY":          "test-jev-key",
		}[name]
	})
	if err != nil || len(roles) != len(reviewRoles)+3 || roles[len(roles)-1] != "agent-task-source-admitter" {
		t.Fatalf("explicit Agent source role not selected: roles=%v err=%v", roles, err)
	}
}

func TestCompactWorkersDoNotInheritNotifierCredentials(t *testing.T) {
	environment := []string{
		"CONTROL_DATABASE_URL=postgres://local",
		"OPENREVIEW_NOTIFY_PLATFORM=secret-webhook",
		"OPENREVIEW_NOTIFY_SECURITY=secret-security-webhook",
		"NOTIFIER_WORKER_ID=notifier-test",
	}
	for _, role := range []string{"runner", "issue-triager", "terminal-reporter"} {
		filtered := workerEnvironment(role, environment)
		if len(filtered) != 2 || filtered[0] != environment[0] || filtered[1] != environment[3] {
			t.Fatalf("%s inherited notification credentials: %v", role, filtered)
		}
	}
	if got := workerEnvironment("notifier", environment); len(got) != len(environment) {
		t.Fatalf("notifier lost its configured credentials: %v", got)
	}
}

func TestCompactWorkersDoNotInheritGovernanceCredentials(t *testing.T) {
	environment := []string{
		"CONTROL_DATABASE_URL=postgres://local",
		"DATA_GOVERNANCE_ARTIFACT_KEY=test-key",
		"DATA_GOVERNANCE_REGION_ORCHESTRATOR_SECRET=test-secret",
		"OPENREVIEW_NOTIFY_SECURITY=secret-webhook",
	}
	for _, role := range []string{"runner", "issue-triager", "sso-prober"} {
		filtered := workerEnvironment(role, environment)
		if len(filtered) != 1 || filtered[0] != environment[0] {
			t.Fatalf("%s inherited optional credentials: %v", role, filtered)
		}
	}
	if filtered := workerEnvironment("notifier", environment); len(filtered) != 2 || filtered[0] != environment[0] || filtered[1] != environment[3] {
		t.Fatalf("notifier inherited governance credentials: %v", filtered)
	}
	if filtered := workerEnvironment("data-governance-worker", environment); len(filtered) != 3 || filtered[0] != environment[0] || filtered[1] != environment[1] || filtered[2] != environment[2] {
		t.Fatalf("governance worker lost its credential or inherited notifier credential: %v", filtered)
	}
}

func TestCompactWorkersDoNotInheritJevCredentials(t *testing.T) {
	environment := []string{
		"CONTROL_DATABASE_URL=postgres://local",
		"AGENT_DECISION_JEV_API_KEY=test-jev-key",
		"AGENT_DECISION_JEV_URL=https://example.test/jev",
		"OPENREVIEW_NOTIFY_SECURITY=test-notification-key",
		"DATA_GOVERNANCE_ARTIFACT_KEY=test-artifact-key",
	}
	for _, role := range []string{"runner", "issue-triager", "notifier", "data-governance-worker"} {
		for _, item := range workerEnvironment(role, environment) {
			if strings.HasPrefix(item, "AGENT_DECISION_JEV_") {
				t.Fatalf("%s inherited Jev configuration", role)
			}
		}
	}
	filtered := workerEnvironment("agent-task-source-admitter", environment)
	if len(filtered) != 3 || filtered[0] != environment[0] || filtered[1] != environment[1] || filtered[2] != environment[2] {
		t.Fatal("Agent source lost Jev configuration or inherited unrelated secrets")
	}
}

func TestWorkerExitFailsBundleAndStopsSiblings(t *testing.T) {
	directory := t.TempDir()
	writeWorker(t, directory, "waiter", "exec sleep 30")
	writeWorker(t, directory, "stopped", "exit 0")
	started := time.Now()
	err := run(context.Background(), directory, []string{"waiter", "stopped"})
	if err == nil || !strings.Contains(err.Error(), "stopped exited unexpectedly") {
		t.Fatalf("expected fail-closed worker exit, got %v", err)
	}
	// Shutdown has a ten-second graceful deadline before SIGKILL. Allow
	// scheduler overhead when the full Go suite runs packages concurrently.
	if time.Since(started) > 15*time.Second {
		t.Fatal("sibling exceeded the bounded shutdown deadline")
	}
}

func TestExitedWorkerCannotLeaveProcessGroupDescendant(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "descendant.pid")
	t.Setenv("COMPACT_TEST_CHILD_PID", marker)
	writeWorker(t, directory, "leaky", `(trap '' TERM; exec sleep 30) & printf '%s\n' "$!" > "$COMPACT_TEST_CHILD_PID"`)

	err := run(context.Background(), directory, []string{"leaky"})
	if err == nil || !strings.Contains(err.Error(), "leaky exited unexpectedly") {
		t.Fatalf("expected failed worker to stop the bundle, got %v", err)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read descendant pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || pid < 1 {
		t.Fatalf("invalid descendant pid %q: %v", content, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) || linuxProcessState(pid) == "Z" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("exited worker left descendant pid %d alive (state %q)", pid, linuxProcessState(pid))
}

// An orphan killed by the supervisor can remain as a zombie until the
// container's PID 1 exits. It cannot execute code or keep a provider write
// alive, so the process-level assertion treats Z as stopped, not running.
func linuxProcessState(pid int) string {
	content, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				return fields[1]
			}
		}
	}
	return ""
}

func TestCancellationStopsBundle(t *testing.T) {
	directory := t.TempDir()
	writeWorker(t, directory, "waiter", "exec sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if err := run(ctx, directory, []string{"waiter"}); err != nil {
		t.Fatalf("cancel bundle: %v", err)
	}
}

func writeWorker(t *testing.T, directory, name, command string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+command+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
