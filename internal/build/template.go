package build

import (
	"fmt"
	"regexp"
	"strconv"

	"go.oxef.dev/ci/synapse/internal/webhook"
)

// templateVar matches a {{ key }} placeholder, capturing the dotted variable
// name. The contract uses bare names without a leading dot (for example
// {{ params.suite }}), which standard text/template cannot parse, so expansion
// is done by direct substitution.
var templateVar = regexp.MustCompile(`\{\{\s*([\w.]+)\s*\}\}`)

// expand replaces every {{ key }} in tmpl with its value from ctx. An unknown
// variable is an error so a typo surfaces instead of expanding to nothing.
func expand(tmpl string, ctx map[string]string) (string, error) {
	var unknown string

	out := templateVar.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := templateVar.FindStringSubmatch(match)[1]
		if v, ok := ctx[key]; ok {
			return v
		}
		if unknown == "" {
			unknown = key
		}

		return match
	})

	if unknown != "" {
		return "", fmt.Errorf("unknown template variable %q", unknown)
	}

	return out, nil
}

// templateContext is the variable set available to parameter templates: repo,
// pr and sender fields plus the assembled params under params.<name>.
func templateContext(evt webhook.Event, params map[string]string) map[string]string {
	ctx := map[string]string{
		"repo.full_name":  evt.Repo.FullName,
		"repo.name":       evt.Repo.Name,
		"repo.owner":      evt.Repo.Owner,
		"pr.number":       strconv.Itoa(evt.PR.Number),
		"pr.title":        evt.PR.Title,
		"pr.head_ref":     evt.PR.HeadRef,
		"pr.base_ref":     evt.PR.BaseRef,
		"pr.merge_commit": evt.PR.MergeCommit,
		"sender.login":    evt.Sender,
	}

	for name, value := range params {
		ctx["params."+name] = value
	}

	return ctx
}

// synapseContext is the SYNAPSE_-prefixed PR context always passed to the
// executor, so a job (and its fallback reporting) knows which PR it serves. The
// prefix separates this service context from the command's own parameters.
func synapseContext(evt webhook.Event) map[string]string {
	ctx := map[string]string{
		"SYNAPSE_REPO":    evt.Repo.FullName,
		"SYNAPSE_PR":      strconv.Itoa(evt.PR.Number),
		"SYNAPSE_PR_HEAD": evt.PR.HeadRef,
		"SYNAPSE_PR_BASE": evt.PR.BaseRef,
		"SYNAPSE_EVENT":   string(evt.Kind),
	}

	if evt.Kind == webhook.KindComment {
		ctx["SYNAPSE_COMMENT_ID"] = strconv.FormatInt(evt.CommentID, 10)
	}

	return ctx
}
