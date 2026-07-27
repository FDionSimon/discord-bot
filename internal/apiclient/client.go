// Package apiclient wraps net/http with the things every outbound API call in
// this bot needs: a bounded timeout, JSON decoding, retry-on-transient-failure,
// and errors that carry enough detail to show a useful message in Discord.
package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a thin, reusable HTTP client bound to one upstream API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	headers    map[string]string
	maxRetries int
}

// Option configures a Client.
type Option func(*Client)

// WithHeader sets a header sent on every request (auth tokens, User-Agent...).
func WithHeader(key, value string) Option {
	return func(c *Client) {
		c.headers[key] = value
	}
}

// WithMaxRetries overrides the default retry count for transient failures.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// New builds a Client for the given base URL.
func New(baseURL string, timeout time.Duration, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
		headers:    map[string]string{"Accept": "application/json"},
		maxRetries: 2,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError describes a non-2xx response from an upstream API.
type APIError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api request to %s failed with status %d: %s", e.URL, e.StatusCode, e.Body)
}

// NotFound reports whether the upstream returned 404, which usually deserves a
// friendlier Discord message than a generic failure.
func (e *APIError) NotFound() bool { return e.StatusCode == http.StatusNotFound }

// RateLimited reports whether the upstream rejected us for sending too much.
func (e *APIError) RateLimited() bool { return e.StatusCode == http.StatusTooManyRequests }

// GetJSON performs a GET request and decodes the JSON body into T.
//
// It is a package-level function rather than a method because Go does not allow
// type parameters on methods.
func GetJSON[T any](ctx context.Context, c *Client, path string, query url.Values) (T, error) {
	var zero T

	endpoint := c.baseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 250ms, 500ms, 1s...
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(backoff):
			}
		}

		result, err := doGet[T](ctx, c, endpoint)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Never retry a request the caller cancelled, or an error the upstream
		// will answer identically next time (4xx other than 429).
		if ctx.Err() != nil || !retryable(err) {
			return zero, err
		}
	}

	return zero, fmt.Errorf("all %d attempts failed: %w", c.maxRetries+1, lastErr)
}

func doGet[T any](ctx context.Context, c *Client, endpoint string) (T, error) {
	var zero T

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return zero, fmt.Errorf("build request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	// Cap the read so a misbehaving upstream cannot exhaust memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return zero, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, &APIError{
			StatusCode: resp.StatusCode,
			URL:        endpoint,
			Body:       truncate(string(body), 300),
		}
	}

	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		return zero, fmt.Errorf("decode json: %w", err)
	}
	return out, nil
}

func retryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500 || apiErr.RateLimited()
	}
	// Network-level failures (timeouts, connection resets) are worth retrying.
	return true
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}