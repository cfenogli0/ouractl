package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreRoundTripEncrypted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.enc")
	key := DeriveKey("unit-test-key")
	s := NewFileStore(path, key)

	want := Tokens{AccessToken: "a", RefreshToken: "r", Expiry: time.Unix(1000, 0).UTC()}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// On-disk bytes must be ciphertext, not the plaintext token.
	raw, _ := os.ReadFile(path)
	if len(raw) == 0 || string(raw) == "a" || contains(raw, "refresh") {
		t.Fatalf("file is not encrypted: %q", raw)
	}
	// File must be owner-only.
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perms = %v, want 0600", fi.Mode().Perm())
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestFileStoreLoadMissingIsErrNoTokens(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "absent.enc"), DeriveKey("k"))
	if _, err := s.Load(); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("missing file err = %v, want ErrNoTokens", err)
	}
}

func TestNewTokenStoreSelection(t *testing.T) {
	// file backend needs an encryption key
	if _, err := NewTokenStore("file", "/tmp/x.enc", ""); err == nil {
		t.Fatal("file backend without encryption key must error")
	}
	s, err := NewTokenStore("file", "/tmp/x.enc", "k")
	if err != nil {
		t.Fatalf("file backend: %v", err)
	}
	if _, ok := s.(*FileStore); !ok {
		t.Fatalf("want *FileStore, got %T", s)
	}
	// empty backend defaults to file
	if _, err := NewTokenStore("", "/tmp/x.enc", "k"); err != nil {
		t.Fatalf("empty backend should default to file: %v", err)
	}
	s, err = NewTokenStore("keychain", "", "")
	if err != nil {
		t.Fatalf("keychain backend: %v", err)
	}
	if _, ok := s.(*KeychainStore); !ok {
		t.Fatalf("want *KeychainStore, got %T", s)
	}
	if _, err := NewTokenStore("bogus", "", ""); err == nil {
		t.Fatal("unknown backend must error")
	}
}

func contains(b []byte, sub string) bool {
	return len(b) >= len(sub) && (string(b) == sub || indexOf(string(b), sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
