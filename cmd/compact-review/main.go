// Compact review is a development-only process supervisor. Production keeps
// workers in separate containers with independent credentials and scaling.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var reviewRoles = []string{
	"outbox-relay",
	"acknowledger",
	"interaction-responder",
	"interaction-admitter",
	"terminal-reporter",
	"issue-publisher",
	"issue-triager",
	"model-prober",
	"provider-prober",
	"provider-feedback-poller",
	"runner",
	"review-scheduler",
	"rule-exception-expirer",
	"rule-rollout-monitor",
	"sso-prober",
}

// The compact supervisor is development-only. Production runs optional roles
// with separate credentials and scaling; enabling them here must be deliberate.
var notificationCredentialNames = []string{
	"OPENREVIEW_NOTIFY_PLATFORM",
	"OPENREVIEW_NOTIFY_ENGINEERING",
	"OPENREVIEW_NOTIFY_SECURITY",
	"OPENREVIEW_NOTIFY_INCIDENTS",
	"OPENREVIEW_NOTIFY_RELEASES",
	"OPENREVIEW_NOTIFY_PRODUCT",
}

type childExit struct {
	index int
	err   error
}

func main() {
	if !compactAllowed(os.Getenv) {
		log.Fatal("compact review is restricted to explicit development deployments")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	roles, err := selectedRoles(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if err := run(ctx, "/app", roles); err != nil {
		log.Fatal(err)
	}
}

func compactAllowed(getenv func(string) string) bool {
	return getenv("ENVIRONMENT") == "development" && getenv("OPEN_REVIEW_COMPACT_REVIEW") == "true"
}

func selectedRoles(getenv func(string) string) ([]string, error) {
	roles := append([]string(nil), reviewRoles...)
	switch getenv("OPEN_REVIEW_COMPACT_NOTIFICATIONS") {
	case "", "false":
	case "true":
		roles = append(roles, "notifier")
	default:
		return nil, fmt.Errorf("OPEN_REVIEW_COMPACT_NOTIFICATIONS must be true or false")
	}
	switch getenv("OPEN_REVIEW_COMPACT_DATA_GOVERNANCE") {
	case "", "false":
	case "true":
		if strings.TrimSpace(getenv("DATA_GOVERNANCE_ARTIFACT_KEY")) == "" {
			return nil, fmt.Errorf("DATA_GOVERNANCE_ARTIFACT_KEY is required when compact data governance is enabled")
		}
		roles = append(roles, "data-governance-worker")
	default:
		return nil, fmt.Errorf("OPEN_REVIEW_COMPACT_DATA_GOVERNANCE must be true or false")
	}
	switch getenv("OPEN_REVIEW_COMPACT_AGENT_SOURCE") {
	case "", "false":
	case "true":
		if strings.TrimSpace(getenv("AGENT_DECISION_JEV_API_KEY")) == "" {
			return nil, fmt.Errorf("AGENT_DECISION_JEV_API_KEY is required when compact Agent source admission is enabled")
		}
		roles = append(roles, "agent-task-source-admitter")
	default:
		return nil, fmt.Errorf("OPEN_REVIEW_COMPACT_AGENT_SOURCE must be true or false")
	}
	return roles, nil
}

func workerEnvironment(role string, environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if role != "notifier" {
			notificationSecret := false
			for _, credential := range notificationCredentialNames {
				if name == credential {
					notificationSecret = true
					break
				}
			}
			if notificationSecret {
				continue
			}
		}
		if role != "data-governance-worker" && strings.HasPrefix(name, "DATA_GOVERNANCE_") {
			continue
		}
		if role != "agent-task-source-admitter" && strings.HasPrefix(name, "AGENT_DECISION_JEV_") {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

// A failed worker makes the entire local bundle unhealthy. Docker restarts the
// complete set instead of leaving an apparently live but partially inert review
// pipeline. Each child has its own process group so OCR/git descendants are
// stopped along with the runner during a bundle restart.
func run(ctx context.Context, binaryDirectory string, roles []string) error {
	if len(roles) == 0 {
		return errors.New("compact review has no worker roles")
	}
	children := make([]*exec.Cmd, 0, len(roles))
	exits := make(chan childExit, len(roles))
	for _, role := range roles {
		command := exec.Command(filepath.Join(binaryDirectory, role))
		command.Env = workerEnvironment(role, os.Environ())
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := command.Start(); err != nil {
			shutdown(children, nil, exits)
			return fmt.Errorf("start %s: %w", role, err)
		}
		index := len(children)
		children = append(children, command)
		go func() { exits <- childExit{index: index, err: command.Wait()} }()
		log.Printf("compact review started %s pid=%d", role, command.Process.Pid)
	}

	select {
	case <-ctx.Done():
		shutdown(children, nil, exits)
		return nil
	case first := <-exits:
		shutdown(children, &first, exits)
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("worker %s exited unexpectedly: %v", roles[first.index], first.err)
	}
}

func shutdown(children []*exec.Cmd, first *childExit, exits <-chan childExit) {
	completed := make([]bool, len(children))
	remaining := len(children)
	if first != nil {
		completed[first.index] = true
		remaining--
	}
	// A worker may exit before one of its git/OCR descendants. Its leader can
	// no longer coordinate cleanup, so kill that orphaned group immediately;
	// living workers still receive a graceful TERM before the bounded KILL.
	for index, command := range children {
		if command.Process != nil {
			signal := syscall.SIGTERM
			if completed[index] {
				signal = syscall.SIGKILL
			}
			_ = syscall.Kill(-command.Process.Pid, signal)
		}
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for remaining > 0 {
		select {
		case result := <-exits:
			if !completed[result.index] {
				completed[result.index] = true
				remaining--
			}
		case <-deadline.C:
			for _, command := range children {
				if command.Process != nil {
					_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				}
			}
			for remaining > 0 {
				result := <-exits
				if !completed[result.index] {
					completed[result.index] = true
					remaining--
				}
			}
		}
	}
}
