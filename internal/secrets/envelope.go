package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

type Envelope struct{ aead cipher.AEAD }

func New(key []byte) (*Envelope, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("controller encryption key must contain at least 32 bytes")
	}
	digest := sha256.Sum256(key)
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Envelope{aead: aead}, nil
}
func (e *Envelope) Seal(accountID string, plaintext []byte) (string, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := e.aead.Seal(nil, nonce, plaintext, []byte(accountID))
	return base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}
func (e *Envelope) Open(accountID, encoded string) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(data) < e.aead.NonceSize() {
		return nil, fmt.Errorf("encrypted envelope is truncated")
	}
	return e.aead.Open(nil, data[:e.aead.NonceSize()], data[e.aead.NonceSize():], []byte(accountID))
}
func TokenHash(token string) []byte { value := sha256.Sum256([]byte(token)); return value[:] }
