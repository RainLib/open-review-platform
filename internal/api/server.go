package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/identity"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/RainLib/open-review-platform/internal/webhook"
)

const maxWebhookBytes = 2 << 20

type Server struct {
	store        store.Store
	auth         identity.Authenticator
	githubSecret string
	gitlabSecret string
	now          func() time.Time
}

func New(store store.Store, auth identity.Authenticator, githubSecret, gitlabSecret string) *Server {
	return &Server{
		store:        store,
		auth:         auth,
		githubSecret: githubSecret,
		gitlabSecret: gitlabSecret,
		now:          time.Now,
	}
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("POST /v1/tenants", s.createTenant)
	mux.HandleFunc("PUT /v1/tenants/{slug}/members/{subject}", s.upsertMembership)
	mux.HandleFunc("POST /v1/tenants/{slug}/installations", s.createInstallation)
	mux.HandleFunc("POST /v1/webhooks/github", s.githubWebhook)
	mux.HandleFunc("POST /v1/webhooks/gitlab", s.gitlabWebhook)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, principal)
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

func (s *Server) createTenant(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Slug = strings.ToLower(strings.TrimSpace(request.Slug))
	request.Name = strings.TrimSpace(request.Name)
	if !slugPattern.MatchString(request.Slug) || request.Name == "" || len(request.Name) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug or name is invalid"})
		return
	}
	tenant, err := s.store.CreateTenant(r.Context(), principal.Subject, request.Slug, request.Name)
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "tenant slug already exists"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create tenant"})
		return
	}
	writeJSON(w, http.StatusCreated, tenant)
}

func (s *Server) createInstallation(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.InstallationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.RepositoryScope = strings.TrimSpace(input.RepositoryScope)
	input.APIBaseURL = strings.TrimSuffix(strings.TrimSpace(input.APIBaseURL), "/")
	input.CredentialRef = strings.TrimSpace(input.CredentialRef)
	if !validInstallation(input) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider installation is invalid"})
		return
	}
	installation, err := s.store.CreateInstallation(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider installation already exists"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create provider installation"})
		return
	}
	writeJSON(w, http.StatusCreated, installation)
}

func (s *Server) upsertMembership(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Role = strings.TrimSpace(request.Role)
	subject := strings.TrimSpace(r.PathValue("subject"))
	if subject == "" || !domain.ValidRole(request.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "subject or role is invalid"})
		return
	}
	membership, err := s.store.UpsertMembership(r.Context(), principal.Subject, r.PathValue("slug"), subject, request.Role)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant owner role is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update membership"})
		return
	}
	writeJSON(w, http.StatusOK, membership)
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if err := webhook.VerifyGitHubSignature(s.githubSecret, body, r.Header.Get("X-Hub-Signature-256")); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook signature"})
		return
	}
	event, accepted, err := webhook.NormalizeGitHub(r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event"), body, s.now())
	s.enqueueNormalized(r.Context(), w, event, accepted, err)
}

func (s *Server) gitlabWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if err := webhook.VerifyGitLabToken(s.gitlabSecret, r.Header.Get("X-Gitlab-Token")); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook token"})
		return
	}
	event, accepted, err := webhook.NormalizeGitLab(r.Header.Get("X-Gitlab-Event-UUID"), r.Header.Get("X-Gitlab-Event"), body, s.now())
	s.enqueueNormalized(r.Context(), w, event, accepted, err)
}

func (s *Server) enqueueNormalized(ctx context.Context, w http.ResponseWriter, event domain.InboundEvent, accepted bool, normalizeErr error) {
	if normalizeErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	if !accepted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	job, duplicate, err := s.store.Enqueue(ctx, event)
	if errors.Is(err, store.ErrUnknownInstallation) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider installation"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not enqueue review"})
		return
	}
	if duplicate {
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "duplicate": true})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "job_id": job.ID.String()})
}

func readWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "webhook payload too large"})
		return nil, err
	}
	return body, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		return false
	}
	return true
}

func validInstallation(input domain.InstallationInput) bool {
	if !input.Provider.Valid() || input.ExternalID == "" || input.RepositoryScope == "" || input.CredentialRef == "" {
		return false
	}
	parsed, err := url.Parse(input.APIBaseURL)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.User == nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
