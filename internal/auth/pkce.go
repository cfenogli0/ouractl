package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCE holds the verifier kept by the client and the challenge sent to Oura.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a fresh S256 verifier/challenge pair.
func NewPKCE() (PKCE, error) {
	v, err := randString(32)
	if err != nil {
		return PKCE{}, err
	}
	sum := sha256.Sum256([]byte(v))
	return PKCE{Verifier: v, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// randString returns n random bytes base64url-encoded (no padding). Used for the
// PKCE verifier and the OAuth state parameter.
func randString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
