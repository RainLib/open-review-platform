package identity

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

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
	issuer        string
	audience      string
	mu            sync.Mutex
	verifier      *oidc.IDTokenVerifier
	nextDiscovery time.Time
}

func New(ctx context.Context, cfg config.AuthConfig) (Authenticator, error) {
	if cfg.Mode == "development" {
		return developmentAuthenticator{}, nil
	}
	if strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.Audience) == "" {
		return nil, fmt.Errorf("OIDC issuer and audience are required")
	}
	// The identity provider may be temporarily unreachable during a local
	// restart or network partition. Start the control plane so webhook and
	// recovery routes remain available, while every management request still
	// fails closed until discovery and token verification succeed.
	return &oidcAuthenticator{issuer: cfg.Issuer, audience: cfg.Audience}, nil
}

func (a *oidcAuthenticator) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	raw, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return Principal{}, err
	}
	verifier, err := a.verifierFor(ctx)
	if err != nil {
		return Principal{}, err
	}
	idToken, err := verifier.Verify(ctx, raw)
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

func (a *oidcAuthenticator) verifierFor(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.verifier != nil {
		return a.verifier, nil
	}
	if time.Now().Before(a.nextDiscovery) {
		return nil, fmt.Errorf("OIDC issuer discovery is temporarily unavailable")
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(discoveryCtx, a.issuer)
	if err != nil {
		if ctx.Err() == nil {
			a.nextDiscovery = time.Now().Add(10 * time.Second)
		}
		return nil, fmt.Errorf("discover Casdoor OIDC issuer: %w", err)
	}
	a.verifier = provider.Verifier(&oidc.Config{ClientID: a.audience})
	return a.verifier, nil
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
