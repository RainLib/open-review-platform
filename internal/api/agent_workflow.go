package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type agentTaskAcceptanceStore interface {
	DecideAgentTaskAcceptance(context.Context, string, string, uuid.UUID, domain.AgentTaskAcceptanceInput) (domain.AgentTaskAcceptance, error)
}

func (s *Server) decideAgentTaskAcceptance(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("taskID"))
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return
	}
	var input domain.AgentTaskAcceptanceInput
	if !decodeJSON(w, r, &input) {
		return
	}
	backend, ok := s.store.(agentTaskAcceptanceStore)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "acceptance unavailable"})
		return
	}
	result, err := backend.DecideAgentTaskAcceptance(r.Context(), principal.Subject, r.PathValue("slug"), id, input)
	switch {
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, 403, map[string]string{"error": "owner or administrator required"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, 404, map[string]string{"error": "task delivery not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, 409, map[string]string{"error": "Delivery or checks changed. Refresh and verify every criterion against the current commit."})
	case errors.Is(err, store.ErrInvalidAgentTask):
		writeJSON(w, 400, map[string]string{"error": "acceptance evidence is invalid"})
	case err != nil:
		writeJSON(w, 500, map[string]string{"error": "acceptance could not be recorded"})
	default:
		writeJSON(w, 200, result)
	}
}

type agentTaskChecksRetryStore interface {
	RetryAgentTaskChecks(context.Context, string, string, uuid.UUID, int) (domain.AgentTaskAcceptance, error)
}

func (s *Server) retryAgentTaskChecks(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("taskID"))
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return
	}
	var input struct {
		Revision int `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	backend, ok := s.store.(agentTaskChecksRetryStore)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "check retry unavailable"})
		return
	}
	result, err := backend.RetryAgentTaskChecks(r.Context(), principal.Subject, r.PathValue("slug"), id, input.Revision)
	switch {
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, 403, map[string]string{"error": "owner or administrator required"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, 404, map[string]string{"error": "task not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, 409, map[string]string{"error": "check observation is not retryable; refresh the task"})
	case errors.Is(err, store.ErrInvalidAgentTask):
		writeJSON(w, 400, map[string]string{"error": "revision is invalid"})
	case err != nil:
		writeJSON(w, 500, map[string]string{"error": "check retry failed"})
	default:
		writeJSON(w, 202, result)
	}
}
