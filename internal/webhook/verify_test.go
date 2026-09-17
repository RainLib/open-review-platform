package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestVerifyGitHubSignature(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	signature := fmt.Sprintf("sha256=%x", mac.Sum(nil))
	if err := VerifyGitHubSignature("secret", body, signature); err != nil {
		t.Fatalf("expected valid signature: %v", err)
	}
	if err := VerifyGitHubSignature("other", body, signature); err == nil {
		t.Fatal("expected invalid signature")
	}
}

func TestVerifyGitLabToken(t *testing.T) {
	if err := VerifyGitLabToken("secret", "secret"); err != nil {
		t.Fatalf("expected token to verify: %v", err)
	}
	if err := VerifyGitLabToken("secret", "wrong"); err == nil {
		t.Fatal("expected token mismatch")
	}
}
