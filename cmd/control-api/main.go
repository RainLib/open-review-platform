package main

import (
	"context"
	"log"
	stdhttp "net/http"

	"github.com/RainLib/open-review-platform/internal/api"
	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/identity"
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
	database, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	authenticator, err := identity.New(ctx, cfg.Auth)
	if err != nil {
		log.Fatal(err)
	}
	mux := stdhttp.NewServeMux()
	api.New(database, authenticator, cfg.GitHub.Secret, cfg.GitLab.Secret).Register(mux)
	server := khttp.NewServer(khttp.Address(cfg.HTTPAddress))
	server.HandlePrefix("/", mux)
	app := kratos.New(
		kratos.Name("open-review-control-api"),
		kratos.Server(server),
	)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
