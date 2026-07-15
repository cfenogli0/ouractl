package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// DeriveKey turns any-length user key material into a 32-byte AES-256 key.
// SHA-256 only: retained to read pre-argon2 token files (see FileStore.Load);
// new files derive with DeriveKeyArgon2.
func DeriveKey(material string) []byte {
	sum := sha256.Sum256([]byte(material))
	return sum[:]
}

// Argon2id parameters written into new token files (RFC 9106 second
// recommended option, time raised to 3: sync is not latency-sensitive and the
// file guards a long-lived refresh token).
const (
	argonTime      = 3
	argonMemoryKiB = 64 * 1024
	argonThreads   = 4
	argonSaltLen   = 16
)

// DeriveKeyArgon2 stretches user key material into a 32-byte AES-256 key.
// Memory-hard, so a low-entropy passphrase resists offline brute force in a
// way DeriveKey's bare SHA-256 does not. Parameters ride with the ciphertext
// so they can be raised later without breaking existing files.
func DeriveKeyArgon2(material string, salt []byte, time, memoryKiB uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(material), salt, time, memoryKiB, threads, 32)
}

// Encrypt seals plaintext with AES-256-GCM; nonce is prepended to the output.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. A wrong key or tampered ciphertext returns an error.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, fmt.Errorf("ciphertext shorter than nonce")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt (wrong key or corrupt data): %w", err)
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	return cipher.NewGCM(block)
}
