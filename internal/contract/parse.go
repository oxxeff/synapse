package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"

	"gopkg.in/yaml.v3"
)

// commandNameRe constrains command keys: lowercase letters, digits and hyphens,
// 1 to 40 characters. It keeps command names usable as identifiers downstream.
var commandNameRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// Parse decodes a .synapse.yaml contract, resolves defaults and validates it. An
// unknown key at any level, an unsupported version or any schema violation is an
// error; a partially valid file is rejected whole rather than applied in part.
func Parse(data []byte) (*Spec, error) {
	var spec Spec

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	// An empty file decodes to io.EOF; that is a missing version, not a parse
	// failure, so it falls through to validation which reports it precisely.
	if err := dec.Decode(&spec); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse contract: %w", err)
	}

	spec.applyDefaults()

	if err := spec.validate(); err != nil {
		return nil, err
	}

	return &spec, nil
}

// applyDefaults fills command fields left unset from defaults, then from the
// built-in defaults (available_in pr_open, min_permission write), so the model
// carries resolved values.
func (s *Spec) applyDefaults() {
	for name, c := range s.Commands {
		// available_in is a pull request state filter; a tag has no PR state, so a
		// command triggered only by a tag is left without it (matching skips the
		// filter for tag events). Default it only when a PR trigger is present.
		if c.prTriggered() {
			if len(c.AvailableIn) == 0 {
				c.AvailableIn = s.Defaults.AvailableIn
			}
			if len(c.AvailableIn) == 0 {
				c.AvailableIn = []string{stateOpen}
			}
		}

		// min_permission is not defaulted to write here: the contract applies that
		// default only when no ACL field is set at all, which is decided when ACL
		// is evaluated. Parsing only inherits an explicit defaults value.
		if c.MinPermission == "" {
			c.MinPermission = s.Defaults.MinPermission
		}

		if c.AllowedUsers == nil {
			c.AllowedUsers = s.Defaults.AllowedUsers
		}
		if c.AllowedTeams == nil {
			c.AllowedTeams = s.Defaults.AllowedTeams
		}

		s.Commands[name] = c
	}
}

func (s *Spec) validate() error {
	if s.Version != SchemaVersion {
		return &UnsupportedVersionError{Version: s.Version}
	}

	if len(s.Commands) == 0 {
		return errors.New("commands must declare at least one command")
	}

	if err := validateAvailableIn(s.Defaults.AvailableIn, "defaults"); err != nil {
		return err
	}
	if s.Defaults.MinPermission != "" && !validPermission(s.Defaults.MinPermission) {
		return fmt.Errorf("defaults: min_permission %q must be one of read, write, admin", s.Defaults.MinPermission)
	}

	for name, c := range s.Commands {
		if !commandNameRe.MatchString(name) {
			return fmt.Errorf("command name %q must match [a-z0-9-]{1,40}", name)
		}
		if c.Job == "" {
			return fmt.Errorf("command %q: job must not be empty", name)
		}
		if !c.prTriggered() && c.OnTag == "" {
			return fmt.Errorf("command %q: must declare at least one of on_comment, on_label, on_merge, on_tag", name)
		}
		if c.OnTag != "" {
			if _, err := path.Match(c.OnTag, ""); err != nil {
				return fmt.Errorf("command %q: on_tag %q is not a valid pattern: %w", name, c.OnTag, err)
			}
		}
		if err := validateAvailableIn(c.AvailableIn, "command "+name); err != nil {
			return err
		}
		if c.MinPermission != "" && !validPermission(c.MinPermission) {
			return fmt.Errorf("command %q: min_permission %q must be one of read, write, admin", name, c.MinPermission)
		}
	}

	return nil
}

func validateAvailableIn(states []string, where string) error {
	for _, st := range states {
		if st != stateOpen && st != stateMerged {
			return fmt.Errorf("%s: available_in %q must be one of pr_open, pr_merged", where, st)
		}
	}

	return nil
}

func validPermission(p string) bool {
	switch p {
	case permRead, permWrite, permAdmin:
		return true
	default:
		return false
	}
}
