package api

import (
	"context"
	"encoding/json"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type campaignAPIStore struct {
	recordingStore
	detail   domain.AgentCampaignDetail
	approval domain.AgentCampaignApproval
	action   domain.AgentCampaignAction
	err      error
}

func (s *campaignAPIStore) CreateAgentCampaign(context.Context, string, string, domain.AgentCampaignInput) (domain.AgentCampaign, error) {
	return s.detail.Campaign, s.err
}
func (s *campaignAPIStore) ListAgentCampaigns(context.Context, string, string, uuid.UUID) ([]domain.AgentCampaign, string, error) {
	return []domain.AgentCampaign{s.detail.Campaign}, "", s.err
}
func (s *campaignAPIStore) GetAgentCampaign(context.Context, string, string, uuid.UUID) (domain.AgentCampaignDetail, error) {
	return s.detail, s.err
}
func (s *campaignAPIStore) ApproveAgentCampaign(_ context.Context, _, _ string, _ uuid.UUID, i domain.AgentCampaignApproval) error {
	s.approval = i
	return s.err
}
func (s *campaignAPIStore) ActAgentCampaign(_ context.Context, _, _ string, _ uuid.UUID, i domain.AgentCampaignAction) error {
	s.action = i
	return s.err
}
func (s *campaignAPIStore) AgentCampaignRepositories(context.Context, string, string, []uuid.UUID) (store.CampaignRepositoryInventory, error) {
	return store.CampaignRepositoryInventory{Complete: true}, s.err
}
func TestCampaignAPIForwardsFrozenApprovalsAndAuthenticatedReports(t *testing.T) {
	backend := &campaignAPIStore{detail: domain.AgentCampaignDetail{Campaign: domain.AgentCampaign{ID: uuid.New(), RequestSHA256: strings.Repeat("a", 64), Input: domain.AgentCampaignInput{Title: "Report fixture"}}, Targets: []domain.AgentCampaignTarget{{Repository: "team/repo", State: "scan_complete", Scan: domain.AgentCampaignScan{Complete: true, BaseSHA: strings.Repeat("b", 40)}}}}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)
	base := "/v1/tenants/acme/agent-campaigns/" + backend.detail.Campaign.ID.String()
	for _, format := range []string{"markdown", "csv", "json"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("GET", base+"/report?format="+format, nil))
		if response.Code != 200 || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "team/repo") {
			t.Fatalf("%s report: %d %s", format, response.Code, response.Body.String())
		}
	}
	input := domain.AgentCampaignApproval{Revision: 3, Plans: []domain.AgentCampaignPlanApproval{{TaskID: uuid.New(), PlanID: uuid.New(), Revision: 2, SHA256: strings.Repeat("c", 64)}}}
	raw, _ := json.Marshal(input)
	for _, c := range []struct {
		err    error
		status int
	}{{nil, 202}, {store.ErrForbidden, 403}, {store.ErrConflict, 409}, {store.ErrInvalidAgentTaskPlan, 400}, {store.ErrNotFound, 404}} {
		backend.err = c.err
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("POST", base+"/approve", strings.NewReader(string(raw))))
		if response.Code != c.status || len(backend.approval.Plans) != 1 || backend.approval.Plans[0].SHA256 != input.Plans[0].SHA256 {
			t.Fatalf("approval envelope: %d %s", response.Code, response.Body.String())
		}
	}
}
