package build

import (
	"testing"

	"go.oxef.dev/ci/synapse/internal/contract"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

func TestAssembleParamsPriority(t *testing.T) {
	t.Parallel()

	cmd := contract.Command{
		OnComment: "/run",
		Params: map[string]contract.Param{
			"suite":   {Default: "smoke"},
			"profile": {},
			"token":   {Required: true},
			"verbose": {},
		},
	}
	evt := webhook.Event{
		Kind:    webhook.KindComment,
		Comment: "/run --suite=fast --verbose",
		PR: webhook.PR{Body: "intro\n" +
			"```synapse\n" +
			"run:\n" +
			"  suite: fromblock\n" +
			"  profile: heavy\n" +
			"  token: t1\n" +
			"```\n" +
			"outro"},
	}

	got, err := AssembleParams("run", cmd, evt)
	if err != nil {
		t.Fatalf("AssembleParams: %v", err)
	}

	want := map[string]string{
		"suite":   "fast",  // inline beats block and default
		"profile": "heavy", // block beats default
		"token":   "t1",    // block satisfies required
		"verbose": "true",  // bare inline flag
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("param %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestAssembleParamsDefaultAndOptional(t *testing.T) {
	t.Parallel()

	cmd := contract.Command{
		OnComment: "/run",
		Params: map[string]contract.Param{
			"suite":   {Default: "smoke"},
			"profile": {},
		},
	}
	evt := webhook.Event{Kind: webhook.KindComment, Comment: "/run"}

	got, err := AssembleParams("run", cmd, evt)
	if err != nil {
		t.Fatalf("AssembleParams: %v", err)
	}

	if got["suite"] != "smoke" {
		t.Errorf("suite = %q, want default smoke", got["suite"])
	}
	if v, ok := got["profile"]; !ok || v != "" {
		t.Errorf("optional profile = %q (present %v), want empty present", v, ok)
	}
}

func TestAssembleParamsRequiredMissing(t *testing.T) {
	t.Parallel()

	cmd := contract.Command{
		OnComment: "/run",
		Params:    map[string]contract.Param{"token": {Required: true}},
	}
	evt := webhook.Event{Kind: webhook.KindComment, Comment: "/run"}

	if _, err := AssembleParams("run", cmd, evt); err == nil {
		t.Fatal("want error for missing required parameter, got nil")
	}
}

func TestAssembleParamsLabelEventIgnoresInline(t *testing.T) {
	t.Parallel()

	// A label event has no comment, so inline args do not apply; the block does.
	cmd := contract.Command{
		OnLabel: "preview",
		Params:  map[string]contract.Param{"env": {Default: "staging"}},
	}
	evt := webhook.Event{
		Kind: webhook.KindLabel,
		PR:   webhook.PR{Body: "```synapse\npreview:\n  env: prod\n```"},
	}

	got, err := AssembleParams("preview", cmd, evt)
	if err != nil {
		t.Fatalf("AssembleParams: %v", err)
	}
	if got["env"] != "prod" {
		t.Errorf("env = %q, want prod from block", got["env"])
	}
}
