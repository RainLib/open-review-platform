package providertransport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderClientRejectsPrivateAddressUnlessDeploymentAllowsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if response, err := NewClient(false).Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("loopback provider address was allowed by default")
	}
	response, err := NewClient(true).Get(server.URL)
	if err != nil {
		t.Fatalf("explicit local provider address was rejected: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", response.StatusCode)
	}
}
