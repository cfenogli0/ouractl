package auth

import "context"

// TokenProvider yields a valid bearer token per request.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// StaticProvider serves a personal access token. The PAT is never persisted by
// the tool; it already lives in env or the config file.
type StaticProvider struct{ tok string }

func NewStaticProvider(token string) StaticProvider { return StaticProvider{tok: token} }

func (p StaticProvider) Token(context.Context) (string, error) { return p.tok, nil }
