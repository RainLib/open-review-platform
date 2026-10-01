package agentadapter

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// VerificationProfile is deployment-owned, never derived from an Issue,
// approved plan, repository file or coding model output. Its command runs as
// argv (not a shell string) in a second, networkless pinned container.
type VerificationProfile struct {
	Provider       domain.Provider `json:"provider"`
	APIBaseURL     string          `json:"api_base_url"`
	Repository     string          `json:"repository"`
	ImageID        string          `json:"image_id"`
	Argv           []string        `json:"argv"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}

type verificationProfileFile struct {
	Version int                   `json:"version"`
	Entries []VerificationProfile `json:"entries"`
}

type VerificationEvidence struct {
	ProfileSHA256 string
	OutputSHA256  string
	OutputBytes   int64
}

func loadVerificationProfile(path string, submission Submission) (*VerificationProfile, error) {
	if path == "" {
		return nil, nil
	}
	entries, err := readVerificationProfiles(path)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entry := &entries[i]
		if entry.Provider == submission.Task.Provider && entry.APIBaseURL == submission.Task.APIBaseURL && entry.Repository == submission.Task.Repository {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("no approved verification profile matches the admitted repository")
}

func readVerificationProfiles(path string) ([]VerificationProfile, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("verification profile file must have an absolute clean path")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open verification profile file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("verification profile file must be a private bounded regular file")
	}
	var config verificationProfileFile
	decoder := json.NewDecoder(io.LimitReader(file, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode verification profile file: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("verification profile file contains trailing data")
	}
	if config.Version != 1 || len(config.Entries) == 0 || len(config.Entries) > 100 {
		return nil, fmt.Errorf("verification profile catalog version or size is invalid")
	}
	seen := make(map[string]bool, len(config.Entries))
	for i := range config.Entries {
		entry := &config.Entries[i]
		if err := entry.valid(); err != nil {
			return nil, fmt.Errorf("verification profile entry %d: %w", i+1, err)
		}
		key := string(entry.Provider) + "\x00" + entry.APIBaseURL + "\x00" + entry.Repository
		if seen[key] {
			return nil, fmt.Errorf("verification profile catalog contains duplicate repository identity")
		}
		seen[key] = true
	}
	return config.Entries, nil
}

func (profile VerificationProfile) valid() error {
	parsed, err := url.Parse(profile.APIBaseURL)
	if !profile.Provider.Valid() || profile.APIBaseURL == "" || err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") || strings.Trim(profile.Repository, "/") != profile.Repository || profile.Repository == "" ||
		len(profile.ImageID) != len("sha256:")+64 || !strings.HasPrefix(profile.ImageID, "sha256:") || !isLowerHex(strings.TrimPrefix(profile.ImageID, "sha256:")) ||
		profile.TimeoutSeconds < 10 || profile.TimeoutSeconds > 600 || len(profile.Argv) == 0 || len(profile.Argv) > 16 || !filepath.IsAbs(profile.Argv[0]) {
		return fmt.Errorf("provider identity, pinned image, command or timeout is invalid")
	}
	for _, arg := range profile.Argv {
		if arg == "" || len(arg) > 512 || strings.ContainsRune(arg, 0) || strings.ContainsAny(arg, "\n\r") {
			return fmt.Errorf("verification argv contains an invalid argument")
		}
	}
	return nil
}

func (profile VerificationProfile) evidenceHash() (string, error) {
	encoded, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (pipeline Pipeline) runVerification(ctx context.Context, workspace string, profile VerificationProfile) (evidence VerificationEvidence, resultErr error) {
	if pipeline.DockerSandbox == nil || pipeline.ExecutorIdentityPool == nil {
		return VerificationEvidence{}, fmt.Errorf("approved verification requires a per-job container sandbox")
	}
	if err := profile.valid(); err != nil {
		return VerificationEvidence{}, err
	}
	if err := pipeline.CheckSandboxReady(ctx); err != nil {
		return VerificationEvidence{}, err
	}
	verifiedImage, err := pipeline.dockerOutput(ctx, "image", "inspect", "--format", "{{.Id}}", profile.ImageID)
	if err != nil || strings.TrimSpace(verifiedImage) != profile.ImageID {
		return VerificationEvidence{}, fmt.Errorf("approved verification image is unavailable or changed")
	}
	relative, err := filepath.Rel(pipeline.WorkspaceRoot, workspace)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") || filepath.Base(relative) != relative {
		return VerificationEvidence{}, fmt.Errorf("verification workspace is outside the private root")
	}
	if actual, err := filepath.EvalSymlinks(workspace); err != nil || actual != workspace {
		return VerificationEvidence{}, fmt.Errorf("verification workspace is not an exact directory")
	}
	identity, release, err := pipeline.ExecutorIdentityPool.acquire(ctx)
	if err != nil {
		return VerificationEvidence{}, fmt.Errorf("reserve verification identity: %w", err)
	}
	defer release()
	if err := transferExecutorWorkspace(workspace, identity); err != nil {
		_ = reclaimExecutorWorkspace(workspace)
		return VerificationEvidence{}, err
	}
	defer func() {
		if err := reclaimExecutorWorkspace(workspace); err != nil {
			resultErr = fmt.Errorf("reclaim verification workspace before publication: %w", err)
		}
	}()
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return VerificationEvidence{}, fmt.Errorf("create verification container identity: %w", err)
	}
	name := "openreview-agent-" + hex.EncodeToString(random)
	args := []string{
		"run", "--interactive", "--pull", "never", "--name", name,
		"--label", sandboxOwnerLabel + "=" + pipeline.DockerSandbox.WorkspaceVolume,
		"--network", "none", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true", "--pids-limit", "256",
		"--memory", "4g", "--cpus", "2", "--user", fmt.Sprintf("%d:%d", identity.uid, identity.gid),
		"--mount", "type=volume,src=" + pipeline.DockerSandbox.WorkspaceVolume + ",dst=" + pipeline.WorkspaceRoot,
		"--tmpfs", "/tmp:rw,nosuid,size=268435456", "--workdir", workspace,
		"--env", "HOME=/tmp", "--entrypoint", profile.Argv[0], profile.ImageID,
	}
	args = append(args, profile.Argv[1:]...)
	verificationCtx, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutSeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(verificationCtx, pipeline.DockerSandbox.DockerBinary, args...)
	command.Env = dockerClientEnvironment()
	if err := configureExecutorProcessGroup(command); err != nil {
		return VerificationEvidence{}, err
	}
	output := &boundedProcessOutput{command: command, limit: maxExecutorOutputBytes, capture: true}
	command.Stdout, command.Stderr = output, output
	runErr := command.Run()
	_ = stopExecutorProcessGroup(command)
	cleanupCtx, stopCleanup := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCleanup()
	if _, cleanupErr := pipeline.dockerOutput(cleanupCtx, "rm", "--force", name); cleanupErr != nil {
		return VerificationEvidence{}, fmt.Errorf("verification container cleanup could not be verified: %w", cleanupErr)
	}
	text, count, exceeded := output.snapshot()
	if exceeded {
		return VerificationEvidence{}, fmt.Errorf("verification output exceeded %d-byte budget", maxExecutorOutputBytes)
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) && verificationCtx.Err() == nil && exit.ExitCode() > 0 && exit.ExitCode() < 125 {
			if len(text) > 16384 {
				text = strings.ToValidUTF8(text[len(text)-16384:], "")
			}
			return VerificationEvidence{}, &verificationFailure{output: text, exit: exit.ExitCode()}
		}
		return VerificationEvidence{}, fmt.Errorf("approved repository verification unavailable: %w", runErr)
	}
	profileSHA, err := profile.evidenceHash()
	if err != nil {
		return VerificationEvidence{}, err
	}
	outputSHA := sha256.Sum256([]byte(text))
	return VerificationEvidence{ProfileSHA256: profileSHA, OutputSHA256: hex.EncodeToString(outputSHA[:]), OutputBytes: count}, nil
}
