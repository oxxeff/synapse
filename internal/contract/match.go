package contract

import (
	"slices"
	"sort"
	"strings"

	"go.oxef.dev/ci/synapse/internal/webhook"
)

// Match is a command selected for an event, paired with its name.
type Match struct {
	Name    string
	Command Command
}

// Match returns the commands triggered by evt: those whose available_in admits
// the PR state and whose trigger fires for the event. Results are sorted by
// command name for deterministic behaviour.
func (s *Spec) Match(evt webhook.Event) []Match {
	var matches []Match

	for name, c := range s.Commands {
		if !availableIn(c.AvailableIn, evt.State) {
			continue
		}
		if !c.triggeredBy(evt) {
			continue
		}

		matches = append(matches, Match{Name: name, Command: c})
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })

	return matches
}

func (c Command) triggeredBy(evt webhook.Event) bool {
	switch evt.Kind {
	case webhook.KindComment:
		return c.OnComment != "" && commentMatches(evt.Comment, c.OnComment)
	case webhook.KindLabel:
		return c.OnLabel != "" && slices.Contains(evt.Labels, c.OnLabel)
	case webhook.KindMerge:
		return c.OnMerge != nil && c.OnMerge.Enabled &&
			(c.OnMerge.Label == "" || slices.Contains(evt.Labels, c.OnMerge.Label))
	default:
		return false
	}
}

// commentMatches reports whether a comment body invokes key. The key must appear
// at the start on a token boundary, so "/run" matches "/run" and "/run --x" but
// not "/runner"; leading whitespace is ignored.
func commentMatches(body, key string) bool {
	body = strings.TrimLeft(body, " \t")
	if !strings.HasPrefix(body, key) {
		return false
	}

	rest := body[len(key):]
	if rest == "" {
		return true
	}

	switch rest[0] {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func availableIn(states []string, state webhook.PRState) bool {
	return slices.Contains(states, string(state))
}
