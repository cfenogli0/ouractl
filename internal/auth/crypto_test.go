package auth

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("a-passphrase-of-any-length")
	plain := []byte(`{"refresh_token":"secret"}`)

	ct, err := Encrypt(key, plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip = %s, want %s", got, plain)
	}
}

func TestDecryptWrongKeyErrors(t *testing.T) {
	ct, _ := Encrypt(DeriveKey("right"), []byte("data"))
	if _, err := Decrypt(DeriveKey("wrong"), ct); err == nil {
		t.Fatal("decrypt with wrong key must error, not return garbage")
	}
}
