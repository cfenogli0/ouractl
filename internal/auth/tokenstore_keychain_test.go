package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestKeychainStoreRoundTrip(t *testing.T) {
	keyring.MockInit() // in-memory keyring; no real OS keychain touched

	s := NewKeychainStore("octl-test")
	if _, err := s.Load(); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("empty keychain err = %v, want ErrNoTokens", err)
	}

	want := Tokens{AccessToken: "a", RefreshToken: "r", Expiry: time.Unix(2000, 0).UTC()}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
