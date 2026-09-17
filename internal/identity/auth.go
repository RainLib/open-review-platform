package identity

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/coreos/go-oidc/v3/oidc"
)

type Principal struct {
	Subject string   `json:"subject"`
	Email   string   `json:"email,omitempty"`
	Groups  []string `json:"groups,omitempty"`
}

type Authenticator interface {
	Authenticate(context.Context, *http.Request) (Principal, error)
}

type oidcAuthenticator struct {
	verifier *oidc.IDTokenVerifier
}

func New(ctx context.Context, cfg config.AuthConfig) (Authenticator, error) {
	if cfg.Mode == "development" {
		return developmentAuthenticator{}, nil
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover Casdoor OIDC issuer: %w", err)
	}
	return oidcAuthenticator{verifier: provider.Verifier(&oidc.Config{ClientID: cfg.Audience})}, nil
}

func (a oidcAuthenticator) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	raw, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return Principal{}, err
	}
	idToken, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, fmt.Errorf("verify token: %w", err)
	}
	var claims struct {
		Subject string   `json:"sub"`
		Email   string   `json:"email"`
		Groups  []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("read claims: %w", err)
	}
	if claims.Subject == "" {
		return Principal{}, fmt.Errorf("token has no subject")
	}
	return Principal{Subject: claims.Subject, Email: claims.Email, Groups: claims.Groups}, nil
}

type developmentAuthenticator struct{}

func (developmentAuthenticator) Authenticate(_ context.Context, r *http.Request) (Principal, error) {
	subject := r.Header.Get("X-Development-Subject")
	if subject == "" {
		return Principal{}, fmt.Errorf("X-Development-Subject is required in development mode")
	}
	return Principal{Subject: subject}, nil
}

func bearerToken(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] == "" {
		return "", fmt.Errorf("missing bearer token")
	}
	return parts[1], nil
}
