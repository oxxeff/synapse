// Package build turns a matched command and its event into a request for the
// executor: it assembles parameter values from their sources, expands the
// command's parameter templates and adds the PR context the executor needs.
package build

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"go.oxef.dev/ci/synapse/internal/contract"
	"go.oxef.dev/ci/synapse/internal/webhook"
)

// AssembleParams resolves the declared params of a command from their sources by
// priority: inline ChatOps arguments, then the synapse block in the PR body,
// then the declared default. A required param absent from every source is an
// error. Every declared param is present in the result (empty when an optional
// one is unset), so parameter templates can reference params.<name> freely.
func AssembleParams(name string, cmd contract.Command, evt webhook.Event) (map[string]string, error) {
	var inline map[string]string
	if evt.Kind == webhook.KindComment && cmd.OnComment != "" {
		inline = parseInlineArgs(evt.Comment, cmd.OnComment)
	}

	block, err := parsePRBlock(evt.PR.Body, name)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(cmd.Params))
	var missing []string

	for pname, decl := range cmd.Params {
		switch {
		case present(inline, pname):
			out[pname] = inline[pname]
		case present(block, pname):
			out[pname] = block[pname]
		case decl.Default != "":
			out[pname] = decl.Default
		case decl.Required:
			missing = append(missing, pname)
		default:
			out[pname] = ""
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required parameter(s): %s", strings.Join(missing, ", "))
	}

	return out, nil
}

func present(m map[string]string, key string) bool {
	_, ok := m[key]

	return ok
}

// parseInlineArgs reads shell-style arguments that follow the trigger key in a
// comment: "--key=value" sets key, a bare "--flag" sets it to "true". Tokens that
// are not flags are ignored. Quoting and embedded spaces are not supported.
func parseInlineArgs(comment, key string) map[string]string {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(comment), key))

	args := make(map[string]string)
	for _, tok := range strings.Fields(rest) {
		if !strings.HasPrefix(tok, "--") {
			continue
		}

		tok = strings.TrimPrefix(tok, "--")
		if k, v, ok := strings.Cut(tok, "="); ok {
			args[k] = v
		} else {
			args[tok] = "true"
		}
	}

	return args
}

// parsePRBlock extracts the fenced ```synapse block from the PR body and returns
// the parameter sub-map declared under the command name, or nil if there is no
// block or no entry for the command.
func parsePRBlock(body, name string) (map[string]string, error) {
	raw := synapseBlock(body)
	if raw == "" {
		return map[string]string{}, nil
	}

	var blocks map[string]map[string]string
	if err := yaml.Unmarshal([]byte(raw), &blocks); err != nil {
		return nil, fmt.Errorf("parse synapse parameter block: %w", err)
	}

	if block, ok := blocks[name]; ok {
		return block, nil
	}

	return map[string]string{}, nil
}

// synapseBlock returns the contents between the first ```synapse fence and the
// next closing ``` fence in body, or an empty string when absent.
func synapseBlock(body string) string {
	var buf []string

	inBlock := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)

		if !inBlock {
			if trimmed == "```synapse" {
				inBlock = true
			}

			continue
		}

		if trimmed == "```" {
			break
		}

		buf = append(buf, line)
	}

	return strings.Join(buf, "\n")
}
