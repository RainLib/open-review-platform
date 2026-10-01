package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type acceptanceFixtureStore struct {
	recordingStore
	input         domain.AgentTaskAcceptanceInput
	taskID        uuid.UUID
	err           error
	retryRevision int
}

func (s *acceptanceFixtureStore) DecideAgentTaskAcceptance(_ context.Context, _ string, _ string, id uuid.UUID, input domain.AgentTaskAcceptanceInput) (domain.AgentTaskAcceptance, error) {
	s.input = input
	s.taskID = id
	return domain.AgentTaskAcceptance{TaskID: id, State: input.Decision, HeadSHA: input.HeadSHA}, s.err
}
func TestAcceptanceEndpointForwardsVersionedEvidenceAndMapsConflicts(t *testing.T) {
	backend := &acceptanceFixtureStore{}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)
	id := uuid.New()
	body := `{"revision":3,"head_sha":"` + strings.Repeat("a", 40) + `","decision":"accepted","reason":"All criteria verified","evidence":["Regression test passed"]}`
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {store.ErrForbidden, 403}, {store.ErrNotFound, 404}, {store.ErrConflict, 409}, {store.ErrInvalidAgentTask, 400}, {errors.New("internal"), 500}} {
		backend.err = tc.err
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks/"+id.String()+"/acceptance", strings.NewReader(body)))
		if response.Code != tc.status || backend.taskID != id || backend.input.Revision != 3 || len(backend.input.Evidence) != 1 {
			t.Fatalf("response=%d body=%s input=%+v", response.Code, response.Body.String(), backend.input)
		}
	}
}

func (s *acceptanceFixtureStore) RetryAgentTaskChecks(_ context.Context, _, _ string, id uuid.UUID, revision int) (domain.AgentTaskAcceptance, error) {
	s.taskID = id
	s.retryRevision = revision
	return domain.AgentTaskAcceptance{TaskID: id}, s.err
}
func TestCheckRetryEndpointPreservesRevisionAndAuthorizationErrors(t *testing.T) {
	backend := &acceptanceFixtureStore{}
	server := New(backend, fixedAuthenticator{}, "secret", "gitlab-secret")
	mux := http.NewServeMux()
	server.Register(mux)
	id := uuid.New()
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 202}, {store.ErrForbidden, 403}, {store.ErrNotFound, 404}, {store.ErrConflict, 409}, {store.ErrInvalidAgentTask, 400}} {
		backend.err = tc.err
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/agent-tasks/"+id.String()+"/retry-checks", strings.NewReader(`{"revision":4}`)))
		if response.Code != tc.status || backend.retryRevision != 4 || backend.taskID != id {
			t.Fatalf("response %d %s", response.Code, response.Body.String())
		}
	}
}
