package domain

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	APIKeyScopeReviewsRead   = "reviews:read"
	APIKeyScopeReviewsCreate = "reviews:create"
	APIKeyScopeRunsCancel    = "runs:cancel"
)

var validAPIKeyScopes = map[string]struct{}{
	APIKeyScopeReviewsRead:   {},
	APIKeyScopeReviewsCreate: {},
	APIKeyScopeRunsCancel:    {},
}

type APIKey struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Prefix        string     `json:"prefix"`
	CallerSubject string     `json:"caller_subject"`
	Scopes        []string   `json:"scopes"`
	Repositories  []string   `json:"repositories"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	RevokedBy     string     `json:"revoked_by,omitempty"`
}

type APIKeyInput struct {
	Name         string     `json:"name"`
	Scopes       []string   `json:"scopes"`
	Repositories []string   `json:"repositories,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type APIKeyCreation struct {
	APIKey APIKey `json:"api_key"`
	Secret string `json:"secret"`
}

type APIKeyPrincipal struct {
	KeyID        uuid.UUID
	TenantID     uuid.UUID
	TenantSlug   string
	Subject      string
	Scopes       []string
	Repositories []string
}

func NormalizeAPIKeyInput(input APIKeyInput, now time.Time) (APIKeyInput, bool) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 80 {
		return APIKeyInput{}, false
	}
	input.Scopes = normalizedUniqueStrings(input.Scopes)
	if len(input.Scopes) == 0 || len(input.Scopes) > len(validAPIKeyScopes) {
		return APIKeyInput{}, false
	}
	for _, scope := range input.Scopes {
		if _, ok := validAPIKeyScopes[scope]; !ok {
			return APIKeyInput{}, false
		}
	}
	input.Repositories = normalizedUniqueStrings(input.Repositories)
	if len(input.Repositories) > 100 {
		return APIKeyInput{}, false
	}
	for _, repository := range input.Repositories {
		parts := strings.Split(repository, "/")
		if len(parts) < 2 || len(parts) > 4 {
			return APIKeyInput{}, false
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." || len(part) > 100 {
				return APIKeyInput{}, false
			}
		}
	}
	if input.ExpiresAt != nil {
		expiresAt := input.ExpiresAt.UTC()
		if !expiresAt.After(now.UTC().Add(time.Minute)) || expiresAt.After(now.UTC().AddDate(2, 0, 0)) {
			return APIKeyInput{}, false
		}
		input.ExpiresAt = &expiresAt
	}
	return input, true
}

func (p APIKeyPrincipal) HasScope(scope string) bool {
	for _, candidate := range p.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func (p APIKeyPrincipal) AllowsRepository(repository string) bool {
	if len(p.Repositories) == 0 {
		return true
	}
	for _, candidate := range p.Repositories {
		if candidate == repository {
			return true
		}
	}
	return false
}

func normalizedUniqueStrings(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
