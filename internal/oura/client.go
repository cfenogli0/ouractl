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

// GetOne fetches a single-object endpoint (e.g. personal_info) and returns the
// decoded body. Used by `auth` to validate a PAT.
func (c *Client) GetOne(ctx context.Context, endpoint string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}
	tok, err := c.tp.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c.calls++
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oura %s returned %d: %s", endpoint, resp.StatusCode, string(body))
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return m, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("oura transient status %d", resp.StatusCode)
		} else {
			return resp, nil
		}
		if attempt < c.maxRetries {
			time.Sleep(c.baseDelay * time.Duration(1<<attempt)) // exponential backoff
		}
	}
	return nil, fmt.Errorf("exhausted %d retries: %w", c.maxRetries, lastErr)
}
