package oura

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type staticToken struct{ t string }

func (s staticToken) Token(context.Context) (string, error) { return s.t, nil }

func TestFetchFollowsPagination(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Query().Get("next_token") == "" {
			w.Write([]byte(`{"data":[{"id":"a","day":"2026-06-10"}],"next_token":"PAGE2"}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"b","day":"2026-06-11"}],"next_token":null}`))
	}))
	defer srv.Close()

	c := New(staticToken{"tok-123"}, srv.URL)
	recs, err := c.Fetch(context.Background(), "daily_sleep", "2026-06-10", "2026-06-11")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records across pages, want 2", len(recs))
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("auth header = %q, want Bearer tok-123", gotAuth)
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":[{"id":"a","day":"2026-06-10"}],"next_token":null}`))
	}))
	defer srv.Close()

	c := New(staticToken{"t"}, srv.URL)
	c.maxRetries = 2
	c.baseDelay = 0 // no real sleeping in tests

	recs, err := c.Fetch(context.Background(), "daily_sleep", "2026-06-10", "2026-06-10")
	if err != nil {
		t.Fatalf("Fetch after 429: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("server hit %d times, want 2 (one 429, one success)", hits)
	}
}
