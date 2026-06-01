// Package contract parses, validates and matches the .synapse.yaml declaration
// a repository ships to route its pull request events onto executor targets.
//
// It is executor-neutral: it names commands, their triggers and targets, but
// knows nothing about a concrete executor. Reading the file from the repository,
// ACL checks and parameter assembly belong to later phases - this package turns
// raw bytes into a validated model and reports which commands a given event
// triggers.
package contract

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Contract states and permission levels, named as in the .synapse.yaml schema.
const (
	stateOpen   = "pr_open"
	stateMerged = "pr_merged"

	permRead  = "read"
	permWrite = "write"
	permAdmin = "admin"
)

// Spec is a parsed and validated .synapse.yaml contract.
type Spec struct {
	Version  string             `yaml:"version"`
	Defaults Defaults           `yaml:"defaults"`
	Commands map[string]Command `yaml:"commands"`

	// AnnotatePR opts the repository in to having Synapse append a synapse
	// parameter-block template to a pull request description when it is opened.
	// It is a repository-wide behaviour, not a per-command default, so it lives at
	// the top level. Off by default: an existing repository is never edited
	// silently.
	AnnotatePR bool `yaml:"annotate_pr"`
}

// Defaults holds values applied to every command that does not set them.
type Defaults struct {
	AvailableIn   []string `yaml:"available_in"`
	MinPermission string   `yaml:"min_permission"`
	AllowedUsers  []string `yaml:"allowed_users"`
	AllowedTeams  []string `yaml:"allowed_teams"`
}

// Command declares one routable action: when it triggers, where it is allowed
// and which executor target it runs. After parsing, default-able fields are
// resolved, so AvailableIn and MinPermission are always populated.
type Command struct {
	Description   string            `yaml:"description"`
	OnComment     string            `yaml:"on_comment"`
	OnLabel       string            `yaml:"on_label"`
	OnMerge       *MergeTrigger     `yaml:"on_merge"`
	OnTag         string            `yaml:"on_tag"`
	AvailableIn   []string          `yaml:"available_in"`
	MinPermission string            `yaml:"min_permission"`
	AllowedUsers  []string          `yaml:"allowed_users"`
	AllowedTeams  []string          `yaml:"allowed_teams"`
	Job           string            `yaml:"job"`
	Params        map[string]Param  `yaml:"params"`
	Parameters    map[string]string `yaml:"parameters"`
	Ack           *Ack              `yaml:"ack"`
}

// prTriggered reports whether the command has a trigger tied to a pull request
// (as opposed to on_tag, which fires on a tag and carries no PR state).
func (c Command) prTriggered() bool {
	return c.OnComment != "" || c.OnLabel != "" || c.OnMerge != nil
}

// Param declares a command parameter and whether it must be supplied.
type Param struct {
	Required bool   `yaml:"required"`
	Default  string `yaml:"default"`
}

// Ack declares how receipt of a command is acknowledged in the pull request.
type Ack struct {
	Reaction string `yaml:"reaction"`
	Comment  bool   `yaml:"comment"`
}

// MergeTrigger is the on_merge field, a union of two YAML shapes: a bool (any
// merge) or a mapping {label: x} (merge only when label x was set on the PR).
type MergeTrigger struct {
	Enabled bool
	Label   string // empty means any merge
}

// UnmarshalYAML decodes on_merge from either a bool scalar or a {label: ...}
// mapping. The custom decode bypasses the decoder's strict unknown-key check, so
// the mapping keys are validated here: only label is allowed.
func (m *MergeTrigger) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return fmt.Errorf("on_merge must be a bool or a {label: ...} mapping: %w", err)
		}
		m.Enabled = enabled

		return nil

	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if key := node.Content[i].Value; key != "label" {
				return fmt.Errorf("on_merge has unknown key %q, only label is allowed", key)
			}
		}

		var raw struct {
			Label string `yaml:"label"`
		}
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("on_merge mapping: %w", err)
		}
		if raw.Label == "" {
			return errors.New("on_merge label must not be empty")
		}
		m.Enabled = true
		m.Label = raw.Label

		return nil

	default:
		return errors.New("on_merge must be a bool or a {label: ...} mapping")
	}
}
