package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// This file holds simple, dependency-free helpers for one-off API calls.
//
// Use these when you just want a response quickly. Use GetJSON[T] in client.go
// instead when you want retries, a shared base URL, or headers applied to every
// request to the same API.

// Fetch performs a GET request and returns the raw response body as a string.
//
//	body, err := apiclient.Fetch(ctx, "https://api.github.com/repos/gorcon/rcon", nil)
func Fetch(ctx context.Context, url string, headers map[string]string) (string, error) {
	body, err := do(ctx, http.MethodGet, url, headers, nil)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// FetchJSON performs a GET request and decodes the JSON response into out,
// which must be a pointer.
//
//	var repo struct {
//	    FullName string `json:"full_name"`
//	    Stars    int    `json:"stargazers_count"`
//	}
//	err := apiclient.FetchJSON(ctx, "https://api.github.com/repos/gorcon/rcon", nil, &repo)
//
// Pass a map[string]any as out when you do not want to declare a struct.
func FetchJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	body, err := do(ctx, http.MethodGet, url, headers, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json from %s: %w", url, err)
	}
	return nil
}

// PostJSON sends payload as a JSON body and decodes the JSON response into out.
// Pass nil for out to ignore the response body.
//
//	err := apiclient.PostJSON(ctx, url, nil, map[string]string{"name": "test"}, &result)
func PostJSON(ctx context.Context, url string, headers map[string]string, payload, out any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}

	if headers == nil {
		headers = map[string]string{}
	}
	if _, set := headers["Content-Type"]; !set {
		headers["Content-Type"] = "application/json"
	}

	body, err := do(ctx, http.MethodPost, url, headers, encoded)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json from %s: %w", url, err)
	}
	return nil
}

// do performs the request and returns the body bytes on a 2xx response.
func do(ctx context.Context, method, url string, headers map[string]string, payload []byte) ([]byte, error) {
	// Guarantee a deadline even when the caller passes context.Background().
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	// Cap the read so a misbehaving upstream cannot exhaust memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			URL:        url,
			Body:       truncate(string(body), 300),
		}
	}

	return body, nil
}