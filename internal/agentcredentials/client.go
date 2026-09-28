package agentcredentials

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/google/uuid"
)

// Source is an adapter-owned credential source. It receives no App private
// key and asks a separate broker for only the job and repository in the
// signed handoff. The broker checks current database state before issuance.
type Source struct {
	endpoint string
	secret   string
	client   *http.Client
}

func NewSource(endpoint, secret string, allowHTTP bool) (*Source, error) {
	endpoint = strings.TrimSuffix(strings.TrimSpace(endpoint), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) || len(secret) < 32 {
		return nil, fmt.Errorf("agent credential broker requires a trusted endpoint and shared secret")
	}
	return &Source{endpoint: endpoint, secret: secret, client: httpguard.NoRedirects(nil, 20*time.Second)}, nil
}

func (source *Source) Resolve(ctx context.Context, scope agentadapter.RepositoryCredentialScope) (agentadapter.RepositoryCredential, error) {
	if source == nil || scope.AttemptID == uuid.Nil || scope.AdapterJobID == "" || scope.InstallationID == uuid.Nil || !scope.Provider.Valid() || scope.APIBaseURL == "" || scope.Repository == "" {
		return agentadapter.RepositoryCredential{}, fmt.Errorf("agent credential request scope is invalid")
	}
	requestBody := Request{AttemptID: scope.AttemptID.String(), AdapterJobID: scope.AdapterJobID, InstallationID: scope.InstallationID.String(), Provider: scope.Provider, APIBaseURL: scope.APIBaseURL, Repository: scope.Repository}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return agentadapter.RepositoryCredential{}, fmt.Errorf("encode agent credential request: %w", err)
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, source.endpoint+Path, bytes.NewReader(body))
	if err != nil {
		return agentadapter.RepositoryCredential{}, fmt.Errorf("create agent credential request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, agentadapter.Sign(source.secret, timestamp, body))
	response, err := source.client.Do(request)
	if err != nil {
		return agentadapter.RepositoryCredential{}, fmt.Errorf("request agent credential: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return agentadapter.RepositoryCredential{}, fmt.Errorf("agent credential broker returned HTTP %d", response.StatusCode)
	}
	var result Response
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		result.InstallationID != requestBody.InstallationID || result.Provider != requestBody.Provider || result.APIBaseURL != requestBody.APIBaseURL || result.Repository != requestBody.Repository ||
		strings.TrimSpace(result.CloneBaseURL) == "" || strings.TrimSpace(result.Token) == "" {
		return agentadapter.RepositoryCredential{}, fmt.Errorf("agent credential broker returned an invalid scope")
	}
	return agentadapter.RepositoryCredential{CloneBaseURL: result.CloneBaseURL, Token: result.Token}, nil
}
