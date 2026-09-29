package agentcredentials

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/httpguard"
	"github.com/RainLib/open-review-platform/internal/store"
)

// GitLabIssuer keeps deployment-owned coding credentials in the broker, not
// the review workers or adapter. The service verifies the active task grant
// before this issuer reads the exact installation/repository mapping.
type GitLabIssuer struct {
	Credentials agentadapter.FileCredentialSource
	AllowHTTP   bool
	HTTPClient  *http.Client
}

func (issuer GitLabIssuer) Validate() error {
	return issuer.Credentials.ValidateScopes(func(scope agentadapter.CredentialMapScope) bool {
		return scope.Provider == domain.ProviderGitLab &&
			validGitLabCodingCloneBase(scope.APIBaseURL, scope.CloneBaseURL, issuer.AllowHTTP)
	})
}

func (issuer GitLabIssuer) Issue(ctx context.Context, grant store.AgentTaskCredentialGrant) (string, string, error) {
	if grant.Provider != domain.ProviderGitLab {
		return "", "", fmt.Errorf("GitLab coding issuer does not own this grant")
	}
	credential, err := issuer.Credentials.Resolve(ctx, agentadapter.RepositoryCredentialScope{
		InstallationID: grant.InstallationID, Provider: grant.Provider,
		APIBaseURL: grant.APIBaseURL, Repository: grant.Repository,
	})
	if err != nil {
		return "", "", fmt.Errorf("resolve GitLab coding credential: %w", err)
	}
	if !validGitLabCodingCloneBase(grant.APIBaseURL, credential.CloneBaseURL, issuer.AllowHTTP) {
		return "", "", fmt.Errorf("GitLab coding clone origin does not match the admitted API")
	}
	if err := issuer.verifyProjectToken(ctx, grant, credential.Token); err != nil {
		return "", "", err
	}
	return credential.CloneBaseURL, credential.Token, nil
}

// A deployment-owned mapping is not evidence that its token is repository
// scoped. GitLab project access tokens authenticate as project_<id>_bot_*
// users; a personal or group token must never cross this write boundary even
// when an operator accidentally maps it to only one repository. This is a
// read-only provider check on every issuance, so revoked/rotated tokens fail
// before they are returned to the adapter.
func (issuer GitLabIssuer) verifyProjectToken(ctx context.Context, grant store.AgentTaskCredentialGrant, token string) error {
	client := httpguard.NoRedirects(issuer.HTTPClient, 10*time.Second)
	read := func(path string, target any) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(grant.APIBaseURL, "/")+path, nil)
		if err != nil {
			return err
		}
		request.Header.Set("PRIVATE-TOKEN", token)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("GitLab returned HTTP %d", response.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		if err != nil || len(body) > 64<<10 {
			return fmt.Errorf("GitLab identity response is invalid")
		}
		if err := json.Unmarshal(body, target); err != nil {
			return fmt.Errorf("GitLab identity response is invalid")
		}
		return nil
	}
	var project struct {
		ID                int64  `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
	}
	if err := read("/projects/"+url.PathEscape(grant.Repository), &project); err != nil ||
		project.ID <= 0 || !strings.EqualFold(project.PathWithNamespace, grant.Repository) {
		return fmt.Errorf("GitLab coding token cannot verify the admitted project")
	}
	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := read("/user", &user); err != nil ||
		user.ID <= 0 ||
		!strings.HasPrefix(user.Username, fmt.Sprintf("project_%d_bot_", project.ID)) ||
		len(user.Username) <= len(fmt.Sprintf("project_%d_bot_", project.ID)) {
		return fmt.Errorf("GitLab coding token is not bound to the admitted project bot")
	}
	// GitLab's token-self endpoint exposes the effective token scopes without
	// requiring a maintainer credential. The project bot identity check above
	// remains necessary: scopes alone cannot distinguish a project token from a
	// personal or group token. Git-over-HTTP push and MR API writes need separate
	// scopes for project tokens.
	var tokenInfo struct {
		UserID  int64    `json:"user_id"`
		Scopes  []string `json:"scopes"`
		Active  *bool    `json:"active"`
		Revoked *bool    `json:"revoked"`
	}
	if err := read("/personal_access_tokens/self", &tokenInfo); err != nil {
		return fmt.Errorf("GitLab coding token scopes could not be verified: %w", err)
	}
	api, repositoryWrite := false, false
	for _, scope := range tokenInfo.Scopes {
		switch scope {
		case "api":
			api = true
		case "write_repository":
			repositoryWrite = true
		}
	}
	if tokenInfo.UserID != user.ID || tokenInfo.Active == nil || !*tokenInfo.Active ||
		tokenInfo.Revoked == nil || *tokenInfo.Revoked || !api || !repositoryWrite {
		return fmt.Errorf("GitLab coding token lacks active project-scoped API and repository-write access")
	}
	return nil
}

func validGitLabCodingCloneBase(apiBase, cloneBase string, allowHTTP bool) bool {
	api, apiErr := url.Parse(strings.TrimSuffix(strings.TrimSpace(apiBase), "/"))
	clone, cloneErr := url.Parse(strings.TrimSuffix(strings.TrimSpace(cloneBase), "/"))
	if apiErr != nil || cloneErr != nil || api.Host == "" || clone.Host == "" ||
		api.Scheme != clone.Scheme || (api.Scheme != "https" && !(allowHTTP && api.Scheme == "http")) ||
		api.User != nil || clone.User != nil || api.RawQuery != "" || clone.RawQuery != "" ||
		api.Fragment != "" || clone.Fragment != "" || api.RawPath != "" || clone.RawPath != "" ||
		!strings.EqualFold(api.Host, clone.Host) {
		return false
	}
	return api.Path == strings.TrimSuffix(clone.Path, "/")+"/api/v4"
}

// ProviderIssuer routes an already-authorized grant to its independently
// configured coding identity. An unconfigured provider fails closed.
type ProviderIssuer struct {
	GitHub Issuer
	GitLab Issuer
}

func (issuer ProviderIssuer) Issue(ctx context.Context, grant store.AgentTaskCredentialGrant) (string, string, error) {
	switch grant.Provider {
	case domain.ProviderGitHub:
		if issuer.GitHub != nil {
			return issuer.GitHub.Issue(ctx, grant)
		}
	case domain.ProviderGitLab:
		if issuer.GitLab != nil {
			return issuer.GitLab.Issue(ctx, grant)
		}
	}
	return "", "", fmt.Errorf("no coding issuer is configured for this provider")
}
