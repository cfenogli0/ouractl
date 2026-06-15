package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestNewPKCEChallengeMatchesVerifier(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	if len(p.Verifier) < 43 { // 32 bytes base64url = 43 chars
		t.Fatalf("verifier too short: %d", len(p.Verifier))
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Fatalf("challenge = %q, want S256(verifier) = %q", p.Challenge, want)
	}
	// Two calls must differ (random verifier).
	p2, _ := NewPKCE()
	if p2.Verifier == p.Verifier {
		t.Fatal("two PKCE verifiers identical; not random")
	}
}
