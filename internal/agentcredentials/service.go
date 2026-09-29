// Package agentcredentials owns the internal, task-bound provider-write
// credential boundary. It does not expose App keys or OAuth references to a
// coding adapter; an issuer resolves those only after the control-plane store
// confirms the exact active job and repository.
package agentcredentials

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

const Path = "/v1/open-review/agent-credentials"

type Request struct {
	AttemptID      string          `json:"attempt_id"`
	AdapterJobID   string          `json:"adapter_job_id"`
	InstallationID string          `json:"installation_id"`
	Provider       domain.Provider `json:"provider"`
	APIBaseURL     string          `json:"api_base_url"`
	Repository     string          `json:"repository"`
}

type Response struct {
	InstallationID string          `json:"installation_id"`
	Provider       domain.Provider `json:"provider"`
	APIBaseURL     string          `json:"api_base_url"`
	Repository     string          `json:"repository"`
	CloneBaseURL   string          `json:"clone_base_url"`
	Token          string          `json:"token"`
}

type Store interface {
	LoadAgentTaskCredentialGrant(context.Context, uuid.UUID, string) (store.AgentTaskCredentialGrant, error)
	ReserveAgentTaskCredentialIssuance(context.Context, uuid.UUID, string, store.AgentTaskCredentialGrant) (uuid.UUID, error)
	CompleteAgentTaskCredentialIssuance(context.Context, uuid.UUID, string) error
}

type Issuer interface {
	Issue(context.Context, store.AgentTaskCredentialGrant) (cloneBaseURL, token string, err error)
}

type Service struct {
	Secret string
	Store  Store
	Issuer Issuer
	Now    func() time.Time
}

func (service Service) Handler() http.Handler {
	return http.HandlerFunc(service.serve)
}

func (service Service) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != Path || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if service.Store == nil || service.Issuer == nil || len(service.Secret) < 32 {
		http.Error(w, "credential broker unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) == 0 || len(body) > 4096 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	now := time.Now()
	if service.Now != nil {
		now = service.Now()
	}
	if !agentadapter.Verify(service.Secret, r.Header.Get(agentadapter.HeaderTimestamp), r.Header.Get(agentadapter.HeaderSignature), body, now, time.Minute) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request Request
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	attemptID, attemptErr := uuid.Parse(request.AttemptID)
	installationID, installationErr := uuid.Parse(request.InstallationID)
	if attemptErr != nil || attemptID == uuid.Nil || installationErr != nil || installationID == uuid.Nil || strings.TrimSpace(request.AdapterJobID) == "" || !request.Provider.Valid() || request.APIBaseURL == "" || request.Repository == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	grant, err := service.Store.LoadAgentTaskCredentialGrant(r.Context(), attemptID, request.AdapterJobID)
	if errors.Is(err, store.ErrAgentTaskClaimLost) || errors.Is(err, store.ErrInvalidAgentTask) {
		http.Error(w, "credential grant unavailable", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, "credential grant unavailable", http.StatusServiceUnavailable)
		return
	}
	if grant.InstallationID != installationID || grant.Provider != request.Provider || grant.APIBaseURL != request.APIBaseURL || grant.Repository != request.Repository {
		http.Error(w, "credential grant unavailable", http.StatusForbidden)
		return
	}
	issuanceID, err := service.Store.ReserveAgentTaskCredentialIssuance(r.Context(), attemptID, request.AdapterJobID, grant)
	if errors.Is(err, store.ErrAgentTaskClaimLost) || errors.Is(err, store.ErrInvalidAgentTask) {
		http.Error(w, "credential grant unavailable", http.StatusForbidden)
		return
	}
	if errors.Is(err, store.ErrAgentCredentialIssuanceLimit) {
		http.Error(w, "credential issuance limit reached", http.StatusTooManyRequests)
		return
	}
	if err != nil {
		http.Error(w, "credential grant unavailable", http.StatusServiceUnavailable)
		return
	}
	complete := func(state string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return service.Store.CompleteAgentTaskCredentialIssuance(ctx, issuanceID, state)
	}
	cloneBaseURL, token, err := service.Issuer.Issue(r.Context(), grant)
	if err != nil || strings.TrimSpace(cloneBaseURL) == "" || strings.TrimSpace(token) == "" {
		_ = complete("failed")
		http.Error(w, "credential issuance unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := complete("issued"); err != nil {
		_ = complete("withheld")
		if errors.Is(err, store.ErrAgentTaskClaimLost) || errors.Is(err, store.ErrInvalidAgentTask) {
			http.Error(w, "credential grant unavailable", http.StatusForbidden)
		} else {
			http.Error(w, "credential issuance unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Response{InstallationID: request.InstallationID, Provider: request.Provider, APIBaseURL: request.APIBaseURL, Repository: request.Repository, CloneBaseURL: cloneBaseURL, Token: token})
}
