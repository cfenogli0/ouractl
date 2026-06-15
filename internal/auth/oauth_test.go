package auth

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type fakeRefresher struct {
	calls int
}

// errRefresher always fails, modeling a revoked or network-failed refresh.
type errRefresher struct{}

func (errRefresher) Refresh(context.Context, string) (Tokens, error) {
	return Tokens{}, fmt.Errorf("refresh rejected")
}

// A failed refresh must surface as an error, never serve a stale or empty token.
func TestOAuthRefreshFailureErrorsLoudly(t *testing.T) {
	p := &OAuthProvider{
		tokens:    Tokens{AccessToken: "stale", RefreshToken: "r0", Expiry: fixedNow().Add(-time.Minute)},
		refresher: errRefresher{},
		now:       fixedNow,
	}
	tok, err := p.Token(context.Background())
	if err == nil {
		t.Fatal("failed refresh must error, not serve a token")
	}
	if tok != "" {
		t.Fatalf("on refresh failure token must be empty, got %q", tok)
	}
}

func (f *fakeRefresher) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	f.calls++
	return Tokens{
		AccessToken:  "fresh-access",
		RefreshToken: "next-refresh",
		Expiry:       fixedNow().Add(time.Hour),
	}, nil
}

func fixedNow() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) }

func TestOAuthRefreshesExpiredToken(t *testing.T) {
	ref := &fakeRefresher{}
	p := &OAuthProvider{
		tokens:    Tokens{AccessToken: "stale", RefreshToken: "r0", Expiry: fixedNow().Add(-time.Minute)},
		refresher: ref,
		now:       fixedNow,
	}
	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "fresh-access" {
		t.Fatalf("token = %q, want fresh-access", tok)
	}
	if ref.calls != 1 {
		t.Fatalf("refresh called %d times, want 1", ref.calls)
	}
	if _, err := p.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ref.calls != 1 {
		t.Fatalf("refresh called %d times on valid token, want 1", ref.calls)
	}
}

func TestOAuthValidTokenSkipsRefresh(t *testing.T) {
	ref := &fakeRefresher{}
	p := &OAuthProvider{
		tokens:    Tokens{AccessToken: "good", RefreshToken: "r0", Expiry: fixedNow().Add(time.Hour)},
		refresher: ref,
		now:       fixedNow,
	}
	tok, _ := p.Token(context.Background())
	if tok != "good" || ref.calls != 0 {
		t.Fatalf("valid token should be served as-is without refresh; tok=%q calls=%d", tok, ref.calls)
	}
}
