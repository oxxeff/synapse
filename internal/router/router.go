// Package router orchestrates the post-parse request lifecycle: it reads the
// repository contract, matches commands against the event, checks ACL, triggers
// the executor, acknowledges receipt and hands result reporting to a background
// pool.
//
// The contract is always read from the repository default branch, never from a
// pull request branch: a change to .synapse.yaml (including its ACL) must be
// merged - and so reviewed - before it takes effect, otherwise a PR author could
// widen their own access. See the architecture decision record.
package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.oxef.dev/ci/synapse/internal/acl"
	"go.oxef.dev/ci/synapse/internal/build"
	"go.oxef.dev/ci/synapse/internal/contract"
	"go.oxef.dev/ci/synapse/internal/executor"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

// contractPath is the fixed location of the contract in a repository.
const contractPath = ".synapse.yaml"

// denialReaction is placed on a triggering comment when its author fails ACL.
const denialReaction = "-1"

// Gitea is the Gitea surface the router needs. *gitea.Client satisfies it, and
// because it is a superset of acl.Gitea it is passed straight to acl.Authorize.
type Gitea interface {
	ReadFile(ctx context.Context, owner, repo, path, ref string) ([]byte, bool, error)
	CreateComment(ctx context.Context, owner, repo string, number int, body string) (int64, error)
	CreateReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error
	CurrentUser(ctx context.Context) (string, error)
	UserPermission(ctx context.Context, owner, repo, user string) (string, error)
	IsTeamMember(ctx context.Context, org, team, user string) (bool, error)
}

// Router runs the lifecycle for parsed events. It is safe for concurrent use:
// botLogin is set once via ResolveBotLogin before serving begins.
type Router struct {
	gitea    Gitea
	exec     executor.Executor
	waiters  *waiterPool
	botLogin string
	timeout  time.Duration
	log      *slog.Logger
}

// New builds a Router with its background report pool. poll is the build status
// poll interval; timeout bounds how long a build is awaited before reporting is
// handed to the executor fallback.
func New(g Gitea, exec executor.Executor, poll, timeout time.Duration, log *slog.Logger) *Router {
	return &Router{
		gitea:   g,
		exec:    exec,
		waiters: newWaiterPool(exec, g, poll, defaultWaiterWorkers, log),
		timeout: timeout,
		log:     log,
	}
}

// Run drives the background report waiters until ctx is cancelled. It is meant to
// run in its own goroutine for the service lifetime.
func (r *Router) Run(ctx context.Context) {
	r.waiters.Run(ctx)
}

// ResolveBotLogin records the bot account login for loop protection. It is called
// once at startup before the server accepts requests.
func (r *Router) ResolveBotLogin(ctx context.Context) error {
	login, err := r.gitea.CurrentUser(ctx)
	if err != nil {
		return err
	}

	r.botLogin = login

	return nil
}

// Handle runs the lifecycle for a parsed event: loop protection, contract read,
// command matching, then each matched command independently. It reports problems
// to the pull request and never returns them - the HTTP response was already sent.
func (r *Router) Handle(ctx context.Context, evt webhook.Event) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("router panic", "panic", p)
		}
	}()

	if r.botLogin != "" && evt.Sender == r.botLogin {
		r.log.Debug("ignoring own event", "sender", evt.Sender)
		return
	}

	data, found, err := r.gitea.ReadFile(ctx, evt.Repo.Owner, evt.Repo.Name, contractPath, "")
	if err != nil {
		// Transport failure: not the author's fault and we likely cannot reach
		// Gitea to comment - log and drop.
		r.log.Warn("read contract", "repo", evt.Repo.FullName, "error", err)
		return
	}
	if !found {
		r.log.Debug("no contract, repository not onboarded", "repo", evt.Repo.FullName)
		return
	}

	spec, err := contract.Parse(data)
	if err != nil {
		r.comment(ctx, evt, contractErrorMessage(err))
		return
	}

	matches := spec.Match(evt)
	if len(matches) == 0 {
		r.log.Debug("no command matched", "repo", evt.Repo.FullName)
		return
	}

	for _, m := range matches {
		r.runCommand(ctx, evt, m)
	}
}

// runCommand carries one matched command through ACL, parameter assembly,
// trigger, acknowledgement and report enqueue. A failure is reported to the PR
// and ends only this command, never the others.
func (r *Router) runCommand(ctx context.Context, evt webhook.Event, m contract.Match) {
	cmd := m.Command
	owner, repo := evt.Repo.Owner, evt.Repo.Name

	policy := acl.Policy{
		MinPermission: cmd.MinPermission,
		AllowedUsers:  cmd.AllowedUsers,
		AllowedTeams:  cmd.AllowedTeams,
	}

	allowed, err := acl.Authorize(ctx, r.gitea, owner, repo, evt.Sender, policy)
	if err != nil {
		r.deny(ctx, evt, fmt.Sprintf("Synapse: command %q: access check failed", m.Name))
		return
	}
	if !allowed {
		r.deny(ctx, evt, fmt.Sprintf("Synapse: %s is not allowed to run command %q", evt.Sender, m.Name))
		return
	}

	target, params, err := build.Request(m.Name, cmd, evt)
	if err != nil {
		r.comment(ctx, evt, fmt.Sprintf("Synapse: command %q: %s", m.Name, err))
		return
	}

	run, err := r.exec.Trigger(ctx, target, params)
	if err != nil {
		r.comment(ctx, evt, fmt.Sprintf("Synapse: command %q failed to start: %s", m.Name, err))
		return
	}

	r.ack(ctx, evt, cmd)

	r.waiters.Submit(reportJob{
		owner:   owner,
		repo:    repo,
		number:  evt.PR.Number,
		name:    m.Name,
		run:     run,
		ack:     cmd.Ack,
		timeout: r.timeout,
	})
}

// ack places the acknowledgement reaction on the triggering comment, if the
// event came from a comment and the command asks for one.
func (r *Router) ack(ctx context.Context, evt webhook.Event, cmd contract.Command) {
	if evt.Kind != webhook.KindComment || evt.CommentID == 0 || cmd.Ack == nil || cmd.Ack.Reaction == "" {
		return
	}

	if err := r.gitea.CreateReaction(ctx, evt.Repo.Owner, evt.Repo.Name, evt.CommentID, cmd.Ack.Reaction); err != nil {
		r.log.Warn("ack reaction", "error", err)
	}
}

// deny reports an access denial: a reaction on the triggering comment (when the
// event is a comment) and an explanatory comment. Denials are never silent.
func (r *Router) deny(ctx context.Context, evt webhook.Event, body string) {
	if evt.Kind == webhook.KindComment && evt.CommentID != 0 {
		if err := r.gitea.CreateReaction(ctx, evt.Repo.Owner, evt.Repo.Name, evt.CommentID, denialReaction); err != nil {
			r.log.Warn("denial reaction", "error", err)
		}
	}

	r.comment(ctx, evt, body)
}

func (r *Router) comment(ctx context.Context, evt webhook.Event, body string) {
	if _, err := r.gitea.CreateComment(ctx, evt.Repo.Owner, evt.Repo.Name, evt.PR.Number, body); err != nil {
		r.log.Warn("post comment", "repo", evt.Repo.FullName, "error", err)
	}
}

func contractErrorMessage(err error) string {
	var version *contract.UnsupportedVersionError
	if errors.As(err, &version) {
		return fmt.Sprintf("Synapse: .synapse.yaml %s", version.Error())
	}

	return fmt.Sprintf("Synapse: .synapse.yaml is invalid: %s", err)
}
