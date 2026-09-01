package secrets

import (
	"bytes"
	"testing"
)

func TestEnvelopeAccountBinding(t *testing.T) {
	env, err := New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := env.Seal("account-a", []byte("credential"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := env.Open("account-a", sealed)
	if err != nil || string(plain) != "credential" {
		t.Fatalf("open=%q err=%v", plain, err)
	}
	if _, err := env.Open("account-b", sealed); err == nil {
		t.Fatal("cross-account decrypt succeeded")
	}
}
