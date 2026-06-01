// Package webhook turns an inbound Gitea delivery into a normalized,
// executor-neutral event and verifies its HMAC signature.
//
// It covers two steps of the request lifecycle: signature verification (the only
// trust boundary for inbound events) and payload parsing. Routing of the
// resulting event onto executor targets belongs to later phases - this package
// neither reads .synapse.yaml nor talks to Gitea.
package webhook

import (
	"encoding/json"
	"fmt"
)

// Kind classifies a delivery by the trigger family it can match. Deliveries that
// no trigger can act on (other actions, other event types) are Unsupported and
// carry no further fields.
type Kind string

// Recognized event kinds. KindUnsupported marks deliveries the router ignores.
const (
	KindUnsupported Kind = "unsupported"
	KindComment     Kind = "comment"
	KindLabel       Kind = "label"
	KindMerge       Kind = "merge"
)

// PRState is the pull request state a command's available_in is matched against.
type PRState string

// Pull request states, named as in the .synapse.yaml contract.
const (
	StateOpen   PRState = "pr_open"
	StateMerged PRState = "pr_merged"
)

// Event is the normalized view of a Gitea delivery that the router consumes. It
// is executor-neutral: it names what happened on which pull request and by whom,
// not what to do about it.
type Event struct {
	Kind      Kind
	State     PRState
	Repo      Repo
	PR        PR
	Sender    string   // login of the event initiator (sender.login)
	Comment   string   // comment body, set when Kind is KindComment
	CommentID int64    // id of the triggering comment, set when Kind is KindComment
	Labels    []string // current PR labels, set when Kind is KindLabel
}

// Repo identifies the repository the event originates from.
type Repo struct {
	Owner    string
	Name     string
	FullName string
}

// PR carries the pull request fields available in the delivery. Fields absent
// from a given event type stay zero (for example MergeCommit outside a merge).
type PR struct {
	Number      int
	Title       string
	Body        string // PR description, source of the synapse parameter block
	HeadRef     string
	BaseRef     string
	MergeCommit string
}

// Parse builds an Event from the Gitea event name (the value of the event-type
// header) and the raw delivery body. Event types and actions that map to no
// trigger return an Unsupported event with a nil error; only a malformed payload
// is an error.
func Parse(eventType string, body []byte) (Event, error) {
	switch eventType {
	case "issue_comment":
		return parseIssueComment(body)
	case "pull_request":
		return parsePullRequest(body)
	default:
		return Event{Kind: KindUnsupported}, nil
	}
}

// giteaRepo and giteaUser are the repository and actor blocks shared by every
// Gitea payload. Only the fields the router needs are decoded.
type giteaRepo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type giteaUser struct {
	Login string `json:"login"`
}

func (r giteaRepo) repo() Repo {
	return Repo{Owner: r.Owner.Login, Name: r.Name, FullName: r.FullName}
}

type issueCommentPayload struct {
	Action string `json:"action"`
	Issue  struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		// PullRequest is present only when the comment is on a pull request, so a
		// nil value distinguishes plain-issue comments, which the router ignores.
		PullRequest *struct {
			Merged bool `json:"merged"`
		} `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	} `json:"comment"`
	Repository giteaRepo `json:"repository"`
	Sender     giteaUser `json:"sender"`
}

func parseIssueComment(body []byte) (Event, error) {
	var p issueCommentPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("parse issue_comment payload: %w", err)
	}

	if p.Action != "created" || p.Issue.PullRequest == nil {
		return Event{Kind: KindUnsupported}, nil
	}

	return Event{
		Kind:      KindComment,
		State:     stateFromMerged(p.Issue.PullRequest.Merged),
		Repo:      p.Repository.repo(),
		PR:        PR{Number: p.Issue.Number, Title: p.Issue.Title, Body: p.Issue.Body},
		Sender:    p.Sender.Login,
		Comment:   p.Comment.Body,
		CommentID: p.Comment.ID,
	}, nil
}

type pullRequestPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number         int    `json:"number"`
		Title          string `json:"title"`
		Body           string `json:"body"`
		Merged         bool   `json:"merged"`
		MergeCommitSHA string `json:"merge_commit_sha"`
		Base           struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
	Repository giteaRepo `json:"repository"`
	Sender     giteaUser `json:"sender"`
}

func parsePullRequest(body []byte) (Event, error) {
	var p pullRequestPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("parse pull_request payload: %w", err)
	}

	pr := PR{
		Number:  p.PullRequest.Number,
		Title:   p.PullRequest.Title,
		Body:    p.PullRequest.Body,
		HeadRef: p.PullRequest.Head.Ref,
		BaseRef: p.PullRequest.Base.Ref,
	}

	// Gitea sends the full current label set, not the added/removed delta. Both a
	// label change and a merge expose it: resolving which label fired a command
	// (including on_merge with a required label) is left to matching.
	labels := make([]string, 0, len(p.PullRequest.Labels))
	for _, l := range p.PullRequest.Labels {
		labels = append(labels, l.Name)
	}

	switch {
	case p.Action == "label_updated":
		return Event{
			Kind:   KindLabel,
			State:  stateFromMerged(p.PullRequest.Merged),
			Repo:   p.Repository.repo(),
			PR:     pr,
			Sender: p.Sender.Login,
			Labels: labels,
		}, nil

	case p.Action == "closed" && p.PullRequest.Merged:
		pr.MergeCommit = p.PullRequest.MergeCommitSHA

		return Event{
			Kind:   KindMerge,
			State:  StateMerged,
			Repo:   p.Repository.repo(),
			PR:     pr,
			Sender: p.Sender.Login,
			Labels: labels,
		}, nil

	default:
		return Event{Kind: KindUnsupported}, nil
	}
}

func stateFromMerged(merged bool) PRState {
	if merged {
		return StateMerged
	}

	return StateOpen
}
