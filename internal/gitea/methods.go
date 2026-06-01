package gitea

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ReadFile returns the decoded contents of path in owner/repo at ref. An empty
// ref reads the repository default branch. A missing file (or repo) reports
// found=false without an error, so "no contract" is distinct from a transport
// failure.
func (c *Client) ReadFile(ctx context.Context, owner, repo, path, ref string) ([]byte, bool, error) {
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/contents/%s",
		url.PathEscape(owner), url.PathEscape(repo), escapePath(path))
	if ref != "" {
		endpoint += "?" + url.Values{"ref": {ref}}.Encode()
	}

	status, body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, false, err
	}

	switch status {
	case http.StatusOK:
		var file struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err := json.Unmarshal(body, &file); err != nil {
			return nil, false, fmt.Errorf("decode contents %s: %w", path, err)
		}
		if file.Encoding != "base64" {
			return nil, false, fmt.Errorf("gitea contents %s: unexpected encoding %q", path, file.Encoding)
		}

		// Gitea may wrap the base64 payload; strip whitespace before decoding.
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
		if err != nil {
			return nil, false, fmt.Errorf("decode contents base64 %s: %w", path, err)
		}

		return decoded, true, nil
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("gitea contents %s/%s: unexpected status %d", owner, repo, status)
	}
}

// CreateComment posts body as a new comment on issue/PR number in owner/repo from
// the bot account and returns the created comment id.
func (c *Client) CreateComment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), number)

	status, resp, err := c.send(ctx, http.MethodPost, endpoint, map[string]string{"body": body})
	if err != nil {
		return 0, err
	}
	if status != http.StatusCreated {
		return 0, fmt.Errorf("gitea comment %s/%s#%d: unexpected status %d", owner, repo, number, status)
	}

	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil {
		return 0, fmt.Errorf("decode comment: %w", err)
	}

	return created.ID, nil
}

// CreateReaction adds reaction (a Gitea reaction name) to commentID in owner/repo
// from the bot account. A reaction the bot already placed (200) is success.
func (c *Client) CreateReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error {
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/issues/comments/%d/reactions",
		url.PathEscape(owner), url.PathEscape(repo), commentID)

	status, _, err := c.send(ctx, http.MethodPost, endpoint, map[string]string{"content": reaction})
	if err != nil {
		return err
	}

	switch status {
	case http.StatusCreated, http.StatusOK:
		return nil
	default:
		return fmt.Errorf("gitea reaction on comment %d: unexpected status %d", commentID, status)
	}
}

// UpdatePRBody replaces the description (body) of pull request number in
// owner/repo from the bot account. It is used to seed the synapse parameter block
// when a pull request is opened.
func (c *Client) UpdatePRBody(ctx context.Context, owner, repo string, number int, body string) error {
	endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d",
		url.PathEscape(owner), url.PathEscape(repo), number)

	status, _, err := c.send(ctx, http.MethodPatch, endpoint, map[string]string{"body": body})
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("gitea edit pull request %s/%s#%d: unexpected status %d", owner, repo, number, status)
	}

	return nil
}

// CurrentUser returns the login of the account the token authenticates as, used
// to recognise and ignore the service's own events.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	status, body, err := c.get(ctx, "/api/v1/user")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("gitea current user: unexpected status %d", status)
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", fmt.Errorf("decode user: %w", err)
	}

	return user.Login, nil
}

// send issues an authenticated request with an optional JSON body and returns the
// status code and response body. The body is read and the response closed here,
// so callers never hold the response.
func (c *Client) send(ctx context.Context, method, path string, payload any) (int, []byte, error) {
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("gitea request %s: %w", path, err)
	}
	if resp == nil {
		return 0, nil, fmt.Errorf("gitea request %s: nil response", path)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response %s: %w", path, err)
	}

	return resp.StatusCode, body, nil
}

// escapePath escapes each segment of a repository file path, preserving slashes.
func escapePath(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}

	return strings.Join(segments, "/")
}
