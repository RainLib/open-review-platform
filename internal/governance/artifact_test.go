package governance

import (
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

func TestArtifactCipherRoundTripAndTenantBinding(t *testing.T) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	cipher, err := NewArtifactCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	jobID, tenantID := uuid.New(), uuid.New()
	artifact, err := cipher.Encrypt(jobID.String(), tenantID.String(), []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	artifact.JobID, artifact.TenantID = jobID, tenantID
	plaintext, err := cipher.Decrypt(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != `{"ok":true}` {
		t.Fatalf("plaintext=%s", plaintext)
	}
	artifact.TenantID = uuid.New()
	if _, err := cipher.Decrypt(artifact); err == nil {
		t.Fatal("artifact must be cryptographically bound to its tenant")
	}
}

func TestArtifactCipherRequiresExactlyThirtyTwoBytes(t *testing.T) {
	if _, err := NewArtifactCipher(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("short key should fail")
	}
}
