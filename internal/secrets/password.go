package secrets

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

type PasswordPolicy struct {
	Length   int    `json:"length,omitempty"`
	Alphabet string `json:"alphabet,omitempty"`
}

func (p PasswordPolicy) Validate() error {
	_, _, err := passwordPolicy(p.Length, p.Alphabet)
	return err
}

func passwordPolicy(length int, alphabet string) (int, string, error) {
	if length == 0 {
		length = 24
	}
	if alphabet == "" {
		alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&*-_"
	}
	if length < 16 || length > 128 || len(alphabet) < 32 || len(alphabet) > 94 {
		return 0, "", fmt.Errorf("password policy requires length 16–128 and 32–94 unique printable characters")
	}
	seen := map[byte]bool{}
	for i := range alphabet {
		b := alphabet[i]
		if b < 33 || b > 126 || seen[b] {
			return 0, "", fmt.Errorf("invalid password alphabet")
		}
		seen[b] = true
	}
	return length, alphabet, nil
}

// GeneratePassword samples uniformly from a validated ASCII alphabet.
func GeneratePassword(length int, alphabet string) ([]byte, error) {
	length, alphabet, err := passwordPolicy(length, alphabet)
	if err != nil {
		return nil, err
	}
	password := make([]byte, length)
	for i := range password {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			clear(password)
			return nil, fmt.Errorf("password generation unavailable")
		}
		password[i] = alphabet[n.Int64()]
	}
	return password, nil
}
