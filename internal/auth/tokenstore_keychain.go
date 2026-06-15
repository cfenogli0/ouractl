package auth

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keychainUser is the fixed account name under the keychain service entry.
const keychainUser = "oauth"

// KeychainStore keeps tokens in the OS keychain (macOS Keychain, Linux Secret
// Service, Windows Credential Manager) via zalando/go-keyring. No token file
// touches disk.
type KeychainStore struct {
	service string
}

func NewKeychainStore(service string) *KeychainStore { return &KeychainStore{service: service} }

func (s *KeychainStore) Load() (Tokens, error) {
	v, err := keyring.Get(s.service, keychainUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return Tokens{}, ErrNoTokens
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("read keychain (%s): %w", s.service, err)
	}
	var t Tokens
	if err := json.Unmarshal([]byte(v), &t); err != nil {
		return Tokens{}, fmt.Errorf("decode keychain tokens: %w", err)
	}
	return t, nil
}

func (s *KeychainStore) Save(t Tokens) error {
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	if err := keyring.Set(s.service, keychainUser, string(b)); err != nil {
		return fmt.Errorf("write keychain (%s): %w", s.service, err)
	}
	return nil
}
