package secrets

import (
	"bytes"
	"strings"
	"testing"
)

func TestGeneratedPasswordPolicy(t *testing.T) {
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789"
	a, err := GeneratePassword(32, alphabet)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(a)
	b, err := GeneratePassword(32, alphabet)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(b)
	if len(a) != 32 || bytes.Equal(a, b) {
		t.Fatal("invalid generated password")
	}
	for _, c := range a {
		if !strings.ContainsRune(alphabet, rune(c)) {
			t.Fatal("character outside policy")
		}
	}
	for _, length := range []int{1, 15, 129} {
		if _, err := GeneratePassword(length, alphabet); err == nil {
			t.Fatal("accepted invalid length")
		}
	}
	if _, err := GeneratePassword(24, strings.Repeat("a", 32)); err == nil {
		t.Fatal("accepted duplicate alphabet")
	}
}
