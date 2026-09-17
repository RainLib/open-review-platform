package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/identity"
	"github.com/RainLib/open-review-platform/internal/interaction"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/RainLib/open-review-platform/internal/webhook"
	"github.com/google/uuid"
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
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-sets", s.createRuleSet)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-sets", s.listRuleSets)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-sets/{ruleSetID}/versions/{version}/publish", s.publishRuleVersion)
	mux.HandleFunc("POST /v1/tenants/{slug}/rule-bindings", s.createRuleBinding)
	mux.HandleFunc("GET /v1/tenants/{slug}/rule-bindings", s.listRuleBindings)
	mux.HandleFunc("PUT /v1/tenants/{slug}/provider-identities/{provider}/{externalID}", s.upsertProviderIdentity)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs", s.listRuns)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs/{runID}", s.getRun)
	mux.HandleFunc("POST /v1/tenants/{slug}/runs/{runID}/cancel", s.cancelRun)
	mux.HandleFunc("GET /v1/tenants/{slug}/runs/{runID}/events", s.streamRunEvents)
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

func (s *Server) createRuleSet(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.RuleSetInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := s.store.CreateRuleSet(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "rule set name already exists"})
		return
	}
	if err != nil {
		slog.Warn("rejecting invalid rule set", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule set is invalid"})
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) listRuleSets(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	sets, err := s.store.ListRuleSets(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule sets"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule_sets": sets})
}

func (s *Server) publishRuleVersion(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	ruleSetID, err := uuid.Parse(r.PathValue("ruleSetID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule set id is invalid"})
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule version is invalid"})
		return
	}
	published, err := s.store.PublishRuleVersion(r.Context(), principal.Subject, r.PathValue("slug"), ruleSetID, version)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "draft rule version was not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not publish rule version"})
		return
	}
	writeJSON(w, http.StatusOK, published)
}

func (s *Server) createRuleBinding(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.RuleBindingInput
	if !decodeJSON(w, r, &input) {
		return
	}
	binding, err := s.store.CreateRuleBinding(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "published rule version was not found"})
		return
	}
	if err != nil {
		slog.Warn("rejecting invalid rule binding", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rule binding is invalid"})
		return
	}
	writeJSON(w, http.StatusCreated, binding)
}

func (s *Server) listRuleBindings(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	bindings, err := s.store.ListRuleBindings(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list rule bindings"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule_bindings": bindings})
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

func (s *Server) upsertProviderIdentity(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var request struct {
		Subject string `json:"subject"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	input := domain.ProviderIdentity{Provider: domain.Provider(strings.TrimSpace(r.PathValue("provider"))), ExternalID: strings.TrimSpace(r.PathValue("externalID")), Subject: strings.TrimSpace(request.Subject)}
	identity, err := s.store.UpsertProviderIdentity(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tenant administrator role is required"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "provider identity is already mapped"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider identity is invalid"})
		return
	}
	writeJSON(w, http.StatusOK, identity)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	limit := 25
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be from 1 to 100"})
			return
		}
		limit = parsed
	}
	runs, err := s.store.ListReviewRuns(r.Context(), principal.Subject, r.PathValue("slug"), limit)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list review runs"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	run, err := s.store.GetReviewRun(r.Context(), principal.Subject, r.PathValue("slug"), runID)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review run not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load review run"})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	var request struct {
		Revision int `json:"revision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	run, err := s.store.RequestRunCancellation(r.Context(), principal.Subject, r.PathValue("slug"), runID, request.Revision)
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "review run not found"})
		return
	}
	if errors.Is(err, store.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review run revision is stale"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "review run cannot be cancelled"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not cancel review run"})
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	runID, err := uuid.Parse(r.PathValue("runID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is invalid"})
		return
	}
	after := 0
	if value := r.URL.Query().Get("after_revision"); value != "" {
		after, err = strconv.Atoi(value)
		if err != nil || after < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "after_revision is invalid"})
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, err := s.store.ListRunEvents(r.Context(), principal.Subject, r.PathValue("slug"), runID, after)
		if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			return
		}
		for _, event := range events {
			encoded, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: transition\ndata: %s\n\n", event.Revision, encoded)
			after = event.Revision
		}
		if len(events) == 0 {
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
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
	switch r.Header.Get("X-GitHub-Event") {
	case "pull_request":
		event, accepted, err := webhook.NormalizeGitHub(r.Header.Get("X-GitHub-Delivery"), "pull_request", body, s.now())
		s.enqueueNormalized(r.Context(), w, event, accepted, err)
	case "issue_comment":
		s.providerComment(r.Context(), w, webhook.NormalizeGitHubIssueComment, r.Header.Get("X-GitHub-Delivery"), body)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type commentNormalizer func(string, []byte) (domain.CommentEvent, bool, error)

func (s *Server) providerComment(ctx context.Context, w http.ResponseWriter, normalize commentNormalizer, deliveryID string, body []byte) {
	event, accepted, err := normalize(deliveryID, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
		return
	}
	if !accepted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	parsed, mentioned, parseErr := interaction.Parse(event.Body)
	if !mentioned {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	input := domain.InteractionCommand{Event: event, Command: string(parsed.Kind), Mode: parsed.Mode, Normalized: parsed.Normalized}
	if parseErr != nil {
		input.Command, input.Normalized = "invalid", strings.TrimSpace(event.Body)
	}
	outcome, err := s.store.ProcessInteraction(ctx, input)
	if errors.Is(err, store.ErrUnknownInstallation) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not process interaction"})
		return
	}
	response := map[string]any{"accepted": outcome.Accepted, "duplicate": outcome.Duplicate, "reason": outcome.Reason}
	if outcome.RunID != nil {
		response["run_id"] = outcome.RunID.String()
	}
	writeJSON(w, http.StatusAccepted, response)
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
	deliveryID := r.Header.Get("X-Gitlab-Event-UUID")
	switch r.Header.Get("X-Gitlab-Event") {
	case "Merge Request Hook":
		event, accepted, err := webhook.NormalizeGitLab(deliveryID, "Merge Request Hook", body, s.now())
		s.enqueueNormalized(r.Context(), w, event, accepted, err)
	case "Note Hook":
		s.providerComment(r.Context(), w, webhook.NormalizeGitLabNoteComment, deliveryID, body)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
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
