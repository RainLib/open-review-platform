package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

func (s *recordingStore) ListIssueViews(_ context.Context, actor, slug string) (domain.IssueSavedViewPage, error) {
	s.issueViewActor, s.issueViewTenant = actor, slug
	return s.issueViewPage, s.issueViewErr
}
func (s *recordingStore) CreateIssueView(_ context.Context, actor, slug string, input domain.IssueSavedViewInput) (domain.IssueSavedView, error) {
	s.issueViewActor, s.issueViewTenant, s.issueViewInput = actor, slug, input
	return s.issueSavedView, s.issueViewErr
}
func (s *recordingStore) UpdateIssueView(ctx context.Context, actor, slug string, id uuid.UUID, input domain.IssueSavedViewInput) (domain.IssueSavedView, error) {
	s.issueViewID = id
	return s.CreateIssueView(ctx, actor, slug, input)
}
func (s *recordingStore) DeleteIssueView(_ context.Context, actor, slug string, id uuid.UUID, revision int) error {
	s.issueViewActor, s.issueViewTenant, s.issueViewID, s.issueViewRevision = actor, slug, id, revision
	return s.issueViewErr
}

func TestIssueSavedViewAPIContract(t *testing.T) {
	id := uuid.New()
	backend := &recordingStore{issueSavedView: domain.IssueSavedView{ID: id, Revision: 1}, issueViewPage: domain.IssueSavedViewPage{Views: []domain.IssueSavedView{}, CanCreateWorkspace: true}}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
		return recorder
	}
	base := "/v1/tenants/acme/issue-views"
	definition := `"definition":{"view":"open","filters":{"condition":"and","items":[]}}`
	response := request("POST", base, `{"name":"My queue","visibility":"personal",`+definition+`}`)
	if response.Code != 201 || backend.issueViewActor != "operator" || backend.issueViewTenant != "acme" || backend.issueViewInput.Name != "My queue" {
		t.Fatalf("create=%d %s input=%#v", response.Code, response.Body, backend.issueViewInput)
	}
	response = request("GET", base, "")
	var page domain.IssueSavedViewPage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || !page.CanCreateWorkspace {
		t.Fatalf("list=%d %s", response.Code, response.Body)
	}
	response = request("PUT", base+"/"+id.String(), `{"name":"Updated","visibility":"workspace","revision":2,`+definition+`}`)
	if response.Code != 200 || backend.issueViewID != id || backend.issueViewInput.Revision != 2 {
		t.Fatalf("update=%d %s", response.Code, response.Body)
	}
	response = request("DELETE", base+"/"+id.String()+"?revision=2", "")
	if response.Code != 204 || response.Body.Len() != 0 || backend.issueViewRevision != 2 {
		t.Fatalf("delete=%d %s", response.Code, response.Body)
	}
	for _, body := range []string{`{"name":"Bad","visibility":"personal","definition":{"view":"all"}}`, `{"name":"Bad","visibility":"personal","definition":{"view":"all","filters":{"condition":"or","items":[]}}}`, `{"name":"Bad","visibility":"personal","revision":1,` + definition + `}`, `{"name":"Bad","visibility":"personal","definition":{"view":"all","filters":{"condition":"and","items":null}}}`} {
		if response := request("POST", base, body); response.Code != 400 {
			t.Fatalf("accepted invalid payload %s: %d", body, response.Code)
		}
	}
	for _, fixture := range []struct {
		err    error
		status int
	}{{store.ErrForbidden, 403}, {store.ErrNotFound, 404}, {store.ErrRevisionConflict, 409}, {store.ErrConflict, 409}, {store.ErrInvalidIssueFilter, 400}} {
		backend.issueViewErr = fixture.err
		if response := request("DELETE", base+"/"+id.String()+"?revision=2", ""); response.Code != fixture.status {
			t.Fatalf("%v mapped to %d", fixture.err, response.Code)
		}
	}
}

func TestIssueGroupedFilterAPIValidation(t *testing.T) {
	backend := &recordingStore{}
	mux := http.NewServeMux()
	New(backend, fixedAuthenticator{}, "secret", "gitlab-secret").Register(mux)
	filters := `{"condition":"or","items":[{"field":"severity","operator":"is","value":"critical"},{"condition":"and","items":[{"field":"age","operator":"within","value":"7d"}]}]}`
	path := "/v1/tenants/acme/issues?view=all&filters=" + url.QueryEscape(filters) + "&filter_time=2026-09-21T10%3A00%3A00Z"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
	if response.Code != 200 || backend.issueFilter.Filters == nil || backend.issueFilter.FilterTime == nil || backend.issueFilter.View != "all" {
		t.Fatalf("filter=%#v response=%d %s", backend.issueFilter, response.Code, response.Body)
	}
	for _, query := range []string{"view=invalid", "filter_time=invalid", "filters=", "filters=" + url.QueryEscape(`{"condition":"and","items":[],"field":""}`), "filters=" + url.QueryEscape(`{"condition":"and","items":[{"field":"status","operator":"contains","value":"open"}]}`)} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("GET", "/v1/tenants/acme/issues?"+query, nil))
		if response.Code != 400 {
			t.Fatalf("query %s accepted: %d %s", query, response.Code, response.Body)
		}
	}
}
