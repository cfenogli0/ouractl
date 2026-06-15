package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeURLContainsPKCEAndState(t *testing.T) {
	u := AuthorizeURL("https://cloud.ouraring.com/oauth/authorize",
		"cid", "http://localhost:8182/callback", "daily personal", "STATE123", "CHAL")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := parsed.Query()
	checks := map[string]string{
		"response_type": "code", "client_id": "cid",
		"redirect_uri": "http://localhost:8182/callback", "scope": "daily personal",
		"state": "STATE123", "code_challenge": "CHAL", "code_challenge_method": "S256",
	}
	for k, want := range checks {
		if q.Get(k) != want {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestCallbackHandlerExtractsCodeAndChecksState(t *testing.T) {
	ch := make(chan callbackResult, 1)
	h := callbackHandler("S1", ch)

	// happy path
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/callback?code=ABC&state=S1", nil))
	got := <-ch
	if got.err != nil || got.code != "ABC" {
		t.Fatalf("happy path: code=%q err=%v", got.code, got.err)
	}

	// state mismatch must error, not yield a code
	ch2 := make(chan callbackResult, 1)
	h2 := callbackHandler("S1", ch2)
	h2(httptest.NewRecorder(), httptest.NewRequest("GET", "/callback?code=ABC&state=EVIL", nil))
	if r := <-ch2; r.err == nil {
		t.Fatal("state mismatch must error")
	}
}

func TestExchangeCodePostsAuthorizationCode(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"access_token":"A","refresh_token":"R","expires_in":3600}`))
	}))
	defer srv.Close()

	fixed := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	tok, err := ExchangeCode(context.Background(), srv.URL, "cid", "sec",
		"http://localhost:8182/callback", "CODE", "VERIFIER", func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "A" || tok.RefreshToken != "R" {
		t.Fatalf("tokens = %+v", tok)
	}
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "CODE" ||
		form.Get("code_verifier") != "VERIFIER" || !strings.Contains(form.Get("redirect_uri"), "callback") ||
		form.Get("client_id") != "cid" || form.Get("client_secret") != "sec" {
		t.Fatalf("form = %v", form)
	}
}
