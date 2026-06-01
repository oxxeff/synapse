package router

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.oxef.dev/ci/synapse/internal/executor"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

type fakeGitea struct {
	mu sync.Mutex

	contract      string
	contractFound bool
	contractErr   error
	perm          string
	teamMember    map[string]bool
	botLogin      string

	reads     int
	readRef   string
	comments  []string
	reactions []string
	prBodies  []string
}

func (f *fakeGitea) ReadFile(_ context.Context, _, _, _, ref string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.readRef = ref
	if f.contractErr != nil {
		return nil, false, f.contractErr
	}
	if !f.contractFound {
		return nil, false, nil
	}

	return []byte(f.contract), true, nil
}

func (f *fakeGitea) CreateComment(_ context.Context, _, _ string, _ int, body string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, body)

	return int64(len(f.comments)), nil
}

func (f *fakeGitea) CreateReaction(_ context.Context, _, _ string, _ int64, reaction string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, reaction)

	return nil
}

func (f *fakeGitea) UpdatePRBody(_ context.Context, _, _ string, _ int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prBodies = append(f.prBodies, body)

	return nil
}

func (f *fakeGitea) CurrentUser(context.Context) (string, error) { return f.botLogin, nil }
func (f *fakeGitea) UserPermission(context.Context, string, string, string) (string, error) {
	return f.perm, nil
}

func (f *fakeGitea) IsTeamMember(_ context.Context, _, team, _ string) (bool, error) {
	return f.teamMember[team], nil
}

type fakeExecutor struct {
	mu         sync.Mutex
	triggerErr error
	triggered  int
	lastParams map[string]string
	status     executor.Status
	statusErr  error
}

func (f *fakeExecutor) Trigger(_ context.Context, _ string, params map[string]string) (executor.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.triggerErr != nil {
		return executor.Run{}, f.triggerErr
	}
	f.triggered++
	f.lastParams = params

	return executor.Run{ID: "q1"}, nil
}

func (f *fakeExecutor) Status(context.Context, executor.Run) (executor.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.status, f.statusErr
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testRouter(g Gitea, exec executor.Executor) *Router {
	return New(g, exec, time.Millisecond, time.Minute, discardLog())
}

func commentEvent(body string) webhook.Event {
	return webhook.Event{
		Kind:      webhook.KindComment,
		State:     webhook.StateOpen,
		Repo:      webhook.Repo{Owner: "acme", Name: "app", FullName: "acme/app"},
		PR:        webhook.PR{Number: 7},
		Sender:    "alice",
		Comment:   body,
		CommentID: 5,
	}
}

const okContract = `
version: "1"
commands:
  run:
    on_comment: "/run"
    job: "j"
    ack:
      reaction: rocket
      comment: true
`

func TestHandle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		contract      string
		contractFound bool
		contractErr   error
		perm          string
		triggerErr    error
		botLogin      string
		event         webhook.Event
		wantReads     int
		wantTriggered int
		wantComments  int
		wantReactions int
	}{
		{
			name:     "bot event ignored",
			contract: okContract, contractFound: true, perm: "write",
			botLogin: "alice",
			event:    commentEvent("/run"),
		},
		{
			name:  "repo not onboarded",
			event: commentEvent("/run"),
			// contractFound false
			wantReads: 1,
		},
		{
			name:        "contract transport error",
			contractErr: errors.New("boom"),
			event:       commentEvent("/run"),
			wantReads:   1,
		},
		{
			name:     "schema error comments once",
			contract: "version: \"2\"\ncommands: {}\n", contractFound: true,
			event:        commentEvent("/run"),
			wantReads:    1,
			wantComments: 1,
		},
		{
			name:     "no command matched",
			contract: okContract, contractFound: true, perm: "write",
			event:     commentEvent("/nope"),
			wantReads: 1,
		},
		{
			name:     "happy path triggers and acks",
			contract: okContract, contractFound: true, perm: "write",
			event:         commentEvent("/run"),
			wantReads:     1,
			wantTriggered: 1,
			wantReactions: 1,
		},
		{
			name:     "acl denial reacts and comments",
			contract: okContract, contractFound: true, perm: "read",
			event:         commentEvent("/run"),
			wantReads:     1,
			wantComments:  1,
			wantReactions: 1,
		},
		{
			name:     "trigger failure comments",
			contract: okContract, contractFound: true, perm: "write",
			triggerErr:   errors.New("down"),
			event:        commentEvent("/run"),
			wantReads:    1,
			wantComments: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := &fakeGitea{
				contract:      tt.contract,
				contractFound: tt.contractFound,
				contractErr:   tt.contractErr,
				perm:          tt.perm,
			}
			exec := &fakeExecutor{triggerErr: tt.triggerErr}
			r := testRouter(g, exec)
			r.botLogin = tt.botLogin

			r.Handle(context.Background(), tt.event)

			if g.reads != tt.wantReads {
				t.Errorf("reads = %d, want %d", g.reads, tt.wantReads)
			}
			if exec.triggered != tt.wantTriggered {
				t.Errorf("triggered = %d, want %d", exec.triggered, tt.wantTriggered)
			}
			if len(g.comments) != tt.wantComments {
				t.Errorf("comments = %d (%v), want %d", len(g.comments), g.comments, tt.wantComments)
			}
			if len(g.reactions) != tt.wantReactions {
				t.Errorf("reactions = %d (%v), want %d", len(g.reactions), g.reactions, tt.wantReactions)
			}
		})
	}
}

