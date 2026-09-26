// Package secrets encrypts integration credentials at rest and enforces the
// least-privilege scope separation the SPEC requires: read, sandbox, and
// production credentials are distinct and are never fetched by the wrong scope.
package secrets

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
)

// Scope is the trust boundary a credential belongs to. A read credential can
// never be used where a production credential is required, and vice versa.
type Scope string

const (
	ScopeRead       Scope = "read"
	ScopeSandbox    Scope = "sandbox"
	ScopeProduction Scope = "production"
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool {
	switch s {
	case ScopeRead, ScopeSandbox, ScopeProduction:
		return true
	default:
		return false
	}
}

// Kind is the integration a credential authenticates.
type Kind string

const (
	KindKubernetes Kind = "kubernetes"
	KindPrometheus Kind = "prometheus"
	KindGitHub     Kind = "github"
	KindOpenAI     Kind = "openai"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindKubernetes, KindPrometheus, KindGitHub, KindOpenAI:
		return true
	default:
		return false
	}
}

// Cipher performs authenticated encryption of credential plaintext.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte AES-256 key. The key may be provided
// as raw 32 bytes, hex (64 chars), or base64. An empty or wrong-length key is
// rejected so misconfiguration cannot silently weaken encryption.
func NewCipher(key string) (*Cipher, error) {
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func decodeKey(key string) ([]byte, error) {
	if key == "" {
		return nil, errors.New("secret key is not configured (set KNULL_SECRET_KEY)")
	}
	if len(key) == 32 {
		return []byte(key), nil
	}
	if b, err := hex.DecodeString(key); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("secret key must be 32 bytes (raw, hex, or base64)")
}

// Encrypt returns the ciphertext and the nonce used. A fresh random nonce is
// generated per call so identical plaintext yields distinct ciphertext.
func (c *Cipher) Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext = c.aead.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt reverses Encrypt. It fails if the ciphertext or nonce was tampered
// with, since GCM authenticates the data.
func (c *Cipher) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	if len(nonce) != c.aead.NonceSize() {
		return nil, errors.New("invalid nonce length")
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// Fingerprint returns a short, non-reversible identifier for a secret value so
// the UI can distinguish credentials without ever revealing them.
func Fingerprint(plaintext []byte) string {
	sum := sha256.Sum256(plaintext)
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}
