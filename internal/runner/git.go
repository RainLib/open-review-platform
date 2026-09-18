package runner

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
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
	return &Workspace{Path: directory, BaseSHA: strings.TrimSpace(baseSHA), cleanup: func() error { return os.RemoveAll(directory) }}, nil
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
	_, err := c.gitOutput(ctx, directory, token, args...)
	return err
}

func (c Checkout) gitOutput(ctx context.Context, directory, token string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, c.gitBinary(), args...)
	configureGitProcessGroup(command)
	if directory != "" {
		command.Dir = directory
	}
	if token != "" {
		command.Env = append(os.Environ(),
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Bearer "+token,
		)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args[:min(len(args), 2)], " "), err, trim(output))
	}
	return string(output), nil
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
