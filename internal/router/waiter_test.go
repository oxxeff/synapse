package router

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.oxef.dev/ci/synapse/internal/contract"
	"go.oxef.dev/ci/synapse/internal/executor"
)

func testPool(exec executor.Executor, g Reporter) *waiterPool {
	return newWaiterPool(exec, g, time.Millisecond, 2, discardLog())
}

func TestWaitSummary(t *testing.T) {
	t.Parallel()

	g := &fakeGitea{}
	exec := &fakeExecutor{status: executor.Status{State: executor.StateDone, Result: executor.ResultSuccess, URL: "http://build/1"}}
	p := testPool(exec, g)

	p.wait(context.Background(), reportJob{name: "run", ack: &contract.Ack{Comment: true}, timeout: time.Minute})

	if len(g.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(g.comments))
	}
	if !strings.Contains(g.comments[0], "succeeded") || !strings.Contains(g.comments[0], "http://build/1") {
		t.Errorf("summary = %q, want outcome and build URL", g.comments[0])
	}
}

func TestWaitNoCommentWhenAckOff(t *testing.T) {
	t.Parallel()

	g := &fakeGitea{}
	exec := &fakeExecutor{status: executor.Status{State: executor.StateDone, Result: executor.ResultSuccess}}
	p := testPool(exec, g)

	// ack.comment false: no summary is posted.
	p.wait(context.Background(), reportJob{name: "run", ack: &contract.Ack{Comment: false}, timeout: time.Minute})

	if len(g.comments) != 0 {
		t.Errorf("comments = %v, want none", g.comments)
	}
}

func TestWaitTimeout(t *testing.T) {
	t.Parallel()

	g := &fakeGitea{}
	exec := &fakeExecutor{status: executor.Status{State: executor.StateRunning}}
	p := testPool(exec, g)

	p.wait(context.Background(), reportJob{name: "run", ack: &contract.Ack{Comment: true}, timeout: 5 * time.Millisecond})

	if len(g.comments) != 1 || !strings.Contains(g.comments[0], "longer than expected") {
		t.Errorf("comments = %v, want one timeout handoff", g.comments)
	}
}

func TestWaitCancelled(t *testing.T) {
	t.Parallel()

	g := &fakeGitea{}
	exec := &fakeExecutor{status: executor.Status{State: executor.StateRunning}}
	p := testPool(exec, g)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.wait(ctx, reportJob{name: "run", ack: &contract.Ack{Comment: true}, timeout: time.Hour})
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not return after cancellation")
	}
	if len(g.comments) != 0 {
		t.Errorf("comments = %v, want none on cancellation", g.comments)
	}
}

func TestRunShutdown(t *testing.T) {
	t.Parallel()

	g := &fakeGitea{}
	exec := &fakeExecutor{status: executor.Status{State: executor.StateRunning}}
	p := testPool(exec, g)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	p.Submit(reportJob{name: "x", ack: &contract.Ack{Comment: true}, timeout: time.Hour})
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}
}

func TestSubmitSaturated(t *testing.T) {
	t.Parallel()

	g := &fakeGitea{}
	exec := &fakeExecutor{status: executor.Status{State: executor.StateRunning}}
	p := newWaiterPool(exec, g, time.Millisecond, 2, discardLog()) // buffer 2, not running

	if !p.Submit(reportJob{name: "a"}) || !p.Submit(reportJob{name: "b"}) {
		t.Fatal("first two submits should succeed")
	}
	if p.Submit(reportJob{name: "c"}) {
		t.Error("submit into a full pool should return false")
	}
}
