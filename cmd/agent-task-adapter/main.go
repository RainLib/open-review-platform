// agent-task-adapter is a separately deployable coding-agent boundary. It is
// intentionally not part of control-api or agent-task-runner: only this
// workload may receive provider write credentials and an approved executor
// binary, and it exposes no Console/public route.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/agentcredentials"
)

func main() {
	environment := strings.TrimSpace(os.Getenv("ENVIRONMENT"))
	sandboxKind := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_ADAPTER_SANDBOX_KIND")))
	if sandboxKind != "" && sandboxKind != "docker" {
		log.Fatal("AGENT_ADAPTER_SANDBOX_KIND must be docker or empty for development-only UID execution")
	}
	containerSandbox := sandboxKind == "docker"
	// The local UID-pool path still shares PID/network namespaces and remains
	// development-only. A production process requires a preflighted per-job
	// container image on an internal-only network before accepting work.
	if !containerSandbox && !strings.EqualFold(environment, "development") {
		log.Fatal("production coding adapter requires a per-job sandbox; the UID-pool adapter is development-only")
	}
	if !containerSandbox && !strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_ADAPTER_ALLOW_UNSANDBOXED_DEVELOPMENT")), "true") {
		log.Fatal("development coding adapter requires AGENT_ADAPTER_ALLOW_UNSANDBOXED_DEVELOPMENT=true")
	}
	allowAdapterHTTP := strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_TASK_ADAPTER_ALLOW_HTTP")), "true")
	allowGitLabHTTP := strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_ADAPTER_GITLAB_ALLOW_HTTP")), "true")
	gitLabPublicBaseURL := strings.TrimSuffix(strings.TrimSpace(os.Getenv("AGENT_GITLAB_PUBLIC_BASE_URL")), "/")
	gitLabPublicForAPIBaseURL := strings.TrimSuffix(strings.TrimSpace(os.Getenv("AGENT_GITLAB_PUBLIC_FOR_API_BASE_URL")), "/")
	gitHubPublicBaseURL := strings.TrimSuffix(strings.TrimSpace(os.Getenv("AGENT_GITHUB_PUBLIC_BASE_URL")), "/")
	gitHubPublicForAPIBaseURL := strings.TrimSuffix(strings.TrimSpace(os.Getenv("AGENT_GITHUB_PUBLIC_FOR_API_BASE_URL")), "/")
	if (allowAdapterHTTP || allowGitLabHTTP) && !strings.EqualFold(strings.TrimSpace(os.Getenv("ENVIRONMENT")), "development") {
		log.Fatal("plaintext adapter/GitLab HTTP is allowed only when ENVIRONMENT=development")
	}
	if gitLabPublicBaseURL != "" && !validPublicDraftBaseURL(gitLabPublicBaseURL, allowGitLabHTTP) {
		log.Fatal("AGENT_GITLAB_PUBLIC_BASE_URL requires HTTPS, or loopback HTTP with development GitLab opt-in")
	}
	if gitLabPublicBaseURL != "" && gitLabPublicForAPIBaseURL == "" {
		log.Fatal("AGENT_GITLAB_PUBLIC_FOR_API_BASE_URL is required when the public GitLab base is configured")
	}
	if gitHubPublicBaseURL != "" && (!validPublicDraftBaseURL(gitHubPublicBaseURL, false) || gitHubPublicForAPIBaseURL == "") {
		log.Fatal("AGENT_GITHUB_PUBLIC_BASE_URL requires HTTPS and AGENT_GITHUB_PUBLIC_FOR_API_BASE_URL")
	}
	workspaceRoot := strings.TrimSpace(os.Getenv("AGENT_ADAPTER_WORKSPACE_ROOT"))
	if workspaceRoot == "" {
		log.Fatal("AGENT_ADAPTER_WORKSPACE_ROOT is required")
	}
	receiptDir := strings.TrimSpace(os.Getenv("AGENT_ADAPTER_RECEIPT_DIR"))
	if receiptDir == "" {
		log.Fatal("AGENT_ADAPTER_RECEIPT_DIR is required for restart-safe job identity")
	}
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		log.Fatalf("create adapter workspace root: %v", err)
	}
	pipeline := agentadapter.Pipeline{
		WorkspaceRoot: workspaceRoot, ExecutorKind: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_EXECUTOR")),
		CodexBinary: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CODEX_BINARY")), ClaudeBinary: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CLAUDE_BINARY")),
		GitBinary: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_GIT_BINARY")), AllowedPaths: splitCSV(os.Getenv("AGENT_ADAPTER_ALLOWED_PATHS")),
		GitHubToken: os.Getenv("AGENT_ADAPTER_GITHUB_TOKEN"), GitLabToken: os.Getenv("AGENT_ADAPTER_GITLAB_TOKEN"),
		GitHubCloneBaseURL: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_GITHUB_CLONE_BASE_URL")), GitLabCloneBaseURL: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_GITLAB_CLONE_BASE_URL")),
		GitHubPublicBaseURL: gitHubPublicBaseURL, GitHubPublicForAPIBaseURL: gitHubPublicForAPIBaseURL,
		GitLabAllowHTTP: allowGitLabHTTP, GitLabPublicBaseURL: gitLabPublicBaseURL, GitLabPublicForAPIBaseURL: gitLabPublicForAPIBaseURL,
		MaxChangedFiles: intEnv("AGENT_ADAPTER_MAX_CHANGED_FILES", 100), MaxDiffBytes: int64(intEnv("AGENT_ADAPTER_MAX_DIFF_BYTES", 2<<20)),
		CodeModelBroker:         agentadapter.ModelBrokerConfig{APIBaseURL: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CODEX_MODEL_API_BASE_URL")), APIKey: os.Getenv("AGENT_ADAPTER_CODEX_MODEL_API_KEY"), Model: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CODEX_MODEL"))},
		ClaudeModelBroker:       agentadapter.ModelBrokerConfig{APIBaseURL: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL_API_BASE_URL")), APIKey: os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL_API_KEY"), Model: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL")), WireAPI: "anthropic"},
		VerificationProfileFile: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_VERIFICATION_PROFILES_FILE")),
	}
	if containerSandbox {
		pipeline.DockerSandbox = &agentadapter.DockerSandboxConfig{
			DockerBinary:    strings.TrimSpace(os.Getenv("AGENT_ADAPTER_DOCKER_BINARY")),
			ImageID:         strings.TrimSpace(os.Getenv("AGENT_ADAPTER_SANDBOX_IMAGE_ID")),
			InternalNetwork: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_SANDBOX_NETWORK")),
			WorkspaceVolume: strings.TrimSpace(os.Getenv("AGENT_ADAPTER_SANDBOX_WORKSPACE_VOLUME")),
			AdapterHost:     strings.TrimSpace(os.Getenv("AGENT_ADAPTER_SANDBOX_ADAPTER_HOST")),
		}
	}
	identityPoolSize := intEnv("AGENT_ADAPTER_EXECUTOR_IDENTITY_POOL_SIZE", 0)
	if identityPoolSize > 0 {
		pool, err := agentadapter.NewExecutorIdentityPool(10002, identityPoolSize)
		if err != nil {
			log.Fatal(err)
		}
		pipeline.ExecutorIdentityPool = pool
	} else {
		log.Fatal("coding adapter requires an isolated executor identity pool")
	}
	credentialFile := strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CREDENTIALS_FILE"))
	credentialBrokerURL := strings.TrimSpace(os.Getenv("AGENT_ADAPTER_CREDENTIAL_BROKER_URL"))
	if credentialBrokerURL != "" {
		if credentialFile != "" || pipeline.GitHubToken != "" || pipeline.GitLabToken != "" {
			log.Fatal("credential broker cannot be combined with file or global provider tokens")
		}
		source, err := agentcredentials.NewSource(credentialBrokerURL, os.Getenv("AGENT_ADAPTER_CREDENTIAL_BROKER_SECRET"), allowAdapterHTTP)
		if err != nil {
			log.Fatal(err)
		}
		pipeline.CredentialSource = source
	} else if credentialFile != "" {
		if !strings.EqualFold(environment, "development") {
			log.Fatal("production adapter requires the task-bound credential broker; a static credential file is development-only")
		}
		if pipeline.GitHubToken != "" || pipeline.GitLabToken != "" {
			log.Fatal("scoped adapter credential file cannot be combined with global provider tokens")
		}
		fileSource := agentadapter.FileCredentialSource{Path: credentialFile}
		if err := fileSource.Validate(); err != nil {
			log.Fatal(err)
		}
		pipeline.CredentialSource = fileSource
	} else if !strings.EqualFold(environment, "development") {
		log.Fatal("production adapter requires a task-bound credential broker")
	}
	executorBinary, err := pipeline.ResolveExecutorBinary()
	if err != nil {
		log.Fatal(err)
	}
	if pipeline.ExecutorKind == "codex" {
		if pipeline.CodeModelBroker.APIBaseURL == "" {
			log.Fatal("Codex executor requires a job-scoped model broker; configure AGENT_ADAPTER_CODEX_MODEL_API_BASE_URL, AGENT_ADAPTER_CODEX_MODEL_API_KEY and AGENT_ADAPTER_CODEX_MODEL")
		}
		if pipeline.DockerSandbox != nil {
			pipeline.DockerSandbox.DockerBinary = executorBinary
		} else {
			pipeline.CodexBinary = executorBinary
		}
	} else if pipeline.ExecutorKind == "claude" {
		if pipeline.ClaudeModelBroker.APIBaseURL == "" {
			log.Fatal("Claude executor requires a job-scoped Anthropic Messages broker; configure AGENT_ADAPTER_CLAUDE_MODEL_API_BASE_URL, AGENT_ADAPTER_CLAUDE_MODEL_API_KEY and AGENT_ADAPTER_CLAUDE_MODEL")
		}
		if pipeline.DockerSandbox == nil {
			log.Fatal("Claude executor requires the per-job Docker sandbox")
		}
		pipeline.DockerSandbox.DockerBinary = executorBinary
	} else {
		log.Fatal("unsupported coding executor")
	}
	preflight, stopPreflight := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopPreflight()
	if err := pipeline.CheckSandboxResourcesReady(preflight); err != nil {
		log.Fatalf("container sandbox preflight failed: %v", err)
	}
	// Own the shared checkout volume before acquiring receipts or removing any
	// orphan sandbox. A second replica with a different receipt directory must
	// not stop this instance's active child or delete its checkout.
	workspaceLock, err := agentadapter.LockWorkspaceRoot(workspaceRoot)
	if err != nil {
		log.Fatalf("adapter workspace volume is already owned: %v", err)
	}
	defer func() { _ = workspaceLock.Close() }()
	service, err := agentadapter.NewService(agentadapter.ServiceConfig{
		Secret: os.Getenv("AGENT_TASK_ADAPTER_SECRET"), CallbackURL: strings.TrimSpace(os.Getenv("AGENT_TASK_CALLBACK_URL")), AllowHTTP: allowAdapterHTTP,
		HeartbeatEvery: durationEnv("AGENT_ADAPTER_HEARTBEAT_INTERVAL", 30*time.Second), RequestMaxSkew: time.Minute,
		CallbackTimeout: durationEnv("AGENT_ADAPTER_CALLBACK_TIMEOUT", 15*time.Second), ReceiptDir: receiptDir, Executor: pipeline,
		DraftURLPolicy: pipeline.DraftURLPolicy(),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	// NewService owns the single-replica receipt lock now. Remove only children
	// carrying this adapter's exact workspace-volume label before recovering
	// interrupted receipts or accepting another task.
	cleanupCtx, stopCleanup := context.WithTimeout(context.Background(), 20*time.Second)
	if err := pipeline.CleanupOrphanSandboxes(cleanupCtx, workspaceLock); err != nil {
		stopCleanup()
		log.Fatalf("container sandbox recovery cleanup failed: %v", err)
	}
	stopCleanup()
	capacityCtx, stopCapacity := context.WithTimeout(context.Background(), 20*time.Second)
	if err := pipeline.CheckSandboxReady(capacityCtx); err != nil {
		stopCapacity()
		log.Fatalf("container sandbox capacity preflight failed: %v", err)
	}
	stopCapacity()
	address := strings.TrimSpace(os.Getenv("AGENT_ADAPTER_HTTP_ADDRESS"))
	if address == "" {
		address = ":8090"
	}
	httpServer := &http.Server{Addr: address, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go service.Recover(ctx)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				service.RetryPending(ctx)
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = httpServer.Shutdown(shutdown)
	}()
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func validPublicDraftBaseURL(value string, allowHTTP bool) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(parsed.Path, "()\t\r\n ") {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" || !allowHTTP {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}
func durationEnv(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if value, err := time.ParseDuration(raw); err == nil && value > 0 {
			return value
		}
	}
	return fallback
}
func intEnv(key string, fallback int) int {
	var value int
	if _, err := fmt.Sscanf(strings.TrimSpace(os.Getenv(key)), "%d", &value); err == nil && value > 0 {
		return value
	}
	return fallback
}
