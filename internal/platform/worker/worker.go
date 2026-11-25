// Package worker runs background jobs on a fixed interval.
package worker

import (
	"context"
	"log/slog"
	"time"
)

// Job is one unit of background work. Run should process whatever is pending
// and return; it is invoked again on the next tick.
type Job interface {
	Name() string
	Run(ctx context.Context) error
}

// Runner executes its jobs sequentially, every interval, until the context ends.
//
// Only ONE runner should be active per database: jobs are idempotent, so two
// runners would not corrupt data, but they would duplicate work. Multi-instance
// deployments would add a lease row (or move to a real queue).
type Runner struct {
	interval time.Duration
	jobs     []Job
	log      *slog.Logger
}

func NewRunner(interval time.Duration, log *slog.Logger, jobs ...Job) *Runner {
	return &Runner{interval: interval, jobs: jobs, log: log}
}

// Start blocks until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) {
	r.log.Info("worker started", "interval", r.interval.String(), "jobs", len(r.jobs))
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		r.RunOnce(ctx)
		select {
		case <-ctx.Done():
			r.log.Info("worker stopped")
			return
		case <-t.C:
		}
	}
}

// RunOnce runs every job once, in order. Errors are logged, not returned, so a
// failing job never starves the others.
func (r *Runner) RunOnce(ctx context.Context) {
	for _, j := range r.jobs {
		if ctx.Err() != nil {
			return
		}
		if err := j.Run(ctx); err != nil {
			r.log.ErrorContext(ctx, "worker job failed", "job", j.Name(), "error", err.Error())
		}
	}
}
