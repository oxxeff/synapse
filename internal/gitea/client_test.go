package gitea

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload string) {
	t.Helper()

	w.WriteHeader(status)
	if _, err := io.WriteString(w, payload); err != nil {
		t.Errorf("write response: %v", err)
	}
}

// newTestServer wires the Gitea endpoints the client uses. It records the last
// Authorization header so tests can assert authentication.
func newTestServer(t *testing.T, auth *string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/collaborators/{user}/permission", func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		switch r.PathValue("user") {
		case "owneruser":
			writeJSON(t, w, http.StatusOK, `{"permission":"owner"}`)
		case "ghost":
			writeJSON(t, w, http.StatusNotFound, "")
		case "boom":
			writeJSON(t, w, http.StatusInternalServerError, "")
		default:
			writeJSON(t, w, http.StatusOK, `{"permission":"read"}`)
		}
	})

	mux.HandleFunc("GET /api/v1/orgs/{org}/teams", func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		if r.PathValue("org") == "noorg" {
			writeJSON(t, w, http.StatusNotFound, "")
			return
		}
		writeJSON(t, w, http.StatusOK, `[{"id":7,"name":"dev"},{"id":8,"name":"ops"}]`)
	})

	mux.HandleFunc("GET /api/v1/teams/{id}/members/{user}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("user") == "alice" {
			writeJSON(t, w, http.StatusOK, `{"login":"alice"}`)
			return
		}
		writeJSON(t, w, http.StatusNotFound, "")
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestUserPermission(t *testing.T) {
	t.Parallel()

	var auth string
	srv := newTestServer(t, &auth)
	c := New(srv.URL, "secret-token")

	tests := []struct {
		name    string
		user    string
		want    string
		wantErr bool
	}{
		{name: "owner level", user: "owneruser", want: "owner"},
		{name: "unknown user is none", user: "ghost", want: "none"},
		{name: "default read", user: "carol", want: "read"},
		{name: "server error", user: "boom", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.UserPermission(context.Background(), "acme", "app", tt.user)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("UserPermission = %q, want %q", got, tt.want)
			}
		})
	}

	if auth != "token secret-token" {
		t.Errorf("Authorization = %q, want %q", auth, "token secret-token")
	}
}

func TestIsTeamMember(t *testing.T) {
	t.Parallel()

	var auth string
	srv := newTestServer(t, &auth)
	c := New(srv.URL, "secret-token")

	tests := []struct {
		name string
		org  string
		team string
		user string
		want bool
	}{
		{name: "member", org: "acme", team: "dev", user: "alice", want: true},
		{name: "not a member", org: "acme", team: "dev", user: "bob", want: false},
		{name: "team absent", org: "acme", team: "ghosts", user: "alice", want: false},
		{name: "org absent", org: "noorg", team: "dev", user: "alice", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.IsTeamMember(context.Background(), tt.org, tt.team, tt.user)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got != tt.want {
				t.Errorf("IsTeamMember = %v, want %v", got, tt.want)
			}
		})
	}
}
