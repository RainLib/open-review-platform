package api

import (
	"context"
	"errors"
	"github.com/RainLib/open-review-platform/internal/agentcampaign"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"net/http"
	"strings"
)

type agentCampaignStore interface {
	CreateAgentCampaign(context.Context, string, string, domain.AgentCampaignInput) (domain.AgentCampaign, error)
	ListAgentCampaigns(context.Context, string, string, uuid.UUID) ([]domain.AgentCampaign, string, error)
	GetAgentCampaign(context.Context, string, string, uuid.UUID) (domain.AgentCampaignDetail, error)
	ApproveAgentCampaign(context.Context, string, string, uuid.UUID, domain.AgentCampaignApproval) error
	ActAgentCampaign(context.Context, string, string, uuid.UUID, domain.AgentCampaignAction) error
	AgentCampaignRepositories(context.Context, string, string, []uuid.UUID) (store.CampaignRepositoryInventory, error)
}

func (s *Server) campaignStore(w http.ResponseWriter) (agentCampaignStore, bool) {
	backend, ok := s.store.(agentCampaignStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "campaign service is unavailable"})
	}
	return backend, ok
}
func campaignError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusInternalServerError
	message := "campaign operation could not complete"
	switch {
	case errors.Is(err, store.ErrForbidden):
		status = http.StatusForbidden
		message = "Workspace owner/admin permission and the configured self-approval policy are required."
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
		message = "Campaign or repository is unavailable in this workspace."
	case errors.Is(err, store.ErrAgentTaskDisabled):
		status = http.StatusConflict
		message = "Enable manual Agent mode, complete workflow, criterion evidence and independent checks for every selected repository."
	case errors.Is(err, store.ErrConflict):
		status = http.StatusConflict
		message = "Campaign revision, inventory or approved plan changed. Refresh and inspect the current evidence."
	case errors.Is(err, store.ErrInvalidAgentTask) || errors.Is(err, store.ErrInvalidAgentTaskPlan):
		status = http.StatusBadRequest
		message = "Campaign request or plan is invalid. Check requirements, criteria, scope and budgets."
	}
	writeJSON(w, status, map[string]string{"error": message})
	return true
}
func (s *Server) createAgentCampaign(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	backend, ok := s.campaignStore(w)
	if !ok {
		return
	}
	var input domain.AgentCampaignInput
	if !decodeJSON(w, r, &input) {
		return
	}
	c, err := backend.CreateAgentCampaign(r.Context(), principal.Subject, r.PathValue("slug"), input)
	if campaignError(w, err) {
		return
	}
	writeJSON(w, http.StatusAccepted, c)
}
func (s *Server) listAgentCampaigns(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	backend, ok := s.campaignStore(w)
	if !ok {
		return
	}
	cursor := uuid.Nil
	if v := r.URL.Query().Get("cursor"); v != "" {
		var err error
		cursor, err = uuid.Parse(v)
		if err != nil {
			campaignError(w, store.ErrInvalidAgentTask)
			return
		}
	}
	items, next, err := backend.ListAgentCampaigns(r.Context(), principal.Subject, r.PathValue("slug"), cursor)
	if campaignError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": items, "next_cursor": next})
}
func (s *Server) getAgentCampaign(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	backend, ok := s.campaignStore(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("campaignID"))
	if err != nil {
		campaignError(w, store.ErrNotFound)
		return
	}
	d, err := backend.GetAgentCampaign(r.Context(), principal.Subject, r.PathValue("slug"), id)
	if campaignError(w, err) {
		return
	}
	if strings.HasSuffix(r.URL.Path, "/report") {
		format := r.URL.Query().Get("format")
		if format == "" {
			format = "markdown"
		}
		raw, kind, e := agentcampaign.Export(d, format, s.now())
		if e != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": e.Error()})
			return
		}
		extension := format
		if format == "markdown" {
			extension = "md"
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("Content-Disposition", `attachment; filename="campaign-`+id.String()+`.`+extension+`"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
func (s *Server) approveAgentCampaign(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	backend, ok := s.campaignStore(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("campaignID"))
	if err != nil {
		campaignError(w, store.ErrNotFound)
		return
	}
	var input domain.AgentCampaignApproval
	if !decodeJSON(w, r, &input) {
		return
	}
	if campaignError(w, backend.ApproveAgentCampaign(r.Context(), principal.Subject, r.PathValue("slug"), id, input)) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "approved"})
}
func (s *Server) actAgentCampaign(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	backend, ok := s.campaignStore(w)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("campaignID"))
	if err != nil {
		campaignError(w, store.ErrNotFound)
		return
	}
	var input domain.AgentCampaignAction
	if !decodeJSON(w, r, &input) {
		return
	}
	if campaignError(w, backend.ActAgentCampaign(r.Context(), principal.Subject, r.PathValue("slug"), id, input)) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "updated"})
}
func (s *Server) agentCampaignRepositories(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.authenticateUser(w, r)
	if !ok {
		return
	}
	backend, ok := s.campaignStore(w)
	if !ok {
		return
	}
	ids := []uuid.UUID{}
	for _, v := range r.URL.Query()["installation_id"] {
		id, err := uuid.Parse(v)
		if err != nil {
			campaignError(w, store.ErrInvalidAgentTask)
			return
		}
		ids = append(ids, id)
	}
	inventory, err := backend.AgentCampaignRepositories(r.Context(), principal.Subject, r.PathValue("slug"), ids)
	if campaignError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, inventory)
}
