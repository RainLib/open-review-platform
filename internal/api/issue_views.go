package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

func (s *Server) listIssueViews(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	page, err := s.store.ListIssueViews(r.Context(), principal.Subject, r.PathValue("slug"))
	if errors.Is(err, store.ErrForbidden) {
		err = store.ErrNotFound
	}
	if issueViewError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) createIssueView(w http.ResponseWriter, r *http.Request) {
	s.writeIssueView(w, r, false)
}
func (s *Server) updateIssueView(w http.ResponseWriter, r *http.Request) {
	s.writeIssueView(w, r, true)
}

func (s *Server) writeIssueView(w http.ResponseWriter, r *http.Request, update bool) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input domain.IssueSavedViewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.Valid() || (update && input.Revision < 1) || (!update && input.Revision != 0) {
		issueViewError(w, r, store.ErrInvalidIssueFilter)
		return
	}
	var view domain.IssueSavedView
	status := http.StatusCreated
	if update {
		id, parseErr := uuid.Parse(r.PathValue("viewID"))
		if parseErr != nil || id == uuid.Nil {
			issueViewError(w, r, store.ErrInvalidIssueFilter)
			return
		}
		view, err = s.store.UpdateIssueView(r.Context(), principal.Subject, r.PathValue("slug"), id, input)
		status = http.StatusOK
	} else {
		view, err = s.store.CreateIssueView(r.Context(), principal.Subject, r.PathValue("slug"), input)
	}
	if issueViewError(w, r, err) {
		return
	}
	writeJSON(w, status, view)
}

func (s *Server) deleteIssueView(w http.ResponseWriter, r *http.Request) {
	principal, err := s.auth.Authenticate(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, parseErr := uuid.Parse(r.PathValue("viewID"))
	revision, revisionErr := strconv.Atoi(r.URL.Query().Get("revision"))
	if parseErr != nil || id == uuid.Nil || revisionErr != nil || revision < 1 {
		issueViewError(w, r, store.ErrInvalidIssueFilter)
		return
	}
	err = s.store.DeleteIssueView(r.Context(), principal.Subject, r.PathValue("slug"), id, revision)
	if issueViewError(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func issueViewError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	status, message := http.StatusInternalServerError, "could not manage saved issue views"
	switch {
	case errors.Is(err, store.ErrInvalidIssueFilter):
		status, message = http.StatusBadRequest, "saved issue view is invalid"
	case errors.Is(err, store.ErrRevisionConflict):
		status, message = http.StatusConflict, "saved issue view changed; reload before retrying"
	case errors.Is(err, store.ErrConflict):
		status, message = http.StatusConflict, "saved view name exists or the 100-view scope limit was reached"
	case errors.Is(err, store.ErrNotFound):
		status, message = http.StatusNotFound, "saved issue view not found"
	case errors.Is(err, store.ErrForbidden):
		status, message = http.StatusForbidden, "not allowed to manage this saved issue view"
	default:
		slog.Error("saved issue view operation failed", "tenant", r.PathValue("slug"), "error", err)
	}
	writeJSON(w, status, map[string]string{"error": message})
	return true
}
