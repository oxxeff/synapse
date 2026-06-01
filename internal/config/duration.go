package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from a YAML string such as "10m".
// Durations are written as human-readable strings in config to keep the file
// self-explanatory; the Go type stays internal and may change freely.
type Duration time.Duration

// Std returns the value as a standard time.Duration.
func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

// String renders the underlying duration, e.g. "10m0s".
func (d Duration) String() string {
	return time.Duration(d).String()
}

// UnmarshalYAML decodes a duration from a YAML scalar string. A non-string node
// or an unparseable value is rejected with a message naming the offending input.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"10m\": %w", err)
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}

	*d = Duration(parsed)
	return nil
}
