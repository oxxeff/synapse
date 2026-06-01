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

// HasSynapseBlock reports whether body already contains a fenced synapse block.
// It is used to keep description annotation idempotent: a body that already has a
// block is left untouched.
func HasSynapseBlock(body string) bool {
	return synapseBlock(body) != ""
}

// AnnotationBlock renders the fenced synapse block that seeds a pull request
// description: one entry per command that has configurable params, each param on
// its own line with its declared default (empty when none). It returns an empty
// string when no command exposes a param, so the caller can skip annotating.
//
// The output is the same shape parsePRBlock reads back, so an author edits values
// in place. Commands and params are emitted in sorted order for a stable result.
func AnnotationBlock(spec *contract.Spec) string {
	names := make([]string, 0, len(spec.Commands))
	for name, cmd := range spec.Commands {
		if len(cmd.Params) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("```synapse\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%s:\n", name)

		params := spec.Commands[name].Params
		keys := make([]string, 0, len(params))
		for p := range params {
			keys = append(keys, p)
		}
		sort.Strings(keys)

		for _, p := range keys {
			fmt.Fprintf(&b, "  %s: %q\n", p, params[p].Default)
		}
	}
	b.WriteString("```")

	return b.String()
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
