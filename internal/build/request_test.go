package build

import (
	"testing"

	"go.oxef.dev/ci/synapse/internal/contract"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

func TestRequest(t *testing.T) {
	t.Parallel()

	cmd := contract.Command{
		Job:       "load-tests",
		OnComment: "/run",
		Params:    map[string]contract.Param{"suite": {Default: "smoke"}},
		Parameters: map[string]string{
			"SUITE": "{{ params.suite }}",
			"REPO":  "{{ repo.full_name }}",
			"PR":    "{{ pr.number }}",
		},
	}
	evt := webhook.Event{
		Kind:      webhook.KindComment,
		State:     webhook.StateOpen,
		Repo:      webhook.Repo{Owner: "acme", Name: "app", FullName: "acme/app"},
		PR:        webhook.PR{Number: 7, HeadRef: "feat", BaseRef: "main"},
		Sender:    "alice",
		Comment:   "/run --suite=fast",
		CommentID: 42,
	}

	target, params, err := Request("run", cmd, evt)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	if target != "load-tests" {
		t.Errorf("target = %q, want load-tests", target)
	}

	want := map[string]string{
		"SUITE":              "fast",
		"REPO":               "acme/app",
		"PR":                 "7",
		"SYNAPSE_REPO":       "acme/app",
		"SYNAPSE_PR":         "7",
		"SYNAPSE_PR_HEAD":    "feat",
		"SYNAPSE_PR_BASE":    "main",
		"SYNAPSE_EVENT":      "comment",
		"SYNAPSE_COMMENT_ID": "42",
	}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("param %q = %q, want %q", k, params[k], v)
		}
	}
}

func TestRequestUnknownVariable(t *testing.T) {
	t.Parallel()

	cmd := contract.Command{
		Job:        "j",
		OnMerge:    &contract.MergeTrigger{Enabled: true},
		Parameters: map[string]string{"X": "{{ pr.nope }}"},
	}
	evt := webhook.Event{Kind: webhook.KindMerge, State: webhook.StateMerged}

	if _, _, err := Request("c", cmd, evt); err == nil {
		t.Fatal("want error for unknown template variable, got nil")
	}
}

func TestRequestMergeNoCommentID(t *testing.T) {
	t.Parallel()

	cmd := contract.Command{Job: "sync", OnMerge: &contract.MergeTrigger{Enabled: true}}
	evt := webhook.Event{
		Kind:  webhook.KindMerge,
		State: webhook.StateMerged,
		Repo:  webhook.Repo{FullName: "acme/app"},
		PR:    webhook.PR{Number: 9},
	}

	_, params, err := Request("sync", cmd, evt)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	if params["SYNAPSE_EVENT"] != "merge" {
		t.Errorf("SYNAPSE_EVENT = %q, want merge", params["SYNAPSE_EVENT"])
	}
	if _, ok := params["SYNAPSE_COMMENT_ID"]; ok {
		t.Error("SYNAPSE_COMMENT_ID should be absent for a merge event")
	}
}
