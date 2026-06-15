package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTokenURL is Oura's OAuth token endpoint.
const DefaultTokenURL = "https://api.ouraring.com/oauth/token"

// OuraRefresher implements Refresher against Oura's token endpoint.
type OuraRefresher struct {
	tokenURL     string
	clientID     string
	clientSecret string
	http         *http.Client
	now          func() time.Time
}

func NewOuraRefresher(tokenURL, clientID, clientSecret string) *OuraRefresher {
	return &OuraRefresher{
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 30 * time.Second},
		now:          time.Now,
	}
}

func (r *OuraRefresher) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {r.clientID},
		"client_secret": {r.clientSecret},
	}
	tok, err := postTokenForm(ctx, r.http, r.tokenURL, form, r.now)
	if err != nil {
		return Tokens{}, err
	}
	// Oura rotates the refresh token but may omit it; keep the old one.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// tokenResponse is Oura's token-endpoint JSON.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// postTokenForm POSTs a form to the token endpoint and maps the response to
// Tokens, computing Expiry from expires_in. Shared by refresh and code exchange.
func postTokenForm(ctx context.Context, hc *http.Client, tokenURL string, form url.Values, now func() time.Time) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return Tokens{}, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Tokens{}, fmt.Errorf("decode token response: %w", err)
	}
	return Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expiry:       now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}
