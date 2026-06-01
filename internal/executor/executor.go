// Package executor is the executor-neutral contract for running a target.
//
// The router triggers a target and polls its status through this interface and
// never knows the concrete executor. Jenkins is the current and only
// implementation (package jenkins); the contract stays neutral so another
// executor can be added without touching the router or the .synapse.yaml schema.
package executor

import "context"

// State is the coarse lifecycle of a run.
type State string

// Run lifecycle states. Pending covers time spent queued before execution.
const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateDone    State = "done"
)

// Result is the outcome of a finished run; empty until State is StateDone.
type Result string

// Run outcomes, mapped from the executor's native result.
const (
	ResultSuccess  Result = "success"
	ResultUnstable Result = "unstable"
	ResultFailure  Result = "failure"
	ResultAborted  Result = "aborted"
)

// Run is the descriptor returned by Trigger and passed back to Status. ID is the
// opaque handle the executor polls by; URL links the run for a PR comment and may
// be empty until execution starts.
type Run struct {
	ID  string
	URL string
}

// Status is a point-in-time view of a run.
type Status struct {
	State  State
	Result Result
	URL    string
}

// Executor runs a target and reports its status. Trigger starts target with the
// given parameters and returns a descriptor; Status reports the current state of
// a previously triggered run.
type Executor interface {
	Trigger(ctx context.Context, target string, params map[string]string) (Run, error)
	Status(ctx context.Context, run Run) (Status, error)
}
