// Package jenkins implements the executor.Executor contract against Jenkins.
//
// It triggers a job with buildWithParameters, correlates the queue item with the
// resulting build and reports build status. POSTs carry a CSRF crumb that is
// cached and refreshed on rejection; authentication is a service user with an
// API token.
package jenkins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const requestTimeout = 30 * time.Second

// client is the low-level HTTP layer: basic auth, crumb handling and JSON reads.
type client struct {
	baseURL string
	user    string
	token   string
	http    *http.Client

	mu          sync.Mutex
	crumbField  string
	crumbValue  string
	crumbLoaded bool
}

func newClient(baseURL, user, token string) *client {
	return &client{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		token:   token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// get issues an authenticated GET and returns the status code and body.
func (c *client) get(ctx context.Context, rawURL string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.user, c.token)
	req.Header.Set("Accept", "application/json")

	status, _, body, err := c.do(req)

	return status, body, err
}

// postForm issues an authenticated form POST with a CSRF crumb. A 403 (possibly a
// stale crumb) triggers one crumb refresh and retry. It returns the status code
// and the Location header.
func (c *client) postForm(ctx context.Context, rawURL string, form url.Values) (int, string, error) {
	status, location, err := c.postOnce(ctx, rawURL, form)
	if err != nil {
		return 0, "", err
	}
	if status == http.StatusForbidden {
		c.invalidateCrumb()

		return c.postOnce(ctx, rawURL, form)
	}

	return status, location, nil
}

func (c *client) postOnce(ctx context.Context, rawURL string, form url.Values) (int, string, error) {
	field, value, err := c.crumb(ctx)
	if err != nil {
		return 0, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, "", fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.user, c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if field != "" {
		req.Header.Set(field, value)
	}

	status, header, _, err := c.do(req)
	if err != nil {
		return 0, "", err
	}

	return status, header.Get("Location"), nil
}

// crumb returns the cached CSRF crumb, fetching it on first use. A disabled crumb
// issuer (404) is cached as an empty crumb so POSTs proceed without one.
func (c *client) crumb(ctx context.Context) (field, value string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.crumbLoaded {
		return c.crumbField, c.crumbValue, nil
	}

	status, body, err := c.get(ctx, c.baseURL+"/crumbIssuer/api/json")
	if err != nil {
		return "", "", err
	}

	switch status {
	case http.StatusOK:
		var parsed struct {
			Field string `json:"crumbRequestField"`
			Crumb string `json:"crumb"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return "", "", fmt.Errorf("decode crumb: %w", err)
		}
		c.crumbField, c.crumbValue = parsed.Field, parsed.Crumb
	case http.StatusNotFound:
		c.crumbField, c.crumbValue = "", ""
	default:
		return "", "", fmt.Errorf("jenkins crumb: unexpected status %d", status)
	}

	c.crumbLoaded = true

	return c.crumbField, c.crumbValue, nil
}

func (c *client) invalidateCrumb() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.crumbLoaded = false
}

func (c *client) do(req *http.Request) (int, http.Header, []byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("jenkins request %s: %w", req.URL.Path, err)
	}
	if resp == nil {
		return 0, nil, nil, fmt.Errorf("jenkins request %s: nil response", req.URL.Path)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read response %s: %w", req.URL.Path, err)
	}

	return resp.StatusCode, resp.Header, body, nil
}
