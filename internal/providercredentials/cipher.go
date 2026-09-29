// Package providercredentials protects provider OAuth material while it is at
// rest in the control-plane database. It intentionally exposes only opaque
// credential references to callers outside the worker trust boundary.
package providercredentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const aadPrefix = "open-review/provider-credential/v1/"

// Cipher uses a deployment-owned 256-bit key. Production deployments should
// source this value from a KMS/Vault injection mechanism; this local envelope
// only ensures the control-plane database never contains provider tokens in
// plaintext.
type Cipher struct {
	aead cipher.AEAD
}

func New(encodedKey string) (Cipher, error) {
	value := strings.TrimSpace(encodedKey)
	if value == "" {
		return Cipher{}, fmt.Errorf("provider credential encryption key is required")
	}
	key, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(value)
	}
	if err != nil || len(key) != 32 {
		return Cipher{}, fmt.Errorf("provider credential encryption key must be base64 encoded 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Cipher{}, fmt.Errorf("create provider credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Cipher{}, fmt.Errorf("create provider credential AEAD: %w", err)
	}
	return Cipher{aead: aead}, nil
}

func (c Cipher) Ready() bool { return c.aead != nil }

func (c Cipher) Seal(reference, value string) ([]byte, error) {
	if c.aead == nil || strings.TrimSpace(reference) == "" || strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("provider credential encryption input is invalid")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate provider credential nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(value), []byte(aadPrefix+reference))
	return append(nonce, sealed...), nil
}

func (c Cipher) Open(reference string, value []byte) (string, error) {
	if c.aead == nil || strings.TrimSpace(reference) == "" || len(value) <= c.aead.NonceSize() {
		return "", fmt.Errorf("provider credential decryption input is invalid")
	}
	plain, err := c.aead.Open(nil, value[:c.aead.NonceSize()], value[c.aead.NonceSize():], []byte(aadPrefix+reference))
	if err != nil || len(plain) == 0 {
		return "", fmt.Errorf("provider credential cannot be decrypted")
	}
	return string(plain), nil
}
