package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/utils/async"
	"github.com/m-mizutani/robin/pkg/utils/errutil"
	"github.com/m-mizutani/robin/pkg/utils/logging"
)

// Job is a unit of work the scheduler starts. Agents and other tasks
// implement it; pkg/cli maps each JobName to one.
type Job interface {
	// MaxDelay is how late a run may start; a later run is skipped. Zero
	// runs a late run however late it is.
	MaxDelay() time.Duration
	Run(ctx context.Context, req JobRequest) error
}

// JobRequest is one run of a job for one user.
type JobRequest struct {
	Key         model.UserKey
	TriggerID   model.JobTriggerID
	ChannelID   string
	TimeZone    string
	ScheduledAt time.Time
}

type SchedulerConfig struct {
	RunTimeout  time.Duration // limit of one run
	Concurrency int           // runs at the same time
}

func (c SchedulerConfig) Validate() error {
	if c.RunTimeout <= 0 || c.Concurrency < 1 {
		return goerr.New("invalid scheduler config",
			goerr.V("run_timeout", c.RunTimeout), goerr.V("concurrency", c.Concurrency))
	}
	return nil
}

// Scheduler runs due triggers. It keeps no state between calls of RunDue, so
// the same Scheduler may serve a command and HTTP requests at once; the claim
// keeps a run from starting twice.
type Scheduler struct {
	repo interfaces.Repository
	jobs map[model.JobName]Job
	cfg  SchedulerConfig
	now  func() time.Time
}

func NewScheduler(repo interfaces.Repository, jobs map[model.JobName]Job, cfg SchedulerConfig) (*Scheduler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	for name, j := range jobs {
		if j == nil {
			return nil, goerr.New("job is nil", goerr.V("job", name))
		}
	}
	return &Scheduler{repo: repo, jobs: jobs, cfg: cfg, now: time.Now}, nil
}

// RunDue claims and runs every due trigger once, waits for the runs it
// started, and does not depend on who calls it (a command now, an HTTP
// handler later). When ctx is cancelled it stops claiming; runs already
// claimed continue until they finish or reach RunTimeout. It returns an error
// when it could not handle every due trigger: the due triggers could not be
// listed, one of them could not be claimed, or ctx ended first.
func (s *Scheduler) RunDue(ctx context.Context) error {
	entries, err := s.repo.JobSetting().ListDue(ctx, s.now())
	if err != nil {
		return goerr.Wrap(err, "failed to list due job triggers")
	}

	slots := make(chan struct{}, s.cfg.Concurrency)
	var runs async.Group
	defer runs.Wait()
	var claimErrs []error
	stopped := func(errs ...error) error {
		return goerr.Wrap(errors.Join(append(append([]error{ctx.Err()}, errs...), claimErrs...)...),
			"stopped before every due job trigger was handled")
	}
	for _, e := range entries {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return stopped()
		}
		// select picks either case when both are ready.
		if ctx.Err() != nil {
			<-slots
			return stopped()
		}
		run, job, err := s.claim(ctx, e)
		if err != nil {
			<-slots
			if ctx.Err() != nil {
				return stopped(err)
			}
			// One trigger that cannot be claimed does not hold back the others.
			claimErrs = append(claimErrs, err)
			continue
		}
		if run == nil {
			<-slots
			continue
		}
		runs.Go(ctx, func(ctx context.Context) error {
			defer func() { <-slots }()
			return s.execute(ctx, job, run)
		})
	}
	if len(claimErrs) > 0 {
		return goerr.Wrap(errors.Join(claimErrs...), "failed to claim due job triggers", goerr.V("failed", len(claimErrs)))
	}
	return nil
}

func triggerVals(key model.UserKey, id model.JobTriggerID) []goerr.Option {
	return []goerr.Option{goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID), goerr.V("trigger_id", id)}
}

// claim moves the trigger of e to its next time, when no other process did,
// and returns the run to start. It returns no run and no error when there is
// nothing to run: another process claimed the trigger, the trigger is gone,
// the job is not defined, or the run is later than the job allows.
func (s *Scheduler) claim(ctx context.Context, e *model.JobScheduleEntry) (*JobRequest, Job, error) {
	key := e.Key()
	now := s.now()
	var run *JobRequest
	var jobName model.JobName
	err := s.repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
		run = nil
		if cur == nil {
			return nil, nil
		}
		t := cur.Trigger(e.TriggerID)
		if t == nil || !t.NextRunAt.Equal(e.NextRunAt) {
			return nil, nil
		}
		loc, err := cur.Location()
		if err != nil {
			return nil, err
		}
		run = &JobRequest{Key: key, TriggerID: t.ID, ChannelID: cur.ChannelID, TimeZone: cur.TimeZone, ScheduledAt: t.NextRunAt}
		jobName = t.Job
		t.NextRunAt = t.Time.Next(now, loc)
		return cur, nil
	})
	if err != nil {
		return nil, nil, goerr.Wrap(err, "failed to claim job trigger", triggerVals(key, e.TriggerID)...)
	}
	if run == nil {
		return nil, nil, nil
	}

	log := logging.From(ctx).With(slog.String("trigger_id", string(run.TriggerID)), slog.String("job", string(jobName)),
		slog.Time("scheduled_at", run.ScheduledAt))
	job, ok := s.jobs[jobName]
	if !ok {
		errutil.Handle(ctx, goerr.New("job is not defined in this build", append(triggerVals(key, run.TriggerID), goerr.V("job", jobName))...),
			"job trigger was not run")
		return nil, nil, nil
	}
	if late := now.Sub(run.ScheduledAt); job.MaxDelay() > 0 && late > job.MaxDelay() {
		log.Info("job run skipped", slog.Duration("late", late), slog.Duration("max_delay", job.MaxDelay()))
		return nil, nil, nil
	}
	log.Info("job run started")
	return run, job, nil
}

// execute runs the job within RunTimeout. ctx is not cancelled with the
// caller of RunDue.
func (s *Scheduler) execute(ctx context.Context, job Job, run *JobRequest) error {
	rctx, cancel := context.WithTimeout(ctx, s.cfg.RunTimeout)
	defer cancel()
	if err := job.Run(rctx, *run); err != nil {
		return goerr.Wrap(err, "job run failed", triggerVals(run.Key, run.TriggerID)...)
	}
	logging.From(ctx).Info("job run finished", slog.String("trigger_id", string(run.TriggerID)))
	return nil
}
