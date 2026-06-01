package contract

import (
	"errors"
	"testing"
)

const validContract = `
version: "1"
defaults:
  available_in: [pr_open]
  min_permission: write
commands:
  run-load-tests:
    description: "Load tests before merge"
    on_comment: "/run-load-tests"
    job: "load-tests"
    params:
      suite:
        required: false
        default: "smoke"
    parameters:
      SUITE: "{{ params.suite }}"
      REPO: "{{ repo.full_name }}"
    ack:
      reaction: rocket
      comment: true
  sync-github:
    on_merge:
      label: "publish/github"
    available_in: [pr_merged]
    min_permission: admin
    job: "sync-github"
`

func TestParseValid(t *testing.T) {
	t.Parallel()

	spec, err := Parse([]byte(validContract))
	if err != nil {
		t.Fatalf("Parse: unexpected error %v", err)
	}

	if spec.Version != "1" {
		t.Errorf("version = %q, want 1", spec.Version)
	}
	if len(spec.Commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(spec.Commands))
	}

	// run-load-tests inherits defaults: available_in [pr_open], min_permission write.
	run := spec.Commands["run-load-tests"]
	if len(run.AvailableIn) != 1 || run.AvailableIn[0] != "pr_open" {
		t.Errorf("run available_in = %v, want [pr_open]", run.AvailableIn)
	}
	if run.MinPermission != "write" {
		t.Errorf("run min_permission = %q, want write", run.MinPermission)
	}

	sync := spec.Commands["sync-github"]
	if sync.OnMerge == nil || !sync.OnMerge.Enabled || sync.OnMerge.Label != "publish/github" {
		t.Errorf("sync on_merge = %+v, want enabled with label publish/github", sync.OnMerge)
	}
	if sync.MinPermission != "admin" {
		t.Errorf("sync min_permission = %q, want admin", sync.MinPermission)
	}
}

func TestParseDefaultsFallback(t *testing.T) {
	t.Parallel()

	// No defaults block: built-in defaults apply (pr_open, write).
	const c = `
version: "1"
commands:
  lint:
    on_comment: "/lint"
    job: "lint"
`
	spec, err := Parse([]byte(c))
	if err != nil {
		t.Fatalf("Parse: unexpected error %v", err)
	}

	lint := spec.Commands["lint"]
	if len(lint.AvailableIn) != 1 || lint.AvailableIn[0] != "pr_open" {
		t.Errorf("available_in = %v, want [pr_open]", lint.AvailableIn)
	}
	// min_permission is left unset when not specified; the write default is
	// resolved during ACL evaluation, not at parse time.
	if lint.MinPermission != "" {
		t.Errorf("min_permission = %q, want empty", lint.MinPermission)
	}
}

func TestParseUnsupportedVersion(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		``,
		"commands:\n  x:\n    on_comment: \"/x\"\n    job: j\n",
		"version: \"2\"\ncommands:\n  x:\n    on_comment: \"/x\"\n    job: j\n",
	} {
		_, err := Parse([]byte(body))

		var verErr *UnsupportedVersionError
		if !errors.As(err, &verErr) {
			t.Errorf("Parse(%q) error = %v, want *UnsupportedVersionError", body, err)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "unknown top-level key", body: `
version: "1"
bogus: true
commands:
  x:
    on_comment: "/x"
    job: j
`},
		{name: "unknown command key", body: `
version: "1"
commands:
  x:
    on_comment: "/x"
    job: j
    bogus: 1
`},
		{name: "no commands", body: `
version: "1"
commands: {}
`},
		{name: "command without trigger", body: `
version: "1"
commands:
  x:
    job: j
`},
		{name: "command without job", body: `
version: "1"
commands:
  x:
    on_comment: "/x"
`},
		{name: "bad command name", body: `
version: "1"
commands:
  Bad_Name:
    on_comment: "/x"
    job: j
`},
		{name: "bad available_in", body: `
version: "1"
commands:
  x:
    on_comment: "/x"
    job: j
    available_in: [pr_draft]
`},
		{name: "bad min_permission", body: `
version: "1"
commands:
  x:
    on_comment: "/x"
    job: j
    min_permission: owner
`},
		{name: "on_merge unknown key", body: `
version: "1"
commands:
  x:
    on_merge:
      labe: "typo"
    job: j
`},
		{name: "on_merge empty label", body: `
version: "1"
commands:
  x:
    on_merge:
      label: ""
    job: j
`},
		{name: "on_merge wrong type", body: `
version: "1"
commands:
  x:
    on_merge: [1, 2]
    job: j
`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Parse([]byte(tt.body)); err == nil {
				t.Fatalf("Parse(%s): want error, got nil", tt.name)
			}
		})
	}
}
