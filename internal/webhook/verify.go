package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func VerifyGitHubSignature(secret string, body []byte, signature string) error {
	if secret == "" {
		return fmt.Errorf("GitHub webhook secret is not configured")
	}
	if !strings.HasPrefix(signature, "sha256=") {
		return fmt.Errorf("invalid GitHub signature format")
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return fmt.Errorf("decode GitHub signature: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return fmt.Errorf("GitHub signature mismatch")
	}
	return nil
}

func VerifyGitLabToken(secret, token string) error {
	if secret == "" {
		return fmt.Errorf("GitLab webhook secret is not configured")
	}
	if !hmac.Equal([]byte(secret), []byte(token)) {
		return fmt.Errorf("GitLab token mismatch")
	}
	return nil
}
