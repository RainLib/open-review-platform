package governance

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

var ErrArtifactKey = errors.New("data governance artifact key is invalid")

type ArtifactCipher struct {
	aead       cipher.AEAD
	keyVersion string
}

func NewArtifactCipher(encodedKey string) (ArtifactCipher, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return ArtifactCipher{}, ErrArtifactKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ArtifactCipher{}, ErrArtifactKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return ArtifactCipher{}, ErrArtifactKey
	}
	digest := sha256.Sum256(key)
	return ArtifactCipher{aead: aead, keyVersion: hex.EncodeToString(digest[:6])}, nil
}

func (c ArtifactCipher) Encrypt(jobID, tenantID string, plaintext []byte) (domain.DataGovernanceArtifact, error) {
	if c.aead == nil || jobID == "" || tenantID == "" {
		return domain.DataGovernanceArtifact{}, ErrArtifactKey
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return domain.DataGovernanceArtifact{}, fmt.Errorf("create artifact nonce: %w", err)
	}
	digest := sha256.Sum256(plaintext)
	return domain.DataGovernanceArtifact{
		KeyVersion:      c.keyVersion,
		Nonce:           nonce,
		Ciphertext:      c.aead.Seal(nil, nonce, plaintext, []byte(jobID+":"+tenantID)),
		PlaintextSHA256: hex.EncodeToString(digest[:]),
		PlaintextBytes:  int64(len(plaintext)),
	}, nil
}

func (c ArtifactCipher) Decrypt(artifact domain.DataGovernanceArtifact) ([]byte, error) {
	if c.aead == nil || artifact.KeyVersion != c.keyVersion || len(artifact.Nonce) != c.aead.NonceSize() {
		return nil, ErrArtifactKey
	}
	plaintext, err := c.aead.Open(nil, artifact.Nonce, artifact.Ciphertext, []byte(artifact.JobID.String()+":"+artifact.TenantID.String()))
	if err != nil {
		return nil, fmt.Errorf("decrypt governance artifact: %w", err)
	}
	digest := sha256.Sum256(plaintext)
	if int64(len(plaintext)) != artifact.PlaintextBytes || hex.EncodeToString(digest[:]) != artifact.PlaintextSHA256 {
		return nil, errors.New("governance artifact integrity check failed")
	}
	return plaintext, nil
}
