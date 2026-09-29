package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCreateReviewUsesEnvironmentSecretWithoutPrintingIt(t *testing.T) {
	secret := "orp_live_test-secret-never-print-this-value"
	var receivedAuthorization, receivedIdempotency string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthorization = r.Header.Get("Authorization")
		receivedIdempotency = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true,"run":{"id":"` + uuid.NewString() + `"}}`))
	}))
	defer server.Close()
	values := map[string]string{"OPEN_REVIEW_API_URL": server.URL, "OPEN_REVIEW_TENANT": "acme", "OPEN_REVIEW_API_KEY": secret}
	var output, errorOutput bytes.Buffer
	code := run(context.Background(), commandEnvironment{
		getenv: func(key string) string { return values[key] }, client: server.Client(), out: &output, errOut: &errorOutput,
	}, []string{
		"review", "create", "--installation", uuid.NewString(), "--repository", "RainLib/open-review-platform", "--number", "3",
		"--base-ref", "main", "--base-sha", "0123456789abcdef0123456789abcdef01234567",
		"--head-ref", "feature/cli", "--head-sha", "89abcdef0123456789abcdef0123456789abcdef", "--mode", "security",
	})
	if code != 0 || receivedAuthorization != "Bearer "+secret || !strings.HasPrefix(receivedIdempotency, "cli:") {
		t.Fatalf("code=%d auth=%q idempotency=%q stderr=%s", code, receivedAuthorization, receivedIdempotency, errorOutput.String())
	}
	if strings.Contains(output.String(), secret) || strings.Contains(errorOutput.String(), secret) {
		t.Fatal("secret was printed")
	}
}

func TestCLIRejectsNonLocalPlainHTTP(t *testing.T) {
	values := map[string]string{"OPEN_REVIEW_API_URL": "http://review.example.com", "OPEN_REVIEW_TENANT": "acme", "OPEN_REVIEW_API_KEY": "secret"}
	var errorOutput bytes.Buffer
	code := run(context.Background(), commandEnvironment{getenv: func(key string) string { return values[key] }, client: http.DefaultClient, out: &bytes.Buffer{}, errOut: &errorOutput}, []string{"review", "status", "--run", uuid.NewString()})
	if code != 2 || !strings.Contains(errorOutput.String(), "HTTPS") {
		t.Fatalf("code=%d stderr=%s", code, errorOutput.String())
	}
}
