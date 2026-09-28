package providercredentials

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCipherSealsWithReferenceBoundAuthenticatedEncryption(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	cipher, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal("secret://provider/gitlab-oauth/11111111-1111-1111-1111-111111111111", "token-value")
	if err != nil || string(sealed) == "token-value" {
		t.Fatalf("seal=%q err=%v", sealed, err)
	}
	opened, err := cipher.Open("secret://provider/gitlab-oauth/11111111-1111-1111-1111-111111111111", sealed)
	if err != nil || opened != "token-value" {
		t.Fatalf("opened=%q err=%v", opened, err)
	}
	if _, err := cipher.Open("secret://provider/gitlab-oauth/22222222-2222-2222-2222-222222222222", sealed); err == nil {
		t.Fatal("ciphertext opened under a different reference")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := cipher.Open("secret://provider/gitlab-oauth/11111111-1111-1111-1111-111111111111", sealed); err == nil {
		t.Fatal("tampered ciphertext opened")
	}
}
