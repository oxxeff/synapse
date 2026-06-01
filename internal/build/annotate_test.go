package build

import (
	"testing"

	"go.oxef.dev/ci/synapse/internal/contract"
)

func TestAnnotationBlock(t *testing.T) {
	t.Parallel()

	spec := &contract.Spec{Commands: map[string]contract.Command{
		"sync-github": {Params: map[string]contract.Param{"message": {Default: ""}}},
		"run":         {Params: map[string]contract.Param{"suite": {Default: "smoke"}}},
		"noparams":    {},
	}}

	// Only commands with params appear; commands and params are sorted; defaults
	// fill the values. noparams is omitted.
	want := "```synapse\n" +
		"run:\n  suite: \"smoke\"\n" +
		"sync-github:\n  message: \"\"\n" +
		"```"

	if got := AnnotationBlock(spec); got != want {
		t.Errorf("AnnotationBlock =\n%q\nwant\n%q", got, want)
	}
}

func TestAnnotationBlockNoConfigurableParams(t *testing.T) {
	t.Parallel()

	spec := &contract.Spec{Commands: map[string]contract.Command{
		"deploy": {},
	}}

	if got := AnnotationBlock(spec); got != "" {
		t.Errorf("AnnotationBlock = %q, want empty when no command has params", got)
	}
}

func TestHasSynapseBlock(t *testing.T) {
	t.Parallel()

	with := "intro\n\n```synapse\nrun:\n  suite: \"fast\"\n```"
	if !HasSynapseBlock(with) {
		t.Error("HasSynapseBlock = false, want true when a block is present")
	}

	without := "just a plain description without any block"
	if HasSynapseBlock(without) {
		t.Error("HasSynapseBlock = true, want false when no block is present")
	}
}
