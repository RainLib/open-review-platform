// agent-credential-broker is a private control-plane workload. Only it holds
// coding App keys or repository credentials; review workers and the coding
// adapter never receive the deployment-owned source.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/agentcredentials"
	"github.com/RainLib/open-review-platform/internal/credentials"
	"github.com/RainLib/open-review-platform/internal/health"
	"github.com/RainLib/open-review-platform/internal/store"
)

func main() {
	secret := os.Getenv("AGENT_CREDENTIAL_BROKER_SECRET")
	databaseURL := strings.TrimSpace(os.Getenv("CONTROL_DATABASE_URL"))
	if len(secret) < 32 || databaseURL == "" {
		log.Fatal("broker secret and database are required")
	}
	var issuer agentcredentials.ProviderIssuer
	if appID := strings.TrimSpace(os.Getenv("AGENT_CODING_GITHUB_APP_ID")); appID != "" {
		keyPath := strings.TrimSpace(os.Getenv("AGENT_CODING_GITHUB_APP_PRIVATE_KEY_PATH"))
		mapPath := strings.TrimSpace(os.Getenv("AGENT_CODING_GITHUB_INSTALLATIONS_FILE"))
		if keyPath == "" || mapPath == "" {
			log.Fatal("GitHub coding App key and installation map are required")
		}
		apiBaseURL := strings.TrimSuffix(strings.TrimSpace(os.Getenv("AGENT_CODING_GITHUB_API_URL")), "/")
		if apiBaseURL == "" {
			apiBaseURL = "https://api.github.com"
		}
		app, err := credentials.NewGitHubAppBrokerFromFile(appID, keyPath, apiBaseURL)
		if err != nil {
			log.Fatal(err)
		}
		github := agentcredentials.GitHubIssuer{APIBaseURL: apiBaseURL, MapPath: mapPath, Tokens: app}
		if err := github.Validate(); err != nil {
			log.Fatal(err)
		}
		preflight, cancelPreflight := context.WithTimeout(context.Background(), 15*time.Second)
		if err := app.CheckRepositoryWriteReadiness(preflight); err != nil {
			cancelPreflight()
			log.Fatal(err)
		}
		cancelPreflight()
		issuer.GitHub = github
	}
	if path := strings.TrimSpace(os.Getenv("AGENT_CODING_GITLAB_CREDENTIALS_FILE")); path != "" {
		gitlab := agentcredentials.GitLabIssuer{
			Credentials: agentadapter.FileCredentialSource{Path: path},
			AllowHTTP: strings.EqualFold(os.Getenv("ENVIRONMENT"), "development") &&
				strings.EqualFold(os.Getenv("AGENT_CODING_GITLAB_ALLOW_HTTP"), "true"),
		}
		if err := gitlab.Validate(); err != nil {
			log.Fatal(err)
		}
		issuer.GitLab = gitlab
	}
	if issuer.GitHub == nil && issuer.GitLab == nil {
		log.Fatal("configure a GitHub coding App or a private GitLab coding credential map")
	}
	postgres, err := store.Open(context.Background(), databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer postgres.Close()
	address := strings.TrimSpace(os.Getenv("AGENT_CREDENTIAL_BROKER_HTTP_ADDRESS"))
	if address == "" {
		address = ":8091"
	}
	certificate := strings.TrimSpace(os.Getenv("AGENT_CREDENTIAL_BROKER_TLS_CERT"))
	privateKey := strings.TrimSpace(os.Getenv("AGENT_CREDENTIAL_BROKER_TLS_KEY"))
	if (certificate == "") != (privateKey == "") || (!strings.EqualFold(os.Getenv("ENVIRONMENT"), "development") && certificate == "") {
		log.Fatal("production credential broker requires a TLS certificate and key")
	}
	server := &http.Server{
		Addr: address, Handler: (agentcredentials.Service{Secret: secret, Store: postgres, Issuer: issuer}).Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reporter := &health.Reporter{
		Store:    postgres,
		WorkerID: health.EnvironmentWorkerID("AGENT_CREDENTIAL_BROKER_WORKER_ID", "agent-credential-broker"),
		Kind:     "agent-credential-broker", Version: health.EnvironmentBuildVersion(), Capacity: 1,
	}
	go reporter.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if certificate != "" {
		err = server.ListenAndServeTLS(certificate, privateKey)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
