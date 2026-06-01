// Package gitea is a thin client for the Gitea API.
//
// It currently covers what ACL needs - a user's effective permission on a
// repository and team membership in an organization - and grows with later
// phases (reading the contract from a repository, posting reactions and
// comments). Requests authenticate with a bot-account token.
package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const requestTimeout = 10 * time.Second

// teamPageLimit is the page size used when listing organization teams.
const teamPageLimit = 50

// Client talks to a single Gitea instance with a fixed bot token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a Client for the instance at baseURL authenticating with token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// UserPermission returns the effective permission level of user on owner/repo:
// one of "none", "read", "write", "admin" or "owner". An unknown user or a
// non-collaborator resolves to "none" rather than an error.
func (c *Client) UserPermission(ctx context.Context, owner, repo, user string) (string, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/%s/collaborators/%s/permission",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(user))

	status, body, err := c.get(ctx, path)
	if err != nil {
		return "", err
	}

	switch status {
	case http.StatusOK:
		var parsed struct {
			Permission string `json:"permission"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return "", fmt.Errorf("decode permission for %s/%s: %w", owner, repo, err)
		}

		return parsed.Permission, nil
	case http.StatusNotFound:
		return "none", nil
	default:
		return "", fmt.Errorf("gitea permission for %s/%s: unexpected status %d", owner, repo, status)
	}
}

// IsTeamMember reports whether user belongs to team in org. A missing org or
// team, or a non-member, yields false without an error.
func (c *Client) IsTeamMember(ctx context.Context, org, team, user string) (bool, error) {
	id, found, err := c.teamID(ctx, org, team)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	path := fmt.Sprintf("/api/v1/teams/%d/members/%s", id, url.PathEscape(user))

	status, _, err := c.get(ctx, path)
	if err != nil {
		return false, err
	}

	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("gitea team membership for %s/%s: unexpected status %d", org, team, status)
	}
}

// teamID resolves a team name to its id within org, paging through the team list.
// A missing org or absent team reports found=false without an error.
func (c *Client) teamID(ctx context.Context, org, team string) (int64, bool, error) {
	for page := 1; ; page++ {
		path := fmt.Sprintf("/api/v1/orgs/%s/teams?limit=%d&page=%d", url.PathEscape(org), teamPageLimit, page)

		id, found, count, err := c.teamPage(ctx, path, team)
		if err != nil {
			return 0, false, err
		}
		if found {
			return id, true, nil
		}
		if count < teamPageLimit {
			return 0, false, nil
		}
	}
}

// teamPage fetches one page of org teams and looks for team by name, returning
// its id if present and the number of teams on the page (to detect the last one).
func (c *Client) teamPage(ctx context.Context, path, team string) (int64, bool, int, error) {
	status, body, err := c.get(ctx, path)
	if err != nil {
		return 0, false, 0, err
	}
	if status == http.StatusNotFound {
		return 0, false, 0, nil
	}
	if status != http.StatusOK {
		return 0, false, 0, fmt.Errorf("gitea list teams: unexpected status %d", status)
	}

	var teams []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &teams); err != nil {
		return 0, false, 0, fmt.Errorf("decode teams: %w", err)
	}

	for _, t := range teams {
		if t.Name == team {
			return t.ID, true, len(teams), nil
		}
	}

	return 0, false, len(teams), nil
}

// get issues an authenticated GET and returns the status code and full body. The
// body is read and the response closed here, so callers never hold the response.
func (c *Client) get(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

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
