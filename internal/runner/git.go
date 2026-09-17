package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	GitHubToken string
	GitLabToken string
}

func (c Checkout) Prepare(ctx context.Context, job domain.ReviewJob) (*Workspace, error) {
	directory, err := os.MkdirTemp("", "open-review-")
	if err != nil {
		return nil, fmt.Errorf("create isolated workspace: %w", err)
	}
	fail := func(err error) (*Workspace, error) {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	if err := c.git(ctx, "", job.Provider, "clone", "--no-checkout", "--depth=1", job.CloneURL, directory); err != nil {
		return fail(fmt.Errorf("clone review repository: %w", err))
	}
	if err := c.git(ctx, directory, job.Provider, "fetch", "--depth=1", "origin", job.BaseRef); err != nil {
		return fail(fmt.Errorf("fetch base ref: %w", err))
	}
	baseSHA, err := c.gitOutput(ctx, directory, job.Provider, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return fail(fmt.Errorf("resolve base SHA: %w", err))
	}
	if job.BaseSHA != "" {
		baseSHA = job.BaseSHA
	}
	if err := c.git(ctx, directory, job.Provider, "fetch", "--depth=1", "origin", job.HeadSHA); err != nil {
		return fail(fmt.Errorf("fetch head SHA: %w", err))
	}
	if err := c.git(ctx, directory, job.Provider, "checkout", "--detach", job.HeadSHA); err != nil {
		return fail(fmt.Errorf("checkout head SHA: %w", err))
	}
	return &Workspace{Path: directory, BaseSHA: strings.TrimSpace(baseSHA), cleanup: func() error { return os.RemoveAll(directory) }}, nil
}

func (c Checkout) git(ctx context.Context, directory string, provider domain.Provider, args ...string) error {
	_, err := c.gitOutput(ctx, directory, provider, args...)
	return err
}

func (c Checkout) gitOutput(ctx context.Context, directory string, provider domain.Provider, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	if directory != "" {
		command.Dir = directory
	}
	if token := c.token(provider); token != "" {
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

func (c Checkout) token(provider domain.Provider) string {
	if provider == domain.ProviderGitHub {
		return c.GitHubToken
	}
	if provider == domain.ProviderGitLab {
		return c.GitLabToken
	}
	return ""
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
