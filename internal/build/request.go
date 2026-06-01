package build

import (
	"fmt"

	"go.oxef.dev/ci/synapse/internal/contract"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

// Request builds the executor call for a matched command and its event. It
// returns the target (the command's job) and the parameters to pass: the
// command's parameter templates expanded against the event, merged with the
// SYNAPSE_ PR context. An unresolved required parameter or an unknown template
// variable is an error and the command is not run.
func Request(name string, cmd contract.Command, evt webhook.Event) (string, map[string]string, error) {
	params, err := AssembleParams(name, cmd, evt)
	if err != nil {
		return "", nil, err
	}

	ctx := templateContext(evt, params)
	out := synapseContext(evt)

	for key, tmpl := range cmd.Parameters {
		value, err := expand(tmpl, ctx)
		if err != nil {
			return "", nil, fmt.Errorf("parameter %q: %w", key, err)
		}
		out[key] = value
	}

	return cmd.Job, out, nil
}
