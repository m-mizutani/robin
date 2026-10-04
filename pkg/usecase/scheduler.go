package usecase

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/utils/async"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
	"github.com/m-mizutani/robin/pkg/utils/logging"
)

// finishTimeout bounds the write of a run's result. It does not use the run's
// own deadline, so a run that timed out is still recorded.
const finishTimeout = 30 * time.Second

// JobRunner runs one run of a job. The agents under pkg/usecase/agents and
// any other runner implement it; pkg/cli maps each JobKind to one.
type JobRunner interface {
	// Run returns the result to record. A non-nil result may come with an
	// error, so that the money already spent is recorded with the failure.
	Run(ctx context.Context, req JobRunRequest) (*JobRunResult, error)
}

type JobRunRequest struct {
	Key         model.UserKey
	Job         model.Job
	RunID       model.JobRunID
	ScheduledAt time.Time
}

type JobRunResult struct {
	MessageTS string // first message posted; empty when none
	Spent     model.NanoUSD
}

type SchedulerConfig struct {
	MaxDelay    time.Duration // a run later than this is skipped
	RunTimeout  time.Duration // limit of one runner call
	RunTTL      time.Duration // lifetime of a run record
	Concurrency int           // runners running at once
	BatchSize   int           // entries read per ListDue
}

func (c SchedulerConfig) Validate() error {
	if c.MaxDelay <= 0 || c.RunTimeout <= 0 || c.RunTTL <= c.RunTimeout {
		return goerr.New("invalid scheduler durations",
			goerr.V("max_delay", c.MaxDelay), goerr.V("run_timeout", c.RunTimeout), goerr.V("run_ttl", c.RunTTL))
	}
	if c.Concurrency < 1 || c.BatchSize < 1 {
		return goerr.New("invalid scheduler limits", goerr.V("concurrency", c.Concurrency), goerr.V("batch_size", c.BatchSize))
	}
	return nil
}

// SchedulerReport counts what one RunDue did with the due jobs it found.
type SchedulerReport struct {
	Succeeded int
	Failed    int
	Skipped   int
	// NotClaimed counts due jobs another process claimed first.
	NotClaimed int
	// Errors counts due jobs that could not be read or claimed.
	Errors int
}

// Scheduler runs due jobs. It keeps no state between calls of RunDue, so the
// same Scheduler may serve a command and HTTP requests at once; the claim in
// the repository keeps a run from starting twice.
type Scheduler struct {
	repo    interfaces.Repository
	runners map[model.JobKind]JobRunner
	cfg     SchedulerConfig
	now     func() time.Time
}

func NewScheduler(repo interfaces.Repository, runners map[model.JobKind]JobRunner, cfg SchedulerConfig) (*Scheduler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	for kind, r := range runners {
		if r == nil {
			return nil, goerr.New("job runner is nil", goerr.V("kind", kind))
		}
	}
	return &Scheduler{repo: repo, runners: runners, cfg: cfg, now: time.Now}, nil
}

// dueRun is the state of one RunDue. It lives only in that call.
type dueRun struct {
	mu     sync.Mutex
	report SchedulerReport
	slots  chan struct{}
	runs   async.Group
}

func (d *dueRun) count(f func(r *SchedulerReport)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f(&d.report)
}

func (d *dueRun) snapshot() *SchedulerReport {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.report
	return &out
}

func (d *dueRun) release() { <-d.slots }

// RunDue claims and runs every due job once, waits for the runs it started,
// and returns the counts. It is the only entry point of the scheduler, and it
// does not depend on who calls it (a command now, an HTTP handler later).
// When ctx is cancelled it stops claiming; runs already claimed continue
// until they finish or reach RunTimeout. It returns an error, together with
// the counts so far, when it could not handle every due job: the due jobs
// could not be listed, or ctx ended first.
func (s *Scheduler) RunDue(ctx context.Context) (*SchedulerReport, error) {
	d := &dueRun{slots: make(chan struct{}, s.cfg.Concurrency)}
	// A job is handled once per call even when its entry stays due, such as
	// after a failed claim.
	seen := make(map[model.JobID]bool)
	err := s.claimDue(ctx, d, seen)
	d.runs.Wait()
	return d.snapshot(), err
}

// claimDue claims due jobs until none is left. It returns an error when it
// stopped before that: the due jobs could not be listed, or ctx ended.
func (s *Scheduler) claimDue(ctx context.Context, d *dueRun, seen map[model.JobID]bool) error {
	stopped := func() error {
		return goerr.Wrap(ctx.Err(), "stopped before every due job was handled")
	}
	for {
		if ctx.Err() != nil {
			return stopped()
		}
		// Entries handled earlier in this call can still be due (a failed
		// claim, a job that could not be read) and come first; reading that
		// many more keeps them from hiding the entries after them.
		entries, err := s.repo.Job().ListDue(ctx, s.now(), s.cfg.BatchSize+len(seen))
		if err != nil {
			return goerr.Wrap(err, "failed to list due jobs")
		}
		handled := false
		for _, e := range entries {
			if seen[e.JobID] {
				continue
			}
			seen[e.JobID] = true
			handled = true
			select {
			case d.slots <- struct{}{}:
			case <-ctx.Done():
				return stopped()
			}
			// select picks either case when both are ready.
			if ctx.Err() != nil {
				d.release()
				return stopped()
			}
			s.claim(ctx, e, d)
		}
		if !handled {
			return nil
		}
	}
}

