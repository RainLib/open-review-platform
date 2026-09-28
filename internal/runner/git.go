package runner

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/domain"
)

type Workspace struct {
	Path    string
	BaseSHA string
	cleanup func() error
}

func (w *Workspace) Close() error {
	if w == nil || w.cleanup == nil {
		return nil
	}
	return w.cleanup()
}

type Checkout struct {
	Resolver    credentials.Resolver
	GitBinary   string
	GitHubToken string
	GitLabToken string
}

// maxGitCommandOutputBytes bounds diagnostics emitted by an untrusted Git
// server or a local transport. Clone/fetch output is never part of review
// input, and the SHA-producing commands are expected to be only a few bytes.
// A hard cap keeps a malformed endpoint from exhausting the runner process.
const maxGitCommandOutputBytes = 64 * 1024

// boundedOutput intentionally reports every write as accepted so os/exec can
// drain a child process even after the diagnostic budget is exhausted. stdout
// and stderr can be copied concurrently, therefore the buffer is protected.
type boundedOutput struct {
	mu        sync.Mutex
	value     []byte
	limit     int
	truncated bool
}

func newBoundedOutput(limit int) *boundedOutput {
	return &boundedOutput{limit: limit}
}

func (output *boundedOutput) Write(value []byte) (int, error) {
	originalLength := len(value)
	output.mu.Lock()
	defer output.mu.Unlock()

	remaining := output.limit - len(output.value)
	if remaining <= 0 {
		output.truncated = output.truncated || originalLength > 0
		return originalLength, nil
	}
	if len(value) > remaining {
		output.value = append(output.value, value[:remaining]...)
		output.truncated = true
		return originalLength, nil
	}
	output.value = append(output.value, value...)
	return originalLength, nil
}

func (output *boundedOutput) bytes() ([]byte, bool) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]byte(nil), output.value...), output.truncated
}

