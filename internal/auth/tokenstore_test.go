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
	s := NewFileStore(path, "unit-test-key")

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
	s := NewFileStore(filepath.Join(t.TempDir(), "absent.enc"), "k")
	if _, err := s.Load(); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("missing file err = %v, want ErrNoTokens", err)
	}
}

func TestFileStoreWritesArgon2Format(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.enc")
	s := NewFileStore(path, "passphrase")
	if err := s.Save(Tokens{AccessToken: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if len(raw) < len(fileMagicV2) || string(raw[:len(fileMagicV2)]) != fileMagicV2 {
		t.Fatalf("saved file lacks v2 magic, starts with %q", raw[:8])
	}
}

func TestFileStoreWrongPassphraseFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.enc")
	if err := NewFileStore(path, "right").Save(Tokens{AccessToken: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := NewFileStore(path, "wrong").Load(); err == nil {
		t.Fatal("wrong passphrase must fail to load")
	}
}

func TestFileStoreReadsLegacySHA256AndUpgrades(t *testing.T) {
	// A pre-argon2 token file: AES-GCM under a bare SHA-256 key, no header.
	path := filepath.Join(t.TempDir(), "tokens.enc")
	legacy, err := Encrypt(DeriveKey("passphrase"), []byte(`{"access_token":"old","refresh_token":"r"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewFileStore(path, "passphrase")
	got, err := s.Load()
	if err != nil {
		t.Fatalf("legacy Load: %v", err)
	}
	if got.AccessToken != "old" {
		t.Fatalf("legacy tokens = %+v", got)
	}

	// The next Save must upgrade the file to the argon2 format.
	if err := s.Save(got); err != nil {
		t.Fatalf("upgrade Save: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw[:len(fileMagicV2)]) != fileMagicV2 {
		t.Fatal("Save did not upgrade legacy file to v2 format")
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("Load after upgrade: %v", err)
	}
}

func TestFileStoreSaltVariesPerSave(t *testing.T) {
	// Fresh salt each Save: identical plaintext must not produce identical files.
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "a.enc"), filepath.Join(dir, "b.enc")
	tok := Tokens{AccessToken: "same"}
	if err := NewFileStore(p1, "k").Save(tok); err != nil {
		t.Fatal(err)
	}
	if err := NewFileStore(p2, "k").Save(tok); err != nil {
		t.Fatal(err)
	}
	r1, _ := os.ReadFile(p1)
	r2, _ := os.ReadFile(p2)
	if string(r1) == string(r2) {
		t.Fatal("two saves produced identical ciphertext (salt or nonce reuse)")
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
