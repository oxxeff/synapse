package contract

import (
	"testing"

	"go.oxef.dev/ci/synapse/internal/webhook"
)

const matchContract = `
version: "1"
commands:
  run:
    on_comment: "/run"
    available_in: [pr_open]
    job: j
  preview:
    on_label: "preview"
    available_in: [pr_open]
    job: j
  redeploy:
    on_comment: "/redeploy"
    available_in: [pr_merged]
    job: j
  anymerge:
    on_merge: true
    available_in: [pr_merged]
    job: j
  publish:
    on_merge:
      label: "publish"
    available_in: [pr_merged]
    job: j
`

func matchedNames(matches []Match) []string {
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Name
	}

	return names
}

func equalNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}

func TestMatch(t *testing.T) {
	t.Parallel()

	spec, err := Parse([]byte(matchContract))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tests := []struct {
		name  string
		event webhook.Event
		want  []string
	}{
		{
			name:  "comment invokes command",
			event: webhook.Event{Kind: webhook.KindComment, State: webhook.StateOpen, Comment: "/run --suite=integration"},
			want:  []string{"run"},
		},
		{
			name:  "comment token boundary",
			event: webhook.Event{Kind: webhook.KindComment, State: webhook.StateOpen, Comment: "/runner"},
			want:  nil,
		},
		{
			name:  "comment out of state",
			event: webhook.Event{Kind: webhook.KindComment, State: webhook.StateMerged, Comment: "/run"},
			want:  nil,
		},
		{
			name:  "comment after merge",
			event: webhook.Event{Kind: webhook.KindComment, State: webhook.StateMerged, Comment: "/redeploy"},
			want:  []string{"redeploy"},
		},
		{
			name:  "label present",
			event: webhook.Event{Kind: webhook.KindLabel, State: webhook.StateOpen, Labels: []string{"preview", "review"}},
			want:  []string{"preview"},
		},
		{
			name:  "label absent",
			event: webhook.Event{Kind: webhook.KindLabel, State: webhook.StateOpen, Labels: []string{"review"}},
			want:  nil,
		},
		{
			name:  "merge with required label matches both, sorted",
			event: webhook.Event{Kind: webhook.KindMerge, State: webhook.StateMerged, Labels: []string{"publish"}},
			want:  []string{"anymerge", "publish"},
		},
		{
			name:  "merge without required label",
			event: webhook.Event{Kind: webhook.KindMerge, State: webhook.StateMerged, Labels: nil},
			want:  []string{"anymerge"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := matchedNames(spec.Match(tt.event))
			if !equalNames(got, tt.want) {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
		})
	}
}
