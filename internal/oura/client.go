package oura

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// TokenProvider yields a valid bearer token per request. Implemented by the
// auth package (PAT and OAuth2); duplicated as a one-method interface here to
// keep oura free of an auth import.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

type Client struct {
	http       *http.Client
	baseURL    string
	tp         TokenProvider
	calls      int
	maxRetries int
	baseDelay  time.Duration
}

func New(tp TokenProvider, baseURL string) *Client {
	return &Client{
		http:       &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		tp:         tp,
		maxRetries: 4,
		baseDelay:  time.Second,
	}
}

func (c *Client) Calls() int { return c.calls }

type pageResponse struct {
	Data      []map[string]any `json:"data"`
	NextToken *string          `json:"next_token"`
}

// Fetch pulls every record for an endpoint over [start,end], following next_token.
func (c *Client) Fetch(ctx context.Context, endpoint, start, end string) ([]map[string]any, error) {
	var out []map[string]any
	next := ""
	for {
		page, err := c.fetchPage(ctx, endpoint, start, end, next)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Data...)
		if page.NextToken == nil || *page.NextToken == "" {
			return out, nil
		}
		next = *page.NextToken
	}
}

func (c *Client) fetchPage(ctx context.Context, endpoint, start, end, next string) (pageResponse, error) {
	u := fmt.Sprintf("%s/%s", c.baseURL, endpoint)
	q := url.Values{}
	if start != "" {
		q.Set("start_date", start)
	}
	if end != "" {
		q.Set("end_date", end)
	}
	if next != "" {
		q.Set("next_token", next)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return pageResponse{}, fmt.Errorf("build request %s: %w", endpoint, err)
	}
	tok, err := c.tp.Token(ctx)
	if err != nil {
		return pageResponse{}, fmt.Errorf("get token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.do(req)
	if err != nil {
		return pageResponse{}, err
	}
	defer resp.Body.Close()
	c.calls++

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return pageResponse{}, fmt.Errorf("oura %s returned %d: %s", endpoint, resp.StatusCode, string(body))
	}
	var page pageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return pageResponse{}, fmt.Errorf("decode %s page: %w", endpoint, err)
	}
	return page, nil
}

// do is a seam for retry; Task 11 replaces its body. For now it is a plain call.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}
