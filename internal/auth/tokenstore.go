package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNoTokens signals that no OAuth tokens are stored yet (run `octl auth`).
var ErrNoTokens = errors.New("no oauth tokens stored")

// TokenStore persists OAuth tokens between runs, outside config.toml.
type TokenStore interface {
	Load() (Tokens, error) // returns ErrNoTokens when none stored
	Save(Tokens) error
}

// FileStore keeps tokens in an AES-256-GCM encrypted file at 0600.
type FileStore struct {
	path string
	key  []byte
}

func NewFileStore(path string, key []byte) *FileStore { return &FileStore{path: path, key: key} }

func (s *FileStore) Load() (Tokens, error) {
	ct, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Tokens{}, ErrNoTokens
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("read token file: %w", err)
	}
	plain, err := Decrypt(s.key, ct)
	if err != nil {
		return Tokens{}, fmt.Errorf("decrypt token file (wrong OURA_ENCRYPTION_KEY?): %w", err)
	}
	var t Tokens
	if err := json.Unmarshal(plain, &t); err != nil {
		return Tokens{}, fmt.Errorf("decode tokens: %w", err)
	}
	return t, nil
}

func (s *FileStore) Save(t Tokens) error {
	plain, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	ct, err := Encrypt(s.key, plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	// Write to a temp file then rename for atomicity; chmod 0600 before rename.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, ct, 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("commit token file: %w", err)
	}
	return nil
}

