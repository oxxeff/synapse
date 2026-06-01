package acl

import (
	"context"
	"errors"
	"testing"
)

// fakeGitea is a hand-written stub recording call counts so tests can assert
// short-circuiting.
type fakeGitea struct {
	perm      string
	permErr   error
	members   map[string]bool // team -> whether sender is a member
	teamErr   error
	permCalls int
	teamCalls int
}

func (f *fakeGitea) UserPermission(_ context.Context, _, _, _ string) (string, error) {
	f.permCalls++

	return f.perm, f.permErr
}

func (f *fakeGitea) IsTeamMember(_ context.Context, _, team, _ string) (bool, error) {
	f.teamCalls++
	if f.teamErr != nil {
		return false, f.teamErr
	}

	return f.members[team], nil
}

func TestAuthorize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		policy    Policy
		gitea     fakeGitea
		want      bool
		wantErr   bool
		permCalls int
		teamCalls int
	}{
		{
			name:   "allowed user short-circuits",
			policy: Policy{AllowedUsers: []string{"alice"}},
			gitea:  fakeGitea{perm: "owner"},
			want:   true,
		},
		{
			name:   "allowed users restrict outsiders",
			policy: Policy{AllowedUsers: []string{"someone-else"}},
			gitea:  fakeGitea{perm: "owner"},
			want:   false, // constrained to a user list; permission is not consulted
		},
		{
			name:      "team member allowed",
			policy:    Policy{AllowedTeams: []string{"dev"}},
			gitea:     fakeGitea{members: map[string]bool{"dev": true}},
			want:      true,
			teamCalls: 1,
		},
		{
			name:      "not a team member",
			policy:    Policy{AllowedTeams: []string{"dev"}},
			gitea:     fakeGitea{members: map[string]bool{"dev": false}},
			want:      false,
			teamCalls: 1,
		},
		{
			name:      "min_permission met",
			policy:    Policy{MinPermission: "write"},
			gitea:     fakeGitea{perm: "write"},
			want:      true,
			permCalls: 1,
		},
		{
			name:      "min_permission not met",
			policy:    Policy{MinPermission: "write"},
			gitea:     fakeGitea{perm: "read"},
			want:      false,
			permCalls: 1,
		},
		{
			name:      "owner satisfies admin",
			policy:    Policy{MinPermission: "admin"},
			gitea:     fakeGitea{perm: "owner"},
			want:      true,
			permCalls: 1,
		},
		{
			name:      "empty policy defaults to write, write passes",
			policy:    Policy{},
			gitea:     fakeGitea{perm: "write"},
			want:      true,
			permCalls: 1,
		},
		{
			name:      "empty policy defaults to write, read fails",
			policy:    Policy{},
			gitea:     fakeGitea{perm: "read"},
			want:      false,
			permCalls: 1,
		},
		{
			name:      "permission api error denies",
			policy:    Policy{MinPermission: "write"},
			gitea:     fakeGitea{permErr: errors.New("boom")},
			want:      false,
			wantErr:   true,
			permCalls: 1,
		},
		{
			name:      "team api error denies",
			policy:    Policy{AllowedTeams: []string{"dev"}},
			gitea:     fakeGitea{teamErr: errors.New("boom")},
			want:      false,
			wantErr:   true,
			teamCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := tt.gitea
			got, err := Authorize(context.Background(), &g, "acme", "app", "alice", tt.policy)

			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Authorize = %v, want %v", got, tt.want)
			}
			if g.permCalls != tt.permCalls {
				t.Errorf("UserPermission calls = %d, want %d", g.permCalls, tt.permCalls)
			}
			if g.teamCalls != tt.teamCalls {
				t.Errorf("IsTeamMember calls = %d, want %d", g.teamCalls, tt.teamCalls)
			}
		})
	}
}
