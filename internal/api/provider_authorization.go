package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

// providerAuthorizationHeader carries the opaque, short-lived receipt that
// the Console BFF obtained after a provider-managed redirect or an authorized
// selection of a deployment-owned credential slot. It is never accepted from
// browser JavaScript as an installation identifier.
const providerAuthorizationHeader = "X-Open-Review-Provider-Authorization"

type providerAuthorizationReceipt struct {
	ActorExternalID string          `json:"actorExternalID"`
	CredentialRef   string          `json:"credentialRef"`
	ExpiresAt       int64           `json:"expiresAt"`
	ExternalID      string          `json:"externalID"`
	Provider        domain.Provider `json:"provider"`
	Tenant          string          `json:"tenant"`
	Version         int             `json:"version"`
}

func verifyProviderAuthorizationReceipt(value, secret string, now time.Time) (providerAuthorizationReceipt, bool) {
	if len(secret) < 32 || value == "" {
		return providerAuthorizationReceipt{}, false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return providerAuthorizationReceipt{}, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	expectedSignature := mac.Sum(nil)
	receivedSignature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expectedSignature, receivedSignature) {
		return providerAuthorizationReceipt{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return providerAuthorizationReceipt{}, false
	}
	var receipt providerAuthorizationReceipt
	if err := json.Unmarshal(payload, &receipt); err != nil ||
		receipt.Version != 1 ||
		receipt.ExpiresAt <= now.UnixMilli() ||
		!validProviderAuthorizationTenant(receipt.Tenant) ||
		(receipt.Provider != domain.ProviderGitHub && receipt.Provider != domain.ProviderGitLab) ||
		strings.TrimSpace(receipt.ExternalID) == "" {
		return providerAuthorizationReceipt{}, false
	}
	receipt.ExternalID = strings.TrimSpace(receipt.ExternalID)
	receipt.ActorExternalID = strings.TrimSpace(receipt.ActorExternalID)
	if receipt.ActorExternalID != "" && !validProviderActorID(receipt.ActorExternalID) {
		return providerAuthorizationReceipt{}, false
	}
	receipt.CredentialRef = strings.TrimSpace(receipt.CredentialRef)
	if receipt.CredentialRef != "" &&
		(receipt.Provider != domain.ProviderGitLab || !validGitLabOAuthCredentialReference(receipt.CredentialRef)) {
		return providerAuthorizationReceipt{}, false
	}
	return receipt, true
}

func validProviderActorID(value string) bool {
	if len(value) == 0 || len(value) > 19 || value[0] < '1' || value[0] > '9' {
		return false
	}
	for _, digit := range value[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func validGitLabOAuthCredentialReference(value string) bool {
	const prefix = "secret://provider/gitlab-oauth/"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	return err == nil
}

func validProviderAuthorizationTenant(tenant string) bool {
	if len(tenant) < 1 || len(tenant) > 63 {
		return false
	}
	for index, character := range tenant {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			continue
		}
		if character == '-' && index > 0 && index < len(tenant)-1 {
			continue
		}
		return false
	}
	return true
}

func gitLabInstallationIdentity(secret, tenant, repositoryScope string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("open-review/gitlab-installation/v1/" + tenant + "\x00" + repositoryScope))
	return "gitlab-scope:" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
