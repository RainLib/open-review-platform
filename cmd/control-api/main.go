package main

import (
	"context"
	"log"
	"log/slog"
	stdhttp "net/http"
	"time"

	"github.com/RainLib/open-review-platform/internal/api"
	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/credentialcleanup"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/identity"
	"github.com/RainLib/open-review-platform/internal/observability"
	"github.com/RainLib/open-review-platform/internal/providercredentials"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/go-kratos/kratos/v2"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if err := cfg.ValidateControlAPI(); err != nil {
		log.Fatal(err)
	}
	database, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	// The API remains online during the three-container authentication mode.
	// Keep abandoned OAuth credentials bounded without a separate worker;
	// the store's row locks and installation recheck make concurrent API
	// replicas safe.
	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		(credentialcleanup.Runner{Store: database}).Run(cleanupCtx)
	}()
	defer func() {
		stopCleanup()
		<-cleanupDone
	}()
	database.ConfigureAgentDraftURLPolicy(domain.AgentDraftURLPolicy{
		GitHubPublicBaseURL: cfg.AgentTask.GitHubPublicBaseURL, GitHubPublicForAPIBaseURL: cfg.GitHub.APIURL,
		GitLabPublicBaseURL: cfg.AgentTask.GitLabPublicBaseURL, GitLabPublicForAPIBaseURL: cfg.GitLab.APIURL,
		AllowGitLabHTTP: cfg.GitLab.AllowHTTP,
	})
	authenticator, err := identity.New(ctx, cfg.Auth)
	if err != nil {
		log.Fatal(err)
	}
	mux := stdhttp.NewServeMux()
	metrics := observability.NewHTTPMetrics()
	mux.Handle("GET /metrics", metrics.PrometheusHandler(database))
	controlAPI := api.NewWithProviderAPIURLsAndProviderAuthorizationAndGitLabHTTP(database, authenticator, cfg.GitHub.Secret, cfg.GitLab.Secret, cfg.GitHub.APIURL, cfg.GitLab.APIURL, cfg.ProviderAuthorizationSecret, cfg.GitLabInstallationIdentitySecret, cfg.GitLab.AllowHTTP)
	controlAPI.SetGitLabDeploymentTokenAvailable(cfg.GitLab.DeploymentTokenAvailable)
	controlAPI.SetAgentTaskAdapterCallback(cfg.AgentTask.AdapterCallbackSecret, cfg.AgentTask.LeaseDuration)
	if cfg.ProviderCredentialEncryptionKey != "" {
		cipher, err := providercredentials.New(cfg.ProviderCredentialEncryptionKey)
		if err != nil {
			log.Fatal(err)
		}
		controlAPI.SetProviderOAuthCipher(cipher)
	}
	controlAPI.Register(mux)
	// Kratos defaults every HTTP request to a one-second deadline, which
	// terminates the run event stream. Disable that transport-wide deadline and
	// apply an application-aware timeout that exempts only the exact SSE route.
	server := khttp.NewServer(khttp.Address(cfg.HTTPAddress), khttp.Timeout(0))
	handler := observability.HTTPMiddleware(mux, metrics, slog.Default())
	server.HandlePrefix("/", observability.RequestTimeoutMiddleware(handler, 30*time.Second))
	app := kratos.New(
		kratos.Name("open-review-control-api"),
		kratos.Server(server),
	)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
