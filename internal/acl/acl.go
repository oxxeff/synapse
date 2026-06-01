// Package acl decides whether the initiator of an event may run a command,
// using a command's policy and the Gitea API.
//
// The initiator passes if any specified criterion holds (union by OR): they are
// in allowed_users, they belong to an allowed_team, or their effective
// repository permission meets min_permission. When the policy specifies nothing,
// the default is min_permission write - triggering CI is allowed to anyone who
// could already push. Any Gitea API error is fail-closed: it denies.
package acl

import (
	"context"
	"fmt"
	"slices"
)

const permWrite = "write"

// Policy is a command's access rule. Empty fields mean "not specified".
type Policy struct {
	MinPermission string
	AllowedUsers  []string
	AllowedTeams  []string
}

// Gitea is the subset of the Gitea API authorization needs. *gitea.Client
// satisfies it.
type Gitea interface {
	UserPermission(ctx context.Context, owner, repo, user string) (string, error)
	IsTeamMember(ctx context.Context, org, team, user string) (bool, error)
}

// Authorize reports whether sender may run a command with policy p on owner/repo.
// Checks run cheapest first and short-circuit on the first grant: the local user
// list, then team membership, then repository permission. An API error denies
// and is returned for logging.
func Authorize(ctx context.Context, g Gitea, owner, repo, sender string, p Policy) (bool, error) {
	if slices.Contains(p.AllowedUsers, sender) {
		return true, nil
	}

	for _, team := range p.AllowedTeams {
		member, err := g.IsTeamMember(ctx, owner, team, sender)
		if err != nil {
			return false, fmt.Errorf("check team %q membership: %w", team, err)
		}
		if member {
			return true, nil
		}
	}

	required := p.MinPermission
	if !p.constrained() {
		required = permWrite
	}
	if required == "" {
		// Only an allowlist was specified and sender was not on it.
		return false, nil
	}

	perm, err := g.UserPermission(ctx, owner, repo, sender)
	if err != nil {
		return false, fmt.Errorf("check permission: %w", err)
	}

	return rank(perm) >= rank(required), nil
}

// constrained reports whether the policy specifies any access criterion.
func (p Policy) constrained() bool {
	return p.MinPermission != "" || len(p.AllowedUsers) > 0 || len(p.AllowedTeams) > 0
}

// rank orders Gitea permission levels so they can be compared. Unknown levels
// (including "none") rank lowest, so they never satisfy a requirement.
func rank(level string) int {
	switch level {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	case "owner":
		return 4
	default:
		return 0
	}
}
