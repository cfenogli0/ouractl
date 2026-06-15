package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

// DefaultAuthorizeURL is Oura's OAuth authorization endpoint.
const DefaultAuthorizeURL = "https://cloud.ouraring.com/oauth/authorize"

// DefaultRedirectURI is the localhost callback the one-shot server listens on.
const DefaultRedirectURI = "http://localhost:8182/callback"

// FlowConfig parameterizes the interactive authorization-code flow.
type FlowConfig struct {
	AuthorizeURL string // default DefaultAuthorizeURL if empty
	TokenURL     string // default DefaultTokenURL if empty
	ClientID     string
	ClientSecret string
	RedirectURI  string // default DefaultRedirectURI if empty
	Scope        string
}

// AuthorizeURL builds the authorization request URL.
func AuthorizeURL(base, clientID, redirectURI, scope, state, challenge string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return base + "?" + q.Encode()
}

// ExchangeCode swaps an authorization code for tokens.
func ExchangeCode(ctx context.Context, tokenURL, clientID, clientSecret, redirectURI, code, verifier string, now func() time.Time) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code_verifier": {verifier},
	}
	return postTokenForm(ctx, &http.Client{Timeout: 30 * time.Second}, tokenURL, form, now)
}

type callbackResult struct {
	code string
	err  error
}

// callbackHandler returns an http handler that validates state, extracts the
// code, and reports the result on ch exactly once.
func callbackHandler(expectedState string, ch chan<- callbackResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			ch <- callbackResult{err: fmt.Errorf("authorization denied: %s", e)}
			http.Error(w, "authorization failed", http.StatusBadRequest)
			return
		}
		if q.Get("state") != expectedState {
			ch <- callbackResult{err: fmt.Errorf("state mismatch (possible CSRF)")}
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			ch <- callbackResult{err: fmt.Errorf("no code in callback")}
			http.Error(w, "no code", http.StatusBadRequest)
			return
		}
		ch <- callbackResult{code: code}
		fmt.Fprintln(w, "octl: authorized. You can close this tab.")
	}
}

// openURL opens a URL in the user's browser. Overridable in tests.
var openURL = func(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}

// RunBrowserFlow runs the full PKCE authorization-code flow and returns tokens.
// It opens the browser, serves one callback on the redirect URI, and exchanges
// the code. Not unit-tested (browser + listener); its pieces are tested above.
func RunBrowserFlow(ctx context.Context, cfg FlowConfig) (Tokens, error) {
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = DefaultAuthorizeURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = DefaultTokenURL
	}
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = DefaultRedirectURI
	}
	pkce, err := NewPKCE()
	if err != nil {
		return Tokens{}, err
	}
	state, err := randString(16)
	if err != nil {
		return Tokens{}, err
	}
	redirect, err := url.Parse(cfg.RedirectURI)
	if err != nil {
		return Tokens{}, fmt.Errorf("parse redirect_uri: %w", err)
	}

	ch := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, callbackHandler(state, ch))
	ln, err := net.Listen("tcp", redirect.Host)
	if err != nil {
		return Tokens{}, fmt.Errorf("listen on %s (is the port free?): %w", redirect.Host, err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	authURL := AuthorizeURL(cfg.AuthorizeURL, cfg.ClientID, cfg.RedirectURI, cfg.Scope, state, pkce.Challenge)
	if err := openURL(authURL); err != nil {
		fmt.Printf("Open this URL to authorize:\n%s\n", authURL)
	}

	select {
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	case <-time.After(2 * time.Minute):
		return Tokens{}, fmt.Errorf("timed out waiting for authorization callback")
	case res := <-ch:
		if res.err != nil {
			return Tokens{}, res.err
		}
		return ExchangeCode(ctx, cfg.TokenURL, cfg.ClientID, cfg.ClientSecret, cfg.RedirectURI, res.code, pkce.Verifier, time.Now)
	}
}
