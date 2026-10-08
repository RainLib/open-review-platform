package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
)

func (s *Server) getWorkspaceApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	policy, err := s.store.GetWorkspaceApprovalPolicy(r.Context(), principal.Subject, r.PathValue("slug"))
	s.writeApprovalPolicy(w, r, policy, err)
}

func (s *Server) saveWorkspaceApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	var input domain.WorkspaceApprovalPolicyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.Valid() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "both self-approval settings and expected_revision are required"})
		return
	}
	policy, err := s.store.SaveWorkspaceApprovalPolicy(r.Context(), principal.Subject, r.PathValue("slug"), input)
	s.writeApprovalPolicy(w, r, policy, err)
}

func (s *Server) writeApprovalPolicy(w http.ResponseWriter, r *http.Request, policy domain.WorkspaceApprovalPolicy, err error) {
	switch {
	case errors.Is(err, store.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "workspace membership is required to read; only an owner can change approval settings"})
	case errors.Is(err, store.ErrRevisionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "approval settings changed; reload before saving"})
	case errors.Is(err, store.ErrInvalidApprovalPolicy):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "approval settings are invalid"})
	case err != nil:
		slog.Error("workspace approval policy failed", "tenant", r.PathValue("slug"), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load or save approval settings"})
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, policy)
	}
}
