package router

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.oxef.dev/ci/synapse/internal/contract"
	"go.oxef.dev/ci/synapse/internal/executor"
)

// defaultWaiterWorkers bounds how many builds are awaited concurrently.
const defaultWaiterWorkers = 16

// Reporter posts the result comment for a finished or timed-out build.
type Reporter interface {
	CreateComment(ctx context.Context, owner, repo string, number int, body string) (int64, error)
}

// reportJob is one triggered build the pool waits on and reports.
type reportJob struct {
	owner, repo string
	number      int
	name        string
	run         executor.Run
	ack         *contract.Ack
	timeout     time.Duration
}

// waiterPool runs a bounded set of background report waiters. Jobs submitted when
// the pool is saturated or shutting down are dropped, deferring the report to the
// executor fallback - the same path as a timeout or restart.
type waiterPool struct {
	jobs    chan reportJob
	exec    executor.Executor
	gitea   Reporter
	poll    time.Duration
	workers int
	log     *slog.Logger
	wg      sync.WaitGroup
}

func newWaiterPool(exec executor.Executor, g Reporter, poll time.Duration, workers int, log *slog.Logger) *waiterPool {
	if workers <= 0 {
		workers = defaultWaiterWorkers
	}

	return &waiterPool{
		jobs:    make(chan reportJob, workers),
		exec:    exec,
		gitea:   g,
		poll:    poll,
		workers: workers,
		log:     log,
	}
}

// Run starts the workers and blocks until ctx is cancelled, then waits for the
// workers to drain. It is meant to run in its own goroutine for the service
// lifetime.
func (p *waiterPool) Run(ctx context.Context) {
	for range p.workers {
		p.wg.Add(1)
		go p.worker(ctx)
	}

	<-ctx.Done()
	p.wg.Wait()
}

func (p *waiterPool) worker(ctx context.Context) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.jobs:
			p.runJob(ctx, job)
		}
	}
}

// runJob isolates one job so a panic in waiting or reporting cannot kill the
// worker.
func (p *waiterPool) runJob(ctx context.Context, job reportJob) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("waiter job panic", "command", job.name, "panic", r)
		}
	}()

	p.wait(ctx, job)
}

// Submit hands a job to the pool without blocking. It returns false when the pool
// is full or shutting down; the build still runs and the executor fallback posts
// the final comment.
func (p *waiterPool) Submit(job reportJob) bool {
	select {
	case p.jobs <- job:
		return true
	default:
		p.log.Warn("waiter pool saturated; deferring report to executor fallback", "command", job.name)
		return false
	}
}

// wait polls the build until it finishes (posts a summary) or the timeout elapses
// (posts a handoff and lets the executor fallback report). Cancellation stops it
// silently - the fallback owns the report after a restart.
func (p *waiterPool) wait(ctx context.Context, job reportJob) {
	deadline := time.Now().Add(job.timeout)

	ticker := time.NewTicker(p.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := p.exec.Status(ctx, job.run)
			if err != nil {
				// Transient: keep polling until the deadline rather than abort.
				p.log.Warn("poll build status", "command", job.name, "error", err)
			} else if st.State == executor.StateDone {
				p.report(ctx, job, summaryText(job.name, st))
				return
			}

			if time.Now().After(deadline) {
				p.report(ctx, job, timeoutText(job.name))
				return
			}
		}
	}
}

// report posts body as the result comment, honouring the command's ack.comment.
func (p *waiterPool) report(ctx context.Context, job reportJob, body string) {
	if job.ack == nil || !job.ack.Comment {
		return
	}

	if _, err := p.gitea.CreateComment(ctx, job.owner, job.repo, job.number, body); err != nil {
		p.log.Warn("post report", "command", job.name, "error", err)
	}
}

func summaryText(name string, st executor.Status) string {
	outcome := resultText(st.Result)
	if st.URL != "" {
		return fmt.Sprintf("Synapse: command %q %s: %s", name, outcome, st.URL)
	}

	return fmt.Sprintf("Synapse: command %q %s", name, outcome)
}

func timeoutText(name string) string {
	return fmt.Sprintf("Synapse: command %q is taking longer than expected; CI will post the result", name)
}

func resultText(result executor.Result) string {
	switch result {
	case executor.ResultSuccess:
		return "succeeded"
	case executor.ResultUnstable:
		return "succeeded with warnings"
	case executor.ResultFailure:
		return "failed"
	case executor.ResultAborted:
		return "was aborted"
	default:
		return "finished"
	}
}
