package agentcredentials

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const maxCodingInstallationMapBytes = 1 << 20

type GitHubWriteTokenSource interface {
	RepositoryWriteToken(context.Context, string, string) (string, error)
}

// GitHubIssuer always requires an explicit, deployment-owned mapping from a
// review installation to a coding App installation. It never guesses that
// the two App identities share an external installation ID.
type GitHubIssuer struct {
	APIBaseURL string
	MapPath    string
	Tokens     GitHubWriteTokenSource
}

type codingInstallationMapping struct {
	TenantID                     string `json:"tenant_id"`
	ReviewInstallationID         string `json:"review_installation_id"`
	ReviewInstallationExternalID string `json:"review_installation_external_id"`
	APIBaseURL                   string `json:"api_base_url"`
	Repository                   string `json:"repository"`
	CodingInstallationExternalID string `json:"coding_installation_external_id"`
	CloneBaseURL                 string `json:"clone_base_url"`
}

func (issuer GitHubIssuer) Validate() error {
	if issuer.Tokens == nil || issuer.APIBaseURL == "" {
		return fmt.Errorf("GitHub coding issuer is not configured")
	}
	entries, err := loadCodingInstallationMap(issuer.MapPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.APIBaseURL != issuer.APIBaseURL {
			return fmt.Errorf("coding App installation map targets another GitHub API origin")
		}
	}
	return nil
}

func (issuer GitHubIssuer) Issue(ctx context.Context, grant store.AgentTaskCredentialGrant) (string, string, error) {
	if issuer.Tokens == nil || grant.Provider != domain.ProviderGitHub || grant.APIBaseURL != issuer.APIBaseURL || grant.TenantID == uuid.Nil || grant.InstallationID == uuid.Nil {
		return "", "", fmt.Errorf("GitHub coding issuer does not own this grant")
	}
	entries, err := loadCodingInstallationMap(issuer.MapPath)
	if err != nil {
		return "", "", err
	}
	for _, entry := range entries {
		if entry.TenantID != grant.TenantID.String() || entry.ReviewInstallationID != grant.InstallationID.String() || entry.ReviewInstallationExternalID != grant.ReviewInstallationExternalID || entry.APIBaseURL != grant.APIBaseURL || entry.Repository != grant.Repository {
			continue
		}
		token, err := issuer.Tokens.RepositoryWriteToken(ctx, entry.CodingInstallationExternalID, grant.Repository)
		if err != nil {
			return "", "", fmt.Errorf("mint task-bound GitHub coding token: %w", err)
		}
		return entry.CloneBaseURL, token, nil
	}
	return "", "", fmt.Errorf("no coding App installation maps to this task grant")
}

func loadCodingInstallationMap(path string) ([]codingInstallationMapping, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("coding App installation map is not configured")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("coding App installation map must be a private regular file")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxCodingInstallationMapBytes {
		return nil, fmt.Errorf("coding App installation map must be a private regular file")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxCodingInstallationMapBytes+1))
	if err != nil || len(contents) > maxCodingInstallationMapBytes {
		return nil, fmt.Errorf("coding App installation map cannot be read safely")
	}
	var document struct {
		Version int                         `json:"version"`
		Entries []codingInstallationMapping `json:"entries"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(&struct{}{}) != io.EOF || document.Version != 1 || len(document.Entries) == 0 || len(document.Entries) > 1000 {
		return nil, fmt.Errorf("coding App installation map format is invalid")
	}
	seen := make(map[string]bool, len(document.Entries))
	for _, entry := range document.Entries {
		tenantID, tenantErr := uuid.Parse(entry.TenantID)
		reviewID, reviewErr := uuid.Parse(entry.ReviewInstallationID)
		codingID, codingErr := strconv.ParseInt(entry.CodingInstallationExternalID, 10, 64)
		if tenantErr != nil || tenantID == uuid.Nil || reviewErr != nil || reviewID == uuid.Nil || codingErr != nil || codingID <= 0 ||
			entry.TenantID != tenantID.String() || entry.ReviewInstallationID != reviewID.String() || entry.CodingInstallationExternalID != strconv.FormatInt(codingID, 10) ||
			entry.ReviewInstallationExternalID == "" || entry.APIBaseURL == "" || entry.Repository == "" || !validCodingCloneBase(entry.APIBaseURL, entry.CloneBaseURL) {
			return nil, fmt.Errorf("coding App installation map has an invalid entry")
		}
		key := entry.TenantID + "\x00" + entry.ReviewInstallationID + "\x00" + entry.APIBaseURL + "\x00" + entry.Repository
		if seen[key] {
			return nil, fmt.Errorf("coding App installation map has a duplicate task scope")
		}
		seen[key] = true
	}
	return document.Entries, nil
}

func validCodingCloneBase(apiBase, cloneBase string) bool {
	api, apiErr := url.Parse(apiBase)
	clone, cloneErr := url.Parse(cloneBase)
	if apiErr != nil || cloneErr != nil || api.Scheme != "https" || clone.Scheme != "https" || api.Host == "" || clone.Host == "" ||
		api.User != nil || clone.User != nil || api.RawQuery != "" || clone.RawQuery != "" || api.Fragment != "" || clone.Fragment != "" ||
		clone.Path != "" || api.Path == "/" {
		return false
	}
	if api.Host == "api.github.com" {
		return api.Path == "" && clone.Host == "github.com"
	}
	return strings.EqualFold(api.Host, clone.Host) && (api.Path == "" || api.Path == "/api/v3")
}
