package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestListTenantsReturnsOnlyAuthenticatedMemberships(t *testing.T) {
	recording := &recordingStore{tenants: []domain.TenantSummary{
		{Slug: "acme", Name: "Acme", Role: "owner"},
	}}
	server := New(recording, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants?limit=2", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if recording.listTenantsActor != "operator" || recording.listTenantsLimit != 2 {
		t.Fatalf("unexpected store request: actor=%q limit=%d", recording.listTenantsActor, recording.listTenantsLimit)
	}
	if body := response.Body.String(); !strings.Contains(body, `"slug":"acme"`) || strings.Contains(body, "subject") {
		t.Fatalf("unexpected workspace response: %s", body)
	}
}

func TestListTenantsRejectsInvalidLimit(t *testing.T) {
	server := New(&recordingStore{}, fixedAuthenticator{}, "", "")
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/v1/tenants?limit=101", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
