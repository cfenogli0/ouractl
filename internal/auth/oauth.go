package auth

import (
	"context"
	"time"
)

// Tokens is the OAuth credential set persisted (encrypted) between runs.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// Refresher exchanges a refresh token for a fresh Tokens set. The real
// implementation calls Oura's token endpoint; tests substitute a fake.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
}

// OAuthProvider serves access tokens, refreshing when within the expiry skew.
type OAuthProvider struct {
	tokens    Tokens
	refresher Refresher
	now       func() time.Time
	persist   func(Tokens) error // optional: write refreshed tokens to disk
}

const expirySkew = 5 * time.Minute

// NewOAuthProvider loads tokens from the store and persists refreshed tokens
// back to it. Returns ErrNoTokens (wrapped) if nothing is stored yet.
func NewOAuthProvider(store TokenStore, refresher Refresher) (*OAuthProvider, error) {
	t, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &OAuthProvider{
		tokens:    t,
		refresher: refresher,
		now:       time.Now,
		persist:   store.Save,
	}, nil
}

func (p *OAuthProvider) Token(ctx context.Context) (string, error) {
	now := time.Now
	if p.now != nil {
		now = p.now
	}
	if now().Add(expirySkew).Before(p.tokens.Expiry) {
		return p.tokens.AccessToken, nil
	}
	fresh, err := p.refresher.Refresh(ctx, p.tokens.RefreshToken)
	if err != nil {
		return "", err
	}
	p.tokens = fresh
	if p.persist != nil {
		if err := p.persist(fresh); err != nil {
			return "", err
		}
	}
	return p.tokens.AccessToken, nil
}
