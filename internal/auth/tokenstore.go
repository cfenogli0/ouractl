package auth

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoTokens signals that no OAuth tokens are stored yet (run `octl auth`).
var ErrNoTokens = errors.New("no oauth tokens stored")

// TokenStore persists OAuth tokens between runs, outside config.toml.
type TokenStore interface {
	Load() (Tokens, error) // returns ErrNoTokens when none stored
	Save(Tokens) error
}

// fileMagicV2 marks the argon2id token file layout:
// magic | time uint32 BE | memoryKiB uint32 BE | threads uint8 | salt [argonSaltLen] | AES-GCM blob.
// Files without the magic are the pre-argon2 layout (bare SHA-256 key, GCM blob
// only); Load still reads them and the next Save rewrites in v2.
const fileMagicV2 = "OCTLTOK2"

// FileStore keeps tokens in an AES-256-GCM encrypted file at 0600, key
// stretched from the user's key material with argon2id.
type FileStore struct {
	path     string
	material string
}

func NewFileStore(path, material string) *FileStore {
	return &FileStore{path: path, material: material}
}

func (s *FileStore) Load() (Tokens, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Tokens{}, ErrNoTokens
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("read token file: %w", err)
	}
	var plain []byte
	if strings.HasPrefix(string(raw), fileMagicV2) {
		plain, err = s.decryptV2(raw[len(fileMagicV2):])
	} else {
		plain, err = Decrypt(DeriveKey(s.material), raw) // legacy layout
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("decrypt token file (wrong OURA_ENCRYPTION_KEY?): %w", err)
	}
	var t Tokens
	if err := json.Unmarshal(plain, &t); err != nil {
		return Tokens{}, fmt.Errorf("decode tokens: %w", err)
	}
	return t, nil
}

func (s *FileStore) decryptV2(rest []byte) ([]byte, error) {
	const header = 4 + 4 + 1 + argonSaltLen
	if len(rest) < header {
		return nil, fmt.Errorf("token file truncated")
	}
	time := binary.BigEndian.Uint32(rest[0:4])
	memoryKiB := binary.BigEndian.Uint32(rest[4:8])
	threads := rest[8]
	salt := rest[9 : 9+argonSaltLen]
	key := DeriveKeyArgon2(s.material, salt, time, memoryKiB, threads)
	return Decrypt(key, rest[header:])
}

func (s *FileStore) Save(t Tokens) error {
	plain, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	key := DeriveKeyArgon2(s.material, salt, argonTime, argonMemoryKiB, argonThreads)
	blob, err := Encrypt(key, plain)
	if err != nil {
		return err
	}
	ct := make([]byte, 0, len(fileMagicV2)+9+len(salt)+len(blob))
	ct = append(ct, fileMagicV2...)
	ct = binary.BigEndian.AppendUint32(ct, argonTime)
	ct = binary.BigEndian.AppendUint32(ct, argonMemoryKiB)
	ct = append(ct, argonThreads)
	ct = append(ct, salt...)
	ct = append(ct, blob...)
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

// NewTokenStore builds the configured backend. The file backend requires an
// encryption key (encrypting with a key that sits beside the ciphertext is
// pointless, so the key must come from env / a secrets manager).
func NewTokenStore(backend, filePath, encKey string) (TokenStore, error) {
	switch backend {
	case "", "file":
		if encKey == "" {
			return nil, fmt.Errorf("file token backend requires an encryption key (set OURA_ENCRYPTION_KEY) or use --token-backend keychain")
		}
		return NewFileStore(filePath, encKey), nil
	case "keychain":
		return NewKeychainStore("octl"), nil
	default:
		return nil, fmt.Errorf("unknown token_backend %q (want file or keychain)", backend)
	}
}

