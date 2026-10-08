package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func (s *recordingStore) GetWorkspaceApprovalPolicy(context.Context, string, string) (domain.WorkspaceApprovalPolicy, error) {
	return domain.WorkspaceApprovalPolicy{Revision: 1}, nil
}
func (s *recordingStore) SaveWorkspaceApprovalPolicy(context.Context, string, string, domain.WorkspaceApprovalPolicyInput) (domain.WorkspaceApprovalPolicy, error) {
	return domain.WorkspaceApprovalPolicy{Revision: 2}, nil
}

func TestApprovalPolicyRequiresExplicitBooleansAndRevision(t *testing.T) {
	server := New(&recordingStore{}, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{}`, 400},
		{`{"allow_agent_plan_self_approval":true,"expected_revision":1}`, 400},
		{`{"allow_agent_plan_self_approval":true,"allow_rule_self_approval":null,"expected_revision":1}`, 400},
		{`{"allow_agent_plan_self_approval":false,"allow_rule_self_approval":false,"expected_revision":0}`, 400},
		{`{"allow_agent_plan_self_approval":false,"allow_rule_self_approval":false,"expected_revision":1}`, 200},
	} {
		r := httptest.NewRequest(http.MethodPut, "/v1/tenants/acme/approval-policy", strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("body=%s status=%d response=%s", tc.body, w.Code, w.Body.String())
		}
	}
}