func (c Checkout) Prepare(ctx context.Context, job domain.ReviewJob) (*Workspace, error) {
	token, err := c.token(ctx, job)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "open-review-")
	if err != nil {
		return nil, fmt.Errorf("create isolated workspace: %w", err)
	}
	fail := func(err error) (*Workspace, error) {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	cloneURL := authenticatedCloneURL(job, token)
	if err := c.git(ctx, "", token, "clone", "--no-checkout", "--depth=1", cloneURL, directory); err != nil {
		return fail(fmt.Errorf("clone review repository: %w", err))
	}
	if err := c.git(ctx, directory, token, "fetch", "--depth=1", "origin", job.BaseRef); err != nil {
		return fail(fmt.Errorf("fetch base ref: %w", err))
	}
	baseSHA, err := c.gitOutput(ctx, directory, token, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return fail(fmt.Errorf("resolve base SHA: %w", err))
	}
	if job.BaseSHA != "" {
		baseSHA = job.BaseSHA
	}
	if err := c.git(ctx, directory, token, "fetch", "--depth=1", "origin", job.HeadSHA); err != nil {
		return fail(fmt.Errorf("fetch head SHA: %w", err))
	}
	if err := c.git(ctx, directory, token, "checkout", "--detach", job.HeadSHA); err != nil {
		return fail(fmt.Errorf("checkout head SHA: %w", err))
	}
	// OCR calculates the diff through git merge-base. Fetching the base and head
	// as independent depth-one objects makes both commits shallow roots, so Git
	// cannot prove their ancestry even for an ordinary pull request. Deepen only
	// when necessary, then fall back to a complete history for long-lived PRs.
	if err := c.ensureMergeBase(ctx, directory, token, strings.TrimSpace(baseSHA), job.HeadSHA, job.BaseRef); err != nil {
		return fail(err)
	}
	mergeBase, err := c.gitOutput(ctx, directory, token, "merge-base", strings.TrimSpace(baseSHA), job.HeadSHA)
	if err != nil {
		return fail(fmt.Errorf("resolve review diff base: %w", err))
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if mergeBase == "" {
		return fail(fmt.Errorf("resolve review diff base: git returned an empty merge base"))
	}
	// Provider pull-request diffs use three-dot semantics. Downstream risk
	// planning and OCR compare this common ancestor with the head commit so a
	// base branch that advanced after the feature branched cannot introduce
	// unrelated, base-only files into the review.
	return &Workspace{Path: directory, BaseSHA: mergeBase, cleanup: func() error { return os.RemoveAll(directory) }}, nil
}

// GitHub webhooks commonly carry an SSH clone URL. Installation tokens only
// authenticate HTTP(S) transport, so normalize that URL before starting Git;
// otherwise a runner can wait on blocked SSH port 22 despite holding a valid
// GitHub App token. HTTPS, file fixtures, and other providers remain intact.
func authenticatedCloneURL(job domain.ReviewJob, token string) string {
	if job.Provider != domain.ProviderGitHub || token == "" {
		return job.CloneURL
	}
	if parsed, err := url.Parse(job.CloneURL); err == nil && parsed.Scheme == "ssh" && parsed.Host != "" {
		return "https://" + parsed.Hostname() + "/" + strings.TrimPrefix(parsed.Path, "/")
	}
	if !strings.HasPrefix(job.CloneURL, "git@") {
		return job.CloneURL
	}
	parts := strings.SplitN(strings.TrimPrefix(job.CloneURL, "git@"), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return job.CloneURL
	}
	return "https://" + parts[0] + "/" + strings.TrimPrefix(parts[1], "/")
}

func (c Checkout) ensureMergeBase(ctx context.Context, directory, token, baseSHA, headSHA, baseRef string) error {
	if baseSHA == "" || headSHA == "" {
		return fmt.Errorf("resolve review merge base: base and head SHAs are required")
	}
	if _, err := c.gitOutput(ctx, directory, token, "merge-base", baseSHA, headSHA); err == nil {
		return nil
	}

	// Most PRs resolve after a small deepening. The bounded sequence prevents a
	// single retry from unexpectedly downloading a large repository history.
	for _, depth := range []int{64, 256, 1024, 4096} {
		if err := c.fetchHistory(ctx, directory, token, "--deepen="+fmt.Sprint(depth), baseRef, headSHA); err != nil {
			return fmt.Errorf("deepen review history by %d: %w", depth, err)
		}
		if _, err := c.gitOutput(ctx, directory, token, "merge-base", baseSHA, headSHA); err == nil {
			return nil
		}
	}
	if err := c.fetchHistory(ctx, directory, token, "--unshallow", baseRef, headSHA); err != nil {
		return fmt.Errorf("unshallow review history: %w", err)
	}
	if _, err := c.gitOutput(ctx, directory, token, "merge-base", baseSHA, headSHA); err != nil {
		return fmt.Errorf("resolve merge base between %s and %s after fetching history: %w", baseSHA, headSHA, err)
	}
	return nil
}

func (c Checkout) fetchHistory(ctx context.Context, directory, token, option, baseRef, headSHA string) error {
	args := []string{"fetch", option, "origin"}
	if baseRef != "" {
		args = append(args, baseRef)
	}
	args = append(args, headSHA)
	return c.git(ctx, directory, token, args...)
}

func (c Checkout) git(ctx context.Context, directory, token string, args ...string) error {
	_, err := c.runGit(ctx, directory, token, false, args...)
	return err
}

func (c Checkout) gitOutput(ctx context.Context, directory, token string, args ...string) (string, error) {
	return c.runGit(ctx, directory, token, true, args...)
}

func (c Checkout) runGit(ctx context.Context, directory, token string, captureStdout bool, args ...string) (string, error) {
	command := exec.CommandContext(ctx, c.gitBinary(), args...)
	configureGitProcessGroup(command)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = gitEnvironment(token)
	output := newBoundedOutput(maxGitCommandOutputBytes)
	// Clone/fetch/checkout do not need stdout. Leaving it disconnected avoids
	// retaining potentially large progress output while still retaining a
	// bounded stderr diagnostic should the command fail.
	if captureStdout {
		command.Stdout = output
	}
	command.Stderr = output
	err := command.Run()
	value, truncated := output.bytes()
	if err != nil {
		suffix := ""
		if truncated {
			suffix = fmt.Sprintf(" [diagnostics capped at %d bytes]", maxGitCommandOutputBytes)
		}
		return "", fmt.Errorf("git %s: %w: %s%s", strings.Join(args[:min(len(args), 2)], " "), err, trim(value), suffix)
	}
	if captureStdout && truncated {
		return "", fmt.Errorf("git %s: output exceeded %d-byte safety limit", strings.Join(args[:min(len(args), 2)], " "), maxGitCommandOutputBytes)
	}
	return string(value), nil
}

// gitEnvironment makes provider authentication deterministic for unattended
// runners. A failed or expired provider token must produce a bounded Git
// error, not wait for an interactive username/password prompt until the
// review checkout timeout expires. The explicit empty credential helper also
// prevents a host-level helper from reaching outside the installation-token
// boundary.
func gitEnvironment(token string) []string {
	environment := append([]string{}, os.Environ()...)
	environment = append(environment,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
	)
	if token == "" {
		return append(environment,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=credential.helper",
			"GIT_CONFIG_VALUE_0=",
		)
	}
	return append(environment,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=http.extraHeader",
		// GitHub's smart-HTTP Git endpoints authenticate installation tokens as
		// HTTP Basic credentials. The REST API accepts Bearer tokens, but using
		// that form for clone/fetch causes GitHub to fall back to an interactive
		// username prompt after rejecting the request.
		"GIT_CONFIG_VALUE_1=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token)),
	)
}

// Git can spawn SSH transport children. Keep the command in its own process
// group so a review cancellation stops both the Git client and that transport.
func configureGitProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
	command.WaitDelay = 5 * time.Second
}

func (c Checkout) gitBinary() string {
	if c.GitBinary != "" {
		return c.GitBinary
	}
	return "git"
}

func (c Checkout) token(ctx context.Context, job domain.ReviewJob) (string, error) {
	if c.Resolver != nil {
		return c.Resolver.Resolve(ctx, job)
	}
	if job.Provider == domain.ProviderGitHub {
		return c.GitHubToken, nil
	}
	if job.Provider == domain.ProviderGitLab {
		return c.GitLabToken, nil
	}
	return "", fmt.Errorf("unsupported provider %q", job.Provider)
}

func trim(value []byte) string {
	const maxBytes = 4096
	if len(value) > maxBytes {
		return string(value[:maxBytes]) + "…"
	}
	return string(value)
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