func TestHandleReadsDefaultBranch(t *testing.T) {
	t.Parallel()

	g := fakeGitea{contract: okContract, contractFound: true, perm: "write"}
	exec := fakeExecutor{}
	r := testRouter(&g, &exec)

	r.Handle(context.Background(), commentEvent("/run"))

	if g.readRef != "" {
		t.Errorf("contract read ref = %q, want empty (default branch)", g.readRef)
	}
	if exec.lastParams["SYNAPSE_REPO"] != "acme/app" {
		t.Errorf("SYNAPSE_REPO = %q, want acme/app", exec.lastParams["SYNAPSE_REPO"])
	}
}

func TestHandleTag(t *testing.T) {
	t.Parallel()

	const tagContract = `
version: "1"
commands:
  deliver:
    on_tag: "v*"
    job: "infra/git-sync"
    parameters:
      REVISION: "{{ tag.commit }}"
`
	g := fakeGitea{contract: tagContract, contractFound: true, perm: "write"}
	exec := fakeExecutor{}
	r := testRouter(&g, &exec)

	evt := webhook.Event{
		Kind:      webhook.KindTag,
		Repo:      webhook.Repo{Owner: "acme", Name: "app", FullName: "acme/app"},
		Sender:    "alice",
		Tag:       "v1.0.0",
		TagCommit: "abc123",
	}
	r.Handle(context.Background(), evt)

	if exec.triggered != 1 {
		t.Errorf("triggered = %d, want 1", exec.triggered)
	}
	if exec.lastParams["REVISION"] != "abc123" {
		t.Errorf("REVISION = %q, want abc123", exec.lastParams["REVISION"])
	}
	if exec.lastParams["SYNAPSE_TAG"] != "v1.0.0" {
		t.Errorf("SYNAPSE_TAG = %q, want v1.0.0", exec.lastParams["SYNAPSE_TAG"])
	}
	// A tag has no pull request, so nothing is posted back.
	if len(g.comments) != 0 {
		t.Errorf("comments = %v, want none for a tag event", g.comments)
	}
	if len(g.reactions) != 0 {
		t.Errorf("reactions = %v, want none for a tag event", g.reactions)
	}
}

func openedEvent(body string) webhook.Event {
	return webhook.Event{
		Kind:   webhook.KindPROpened,
		State:  webhook.StateOpen,
		Repo:   webhook.Repo{Owner: "acme", Name: "app", FullName: "acme/app"},
		PR:     webhook.PR{Number: 8, Body: body},
		Sender: "alice",
	}
}

func TestHandleAnnotate(t *testing.T) {
	t.Parallel()

	const annotateOn = `
version: "1"
annotate_pr: true
commands:
  sync-github:
    on_merge: true
    available_in: [pr_merged]
    job: "j"
    params:
      message:
        required: false
        default: ""
`
	const annotateOff = `
version: "1"
commands:
  sync-github:
    on_merge: true
    available_in: [pr_merged]
    job: "j"
    params:
      message:
        required: false
        default: ""
`

	tests := []struct {
		name        string
		contract    string
		body        string
		wantUpdates int
	}{
		{name: "opt-in empty body appends block", contract: annotateOn, body: "", wantUpdates: 1},
		{name: "idempotent when block present", contract: annotateOn, body: "desc\n```synapse\nsync-github:\n  message: \"x\"\n```", wantUpdates: 0},
		{name: "off by default", contract: annotateOff, body: "", wantUpdates: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := &fakeGitea{contract: tt.contract, contractFound: true}
			r := testRouter(g, &fakeExecutor{})

			r.Handle(context.Background(), openedEvent(tt.body))

			if len(g.prBodies) != tt.wantUpdates {
				t.Fatalf("pr body updates = %d, want %d", len(g.prBodies), tt.wantUpdates)
			}
			if tt.wantUpdates == 1 && !strings.Contains(g.prBodies[0], "```synapse") {
				t.Errorf("updated body has no synapse block: %q", g.prBodies[0])
			}
		})
	}
}

func TestHandleCommandsIndependent(t *testing.T) {
	t.Parallel()

	const c = `
version: "1"
commands:
  run:
    on_comment: "/go"
    job: "j"
  deploy:
    on_comment: "/go"
    min_permission: admin
    job: "d"
`
	g := fakeGitea{contract: c, contractFound: true, perm: "write"}
	exec := fakeExecutor{}
	r := testRouter(&g, &exec)

	r.Handle(context.Background(), commentEvent("/go"))

	// run is allowed (write), deploy denied (needs admin): one triggers, one comments.
	if exec.triggered != 1 {
		t.Errorf("triggered = %d, want 1", exec.triggered)
	}
	if len(g.comments) != 1 || !strings.Contains(g.comments[0], "deploy") {
		t.Errorf("comments = %v, want one denial mentioning deploy", g.comments)
	}
}
