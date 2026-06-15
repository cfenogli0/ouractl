package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestOuraRefresherExchangesAndComputesExpiry(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"newA","refresh_token":"newR","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	fixed := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	ref := NewOuraRefresher(srv.URL, "cid", "csecret")
	ref.now = func() time.Time { return fixed }

	tok, err := ref.Refresh(context.Background(), "oldR")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "newA" || tok.RefreshToken != "newR" {
		t.Fatalf("tokens = %+v", tok)
	}
	if !tok.Expiry.Equal(fixed.Add(3600 * time.Second)) {
		t.Fatalf("expiry = %v, want now+3600s", tok.Expiry)
	}
	if gotForm.Get("grant_type") != "refresh_token" || gotForm.Get("refresh_token") != "oldR" ||
		gotForm.Get("client_id") != "cid" || gotForm.Get("client_secret") != "csecret" {
		t.Fatalf("form = %v", gotForm)
	}
}

// Oura rotates refresh tokens but may omit it; keep the old one so the next refresh works.
func TestOuraRefresherKeepsOldRefreshTokenWhenOmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"newA","expires_in":3600}`))
	}))
	defer srv.Close()

	ref := NewOuraRefresher(srv.URL, "cid", "csecret")
	tok, err := ref.Refresh(context.Background(), "keepme")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.RefreshToken != "keepme" {
		t.Fatalf("refresh token = %q, want keepme (fallback)", tok.RefreshToken)
	}
}

func TestOuraRefresherErrorsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	ref := NewOuraRefresher(srv.URL, "cid", "csecret")
	if _, err := ref.Refresh(context.Background(), "r"); err == nil {
		t.Fatal("non-200 refresh must error loudly")
	}
}