func jobRunVals(key model.UserKey, jobID model.JobID, runID model.JobRunID) []goerr.Option {
	return []goerr.Option{
		goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID),
		goerr.V("job_id", jobID), goerr.V("run_id", runID),
	}
}

// claim takes the slot d holds for e and gives it back when the job is not
// run, or when its run ends.
func (s *Scheduler) claim(ctx context.Context, e *model.JobScheduleEntry, d *dueRun) {
	key := e.Key()
	fail := func(err error) {
		d.release()
		d.count(func(r *SchedulerReport) { r.Errors++ })
		errutil.Handle(ctx, err, "due job was not handled")
	}

	job, err := s.repo.Job().Get(ctx, key, e.JobID)
	if err != nil {
		fail(goerr.Wrap(err, "failed to read due job", jobRunVals(key, e.JobID, "")...))
		return
	}
	now := s.now()
	scheduledAt := job.NextRunAt
	runID := model.NewJobRunID(scheduledAt)
	vals := jobRunVals(key, job.ID, runID)
	// Another process claimed the job after the entry was read and moved
	// it to a later run.
	if scheduledAt.After(now) {
		d.release()
		d.count(func(r *SchedulerReport) { r.NotClaimed++ })
		return
	}
	next, err := job.Schedule.Next(now)
	if err != nil {
		fail(goerr.Wrap(err, "failed to compute the next run", vals...))
		return
	}

	run := &model.JobRun{
		TeamID:      key.TeamID,
		UserID:      key.UserID,
		JobID:       job.ID,
		ID:          runID,
		Kind:        job.Kind,
		ScheduledAt: scheduledAt,
		StartedAt:   now,
		ExpiresAt:   now.Add(s.cfg.RunTTL),
	}
	runner, hasRunner := s.runners[job.Kind]
	switch {
	case now.Sub(scheduledAt) > s.cfg.MaxDelay:
		run.Status = model.JobRunSkipped
		run.FinishedAt = now
	case !hasRunner:
		run.Status = model.JobRunFailed
		run.Failure = model.JobRunNoRunner
		run.FinishedAt = now
	default:
		run.Status = model.JobRunRunning
		run.Deadline = now.Add(s.cfg.RunTimeout)
	}

	claimed, err := s.repo.Job().Claim(ctx, key, model.JobClaimRequest{
		JobID:       job.ID,
		ScheduledAt: scheduledAt,
		NextRunAt:   next,
		Run:         run,
		Now:         now,
	})
	if err != nil {
		fail(goerr.Wrap(err, "failed to claim due job", vals...))
		return
	}
	if !claimed {
		d.release()
		d.count(func(r *SchedulerReport) { r.NotClaimed++ })
		return
	}

	if run.Status != model.JobRunRunning {
		d.release()
		s.recordResult(ctx, run, d)
		if run.Failure == model.JobRunNoRunner {
			errutil.Handle(ctx, goerr.New("no runner for the job kind", append(vals, goerr.V("kind", job.Kind))...),
				"job run failed")
		}
		return
	}

	logging.From(ctx).Info("job run started",
		slog.String("job_id", string(job.ID)), slog.String("run_id", string(runID)),
		slog.String("kind", string(job.Kind)), slog.Time("scheduled_at", scheduledAt))
	d.runs.Go(ctx, func(ctx context.Context) error {
		defer d.release()
		return s.execute(ctx, key, job, run, runner, d)
	})
}

// execute calls the runner and records its result. ctx is not cancelled with
// the caller of RunDue; the run ends at RunTimeout.
func (s *Scheduler) execute(ctx context.Context, key model.UserKey, job *model.Job, run *model.JobRun, runner JobRunner, d *dueRun) error {
	rctx, cancel := context.WithTimeout(ctx, s.cfg.RunTimeout)
	defer cancel()

	res, err := runner.Run(rctx, JobRunRequest{Key: key, Job: *job, RunID: run.ID, ScheduledAt: run.ScheduledAt})
	run.FinishedAt = s.now()
	if res != nil {
		run.MessageTS = res.MessageTS
		run.Spent = res.Spent
	}
	switch {
	case err == nil:
		run.Status = model.JobRunSucceeded
	case errors.Is(rctx.Err(), context.DeadlineExceeded):
		run.Status = model.JobRunFailed
		run.Failure = model.JobRunTimedOut
	default:
		run.Status = model.JobRunFailed
		run.Failure = model.JobRunRunFailed
	}

	fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer fcancel()
	if ferr := s.repo.Job().Finish(fctx, key, run); ferr != nil {
		errutil.Handle(ctx, goerr.Wrap(ferr, "failed to record the job run", jobRunVals(key, job.ID, run.ID)...),
			"job run result was not saved")
	}
	s.recordResult(ctx, run, d)

	if err != nil {
		return goerr.Wrap(err, "job run failed", append(jobRunVals(key, job.ID, run.ID), goerr.V("failure", run.Failure))...)
	}
	return nil
}

func (s *Scheduler) recordResult(ctx context.Context, run *model.JobRun, d *dueRun) {
	d.count(func(r *SchedulerReport) {
		switch run.Status {
		case model.JobRunSucceeded:
			r.Succeeded++
		case model.JobRunSkipped:
			r.Skipped++
		default:
			r.Failed++
		}
	})
	logging.From(ctx).Info("job run finished",
		slog.String("job_id", string(run.JobID)), slog.String("run_id", string(run.ID)),
		slog.String("kind", string(run.Kind)), slog.String("status", string(run.Status)),
		slog.String("failure", string(run.Failure)), slog.Int64("spent_nano_usd", int64(run.Spent)))
}
