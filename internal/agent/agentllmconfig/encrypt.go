package agentllmconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Encrypter wraps AES-256-GCM with the symmetric key from
// AGENT_CONFIG_ENCRYPTION_KEY. The key is loaded once at boot.
//
// Why not pgcrypto? Two reasons. (1) Postgres-side encryption means the key
// has to travel through SQL — easy to leak via query logs. (2) We want a
// clean separation: cipher in code, ciphertext in DB. If the key rotates
// we can re-encrypt without an ALTER.
type Encrypter struct {
	gcm cipher.AEAD
}

// NewEncrypter parses the hex-encoded 32-byte key (or empty for a NoOp
// implementation suitable for tests). Production callers MUST supply a key
// — Save() rejects writes when no encrypter is configured.
func NewEncrypter(hexKey string) (*Encrypter, error) {
	if hexKey == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key: must be 64 hex chars (32 bytes), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encrypter{gcm: gcm}, nil
}

// Encrypt returns (ciphertext, nonce). Nonce is fresh per call.
func (e *Encrypter) Encrypt(plaintext []byte) (ct, nonce []byte, err error) {
	if e == nil {
		return nil, nil, errors.New("encrypter: not configured (AGENT_CONFIG_ENCRYPTION_KEY missing)")
	}
	nonce = make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ct = e.gcm.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

// Decrypt verifies the GCM tag and returns plaintext. Nonce + ciphertext
// must be the ones returned by Encrypt; tampering will surface as an
// error rather than silently return wrong bytes.
func (e *Encrypter) Decrypt(ct, nonce []byte) ([]byte, error) {
	if e == nil {
		return nil, errors.New("encrypter: not configured")
	}
	if len(ct) == 0 && len(nonce) == 0 {
		return nil, nil
	}
	return e.gcm.Open(nil, nonce, ct, nil)
}

// Last4 returns the last 4 chars of the plaintext for the UI's "•••• 1a2b"
// display. Pure convenience — callers could compute it inline. Returns
// empty if the input is shorter than 4 chars (no need to inflate a
// secret to "leak" any of it).
func Last4(plaintext string) string {
	if len(plaintext) < 4 {
		return ""
	}
	return plaintext[len(plaintext)-4:]
}
