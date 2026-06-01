package contract

import "fmt"

// SchemaVersion is the only .synapse.yaml contract version this revision accepts.
const SchemaVersion = "1"

// UnsupportedVersionError reports a contract whose version field is missing or
// not supported. It is distinct from other schema errors so the runtime can tell
// the author which version is supported.
type UnsupportedVersionError struct {
	Version string
}

func (e *UnsupportedVersionError) Error() string {
	if e.Version == "" {
		return fmt.Sprintf("missing version, only %q is supported", SchemaVersion)
	}

	return fmt.Sprintf("unsupported version %q, only %q is supported", e.Version, SchemaVersion)
}
