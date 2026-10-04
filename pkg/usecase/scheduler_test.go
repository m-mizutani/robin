package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/utils/async"
)

var (
	// 09:00 in Tokyo on 2026-10-05.
	scheduledAt = time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	nextDay     = scheduledAt.Add(24 * time.Hour)
)

// fakeRunner records every request. Before returning it waits for release
// when release is set, or until the context ends when waitCtx is set.
type fakeRunner struct {
	mu       sync.Mutex
	requests []usecase.JobRunRequest
	ctxErrs  []error // ctx.Err() of each call when it returned
	result   *usecase.JobRunResult
	err      error
	release  chan struct{}
	started  chan struct{}
	waitCtx  bool
	running  atomic.Int32
	maxSeen  atomic.Int32
}

func (r *fakeRunner) Run(ctx context.Context, req usecase.JobRunRequest) (*usecase.JobRunResult, error) {
	n := r.running.Add(1)
	defer r.running.Add(-1)
	for {
		m := r.maxSeen.Load()
		if n <= m || r.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	if r.started != nil {
		r.started <- struct{}{}
	}
	if r.release != nil {
		<-r.release
	}
	if r.waitCtx {
		<-ctx.Done()
		r.record(ctx)
		return &usecase.JobRunResult{Spent: 7}, ctx.Err()
	}
	time.Sleep(5 * time.Millisecond)
	r.record(ctx)
	return r.result, r.err
}

func (r *fakeRunner) record(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
}

func (r *fakeRunner) calls() []usecase.JobRunRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]usecase.JobRunRequest(nil), r.requests...)
}

// jobHookRepo changes the answers of the job repository and records the
// results the scheduler saved.
type jobHookRepo struct {
	interfaces.Repository
	jobs *jobHooks
}

func (r *jobHookRepo) Job() interfaces.JobRepository { return r.jobs }

type jobHooks struct {
	interfaces.JobRepository
	mu       sync.Mutex
	finished []model.JobRun
	// extra entries come first in every ListDue answer, as entries of
	// earlier runs would, within the limit.
	extra     []*model.JobScheduleEntry
	listErr   error
	finishErr error
}

func (h *jobHooks) ListDue(ctx context.Context, now time.Time, limit int) ([]*model.JobScheduleEntry, error) {
	if h.listErr != nil {
		return nil, h.listErr
	}
	entries, err := h.JobRepository.ListDue(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	out := append(append([]*model.JobScheduleEntry(nil), h.extra...), entries...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (h *jobHooks) Finish(ctx context.Context, key model.UserKey, run *model.JobRun) error {
	h.mu.Lock()
	h.finished = append(h.finished, *run)
	h.mu.Unlock()
	if h.finishErr != nil {
		return h.finishErr
	}
	return h.JobRepository.Finish(ctx, key, run)
}

func (h *jobHooks) finishedRuns() []model.JobRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]model.JobRun(nil), h.finished...)
}

type schedulerFixture struct {
	mem    *memory.Memory
	repo   *jobHookRepo
	runner *fakeRunner
	cfg    usecase.SchedulerConfig
	owners int
}

func newSchedulerFixture(t *testing.T) *schedulerFixture {
	t.Helper()
	mem := memory.New()
	return &schedulerFixture{
		mem:    mem,
		repo:   &jobHookRepo{Repository: mem, jobs: &jobHooks{JobRepository: mem.Job()}},
		runner: &fakeRunner{result: &usecase.JobRunResult{MessageTS: "1900000000.000001", Spent: 1234}},
		cfg: usecase.SchedulerConfig{
			MaxDelay:    time.Hour,
			RunTimeout:  5 * time.Second,
			RunTTL:      720 * time.Hour,
			Concurrency: 4,
			BatchSize:   100,
		},
	}
}

// addJob stores a hello job of a new user due at nextRunAt.
func (f *schedulerFixture) addJob(t *testing.T, nextRunAt time.Time) *model.Job {
	t.Helper()
	f.owners++
	job := &model.Job{
		TeamID:      "T0123",
		UserID:      model.SlackUserID(fmt.Sprintf("U%07d", f.owners)),
		ID:          model.JobID(fmt.Sprintf("00000000-0000-4000-8000-%012d", f.owners)),
		Kind:        model.JobKindHello,
		ChannelID:   "C0GENERAL",
		ChannelName: "general",
		Schedule:    model.DailySchedule{Hour: 9, Minute: 0, TimeZone: "Asia/Tokyo"},
		NextRunAt:   nextRunAt,
		CreatedAt:   nextRunAt.Add(-48 * time.Hour),
		UpdatedAt:   nextRunAt.Add(-48 * time.Hour),
	}
	gt.NoError(t, f.mem.Job().Create(context.Background(), job.Key(), job, 10)).Required()
	return job
}

func (f *schedulerFixture) scheduler(t *testing.T, now time.Time) *usecase.Scheduler {
	t.Helper()
	s, err := usecase.NewScheduler(f.repo, map[model.JobKind]usecase.JobRunner{model.JobKindHello: f.runner}, f.cfg)
	gt.NoError(t, err).Required()
	s.SetNowForTest(func() time.Time { return now })
	return s
}

func (f *schedulerFixture) stored(t *testing.T, job *model.Job) *model.Job {
	t.Helper()
	got, err := f.mem.Job().Get(context.Background(), job.Key(), job.ID)
	gt.NoError(t, err).Required()
	return got
}

func TestScheduler_RunsADueJob(t *testing.T) {
	f := newSchedulerFixture(t)
	job := f.addJob(t, scheduledAt)
	now := scheduledAt.Add(3 * time.Minute)

	report, err := f.scheduler(t, now).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{Succeeded: 1})

	calls := f.runner.calls()
	gt.A(t, calls).Length(1).Required()
	gt.Value(t, calls[0].Key).Equal(job.Key())
	gt.Value(t, calls[0].Job.ID).Equal(job.ID)
	gt.Value(t, calls[0].Job.ChannelID).Equal("C0GENERAL")
	gt.True(t, calls[0].ScheduledAt.Equal(scheduledAt))
	gt.Value(t, calls[0].RunID).Equal(model.JobRunID("20261005T000000Z"))

	runs := f.repo.jobs.finishedRuns()
	gt.A(t, runs).Length(1).Required()
	gt.Value(t, runs[0].Status).Equal(model.JobRunSucceeded)
	gt.Value(t, runs[0].Failure).Equal(model.JobRunNoFailure)
	gt.String(t, runs[0].MessageTS).Equal("1900000000.000001")
	gt.Value(t, runs[0].Spent).Equal(model.NanoUSD(1234))
	gt.True(t, runs[0].StartedAt.Equal(now))
	gt.True(t, runs[0].Deadline.Equal(now.Add(5*time.Second)))
	gt.True(t, runs[0].ExpiresAt.Equal(now.Add(720*time.Hour)))

	got := f.stored(t, job)
	gt.True(t, got.NextRunAt.Equal(nextDay))
	gt.Value(t, got.LastRun.Status).Equal(model.JobRunSucceeded)
	gt.Value(t, got.LastRun.RunID).Equal(model.JobRunID("20261005T000000Z"))
}

func TestScheduler_LeavesAJobThatIsNotDue(t *testing.T) {
	f := newSchedulerFixture(t)
	job := f.addJob(t, scheduledAt)

	report, err := f.scheduler(t, scheduledAt.Add(-time.Minute)).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{})
	gt.A(t, f.runner.calls()).Length(0)
	got := f.stored(t, job)
	gt.True(t, got.NextRunAt.Equal(scheduledAt))
	gt.Value(t, got.LastRun).Nil()
}

func TestScheduler_SkipsALateRun(t *testing.T) {
	cases := map[string]struct {
		now      time.Time
		skipped  bool
		wantNext time.Time
	}{
		"61 minutes late": {now: scheduledAt.Add(61 * time.Minute), skipped: true, wantNext: nextDay},
		"59 minutes late": {now: scheduledAt.Add(59 * time.Minute), skipped: false, wantNext: nextDay},
		"three days late": {now: scheduledAt.Add(72*time.Hour + time.Minute), skipped: true, wantNext: scheduledAt.Add(96 * time.Hour)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			job := f.addJob(t, scheduledAt)

			report, err := f.scheduler(t, c.now).RunDue(context.Background())
			gt.NoError(t, err).Required()
			got := f.stored(t, job)
			gt.True(t, got.NextRunAt.Equal(c.wantNext))
			if c.skipped {
				gt.Equal(t, report, &usecase.SchedulerReport{Skipped: 1})
				gt.A(t, f.runner.calls()).Length(0)
				gt.Value(t, got.LastRun.Status).Equal(model.JobRunSkipped)
				gt.True(t, got.LastRun.ScheduledAt.Equal(scheduledAt))
				return
			}
			gt.Equal(t, report, &usecase.SchedulerReport{Succeeded: 1})
			gt.A(t, f.runner.calls()).Length(1)
		})
	}
}

func TestScheduler_RecordsRunnerFailures(t *testing.T) {
	t.Run("runner error keeps the spending", func(t *testing.T) {
		f := newSchedulerFixture(t)
		job := f.addJob(t, scheduledAt)
		f.runner.result = &usecase.JobRunResult{Spent: 99}
		f.runner.err = errors.New("slack is down")

		report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
		gt.NoError(t, err).Required()
		gt.Equal(t, report, &usecase.SchedulerReport{Failed: 1})
		runs := f.repo.jobs.finishedRuns()
		gt.A(t, runs).Length(1).Required()
		gt.Value(t, runs[0].Status).Equal(model.JobRunFailed)
		gt.Value(t, runs[0].Failure).Equal(model.JobRunRunFailed)
		gt.Value(t, runs[0].Spent).Equal(model.NanoUSD(99))
		gt.Value(t, f.stored(t, job).LastRun.Failure).Equal(model.JobRunRunFailed)
	})

	t.Run("runner over the time limit", func(t *testing.T) {
		f := newSchedulerFixture(t)
		f.addJob(t, scheduledAt)
		f.cfg.RunTimeout = 50 * time.Millisecond
		f.runner.waitCtx = true

		report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
		gt.NoError(t, err).Required()
		gt.Equal(t, report, &usecase.SchedulerReport{Failed: 1})
		runs := f.repo.jobs.finishedRuns()
		gt.A(t, runs).Length(1).Required()
		gt.Value(t, runs[0].Failure).Equal(model.JobRunTimedOut)
		gt.Value(t, runs[0].Spent).Equal(model.NanoUSD(7))
	})
}

// The message was posted but its result could not be saved: the job keeps
// the run as running, which the settings page shows as unrecorded once the
// deadline passes. The run is not retried.
func TestScheduler_ResultNotSaved(t *testing.T) {
	f := newSchedulerFixture(t)
	job := f.addJob(t, scheduledAt)
	f.repo.jobs.finishErr = errors.New("firestore unavailable")

	report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{Succeeded: 1})

	got := f.stored(t, job)
	gt.Value(t, got.LastRun.Status).Equal(model.JobRunRunning)
	gt.True(t, got.NextRunAt.Equal(nextDay))

	report, err = f.scheduler(t, scheduledAt.Add(2*time.Minute)).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{})
	gt.A(t, f.runner.calls()).Length(1)
}

func TestScheduler_JobKindWithoutRunner(t *testing.T) {
	f := newSchedulerFixture(t)
	job := f.addJob(t, scheduledAt)
	s, err := usecase.NewScheduler(f.repo, map[model.JobKind]usecase.JobRunner{}, f.cfg)
	gt.NoError(t, err).Required()
	s.SetNowForTest(func() time.Time { return scheduledAt.Add(time.Minute) })

	report, err := s.RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{Failed: 1})
	got := f.stored(t, job)
	gt.True(t, got.NextRunAt.Equal(nextDay))
	gt.Value(t, got.LastRun.Status).Equal(model.JobRunFailed)
	gt.Value(t, got.LastRun.Failure).Equal(model.JobRunNoRunner)
}

func TestScheduler_TwoSchedulersRunAJobOnce(t *testing.T) {
	f := newSchedulerFixture(t)
	f.addJob(t, scheduledAt)
	now := scheduledAt.Add(time.Minute)
	a, b := f.scheduler(t, now), f.scheduler(t, now)

	var wg sync.WaitGroup
	reports := make([]*usecase.SchedulerReport, 2)
	for i, s := range []*usecase.Scheduler{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.RunDue(context.Background())
			gt.NoError(t, err)
			reports[i] = r
		}()
	}
	wg.Wait()
	gt.A(t, f.runner.calls()).Length(1)
	gt.Number(t, reports[0].Succeeded+reports[1].Succeeded).Equal(1)
}

func TestScheduler_DoesNotClaimAJobAnotherProcessMoved(t *testing.T) {
	f := newSchedulerFixture(t)
	job := f.addJob(t, scheduledAt)
	now := scheduledAt.Add(time.Minute)
	_, err := f.scheduler(t, now).RunDue(context.Background())
	gt.NoError(t, err).Required()

	// The second scheduler read the entry before the first one claimed it.
	f.repo.jobs.extra = []*model.JobScheduleEntry{{TeamID: job.TeamID, UserID: job.UserID, JobID: job.ID, NextRunAt: scheduledAt}}
	report, err := f.scheduler(t, now).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{NotClaimed: 1})
	gt.A(t, f.runner.calls()).Length(1)
	gt.True(t, f.stored(t, job).NextRunAt.Equal(nextDay))
}

func TestScheduler_LimitsConcurrentRuns(t *testing.T) {
	f := newSchedulerFixture(t)
	f.cfg.Concurrency = 2
	for i := 0; i < 5; i++ {
		f.addJob(t, scheduledAt)
	}

	report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{Succeeded: 5})
	gt.Bool(t, f.runner.maxSeen.Load() <= 2).True()
	gt.Number(t, f.runner.running.Load()).Equal(0)
}

func TestScheduler_ReadsEntriesInBatches(t *testing.T) {
	f := newSchedulerFixture(t)
	f.cfg.BatchSize = 2
	var jobs []*model.Job
	for i := 0; i < 5; i++ {
		jobs = append(jobs, f.addJob(t, scheduledAt))
	}

	report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{Succeeded: 5})
	for _, j := range jobs {
		gt.True(t, f.stored(t, j).NextRunAt.Equal(nextDay))
	}
}

func TestScheduler_SkipsAnEntryWithoutAJob(t *testing.T) {
	f := newSchedulerFixture(t)
	job := f.addJob(t, scheduledAt)
	// The entry stays due on every read; the scheduler handles it once.
	f.repo.jobs.extra = []*model.JobScheduleEntry{{
		TeamID: "T0123", UserID: "U0GHOST", JobID: "00000000-0000-4000-8000-999999999999", NextRunAt: scheduledAt,
	}}

	report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{Succeeded: 1, Errors: 1})
	gt.True(t, f.stored(t, job).NextRunAt.Equal(nextDay))
}

// Entries that stay due after a failure fill the first batch; the jobs after
// them still run.
func TestScheduler_FailedEntriesDoNotHideLaterJobs(t *testing.T) {
	f := newSchedulerFixture(t)
	f.cfg.BatchSize = 2
	job := f.addJob(t, scheduledAt)
	f.repo.jobs.extra = []*model.JobScheduleEntry{
		{TeamID: "T0123", UserID: "U0GHOST", JobID: "00000000-0000-4000-8000-999999999998", NextRunAt: scheduledAt.Add(-time.Hour)},
		{TeamID: "T0123", UserID: "U0GHOST", JobID: "00000000-0000-4000-8000-999999999999", NextRunAt: scheduledAt.Add(-time.Hour)},
	}

	report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
	gt.NoError(t, err).Required()
	gt.Equal(t, report, &usecase.SchedulerReport{Succeeded: 1, Errors: 2})
	gt.True(t, f.stored(t, job).NextRunAt.Equal(nextDay))
}

func TestScheduler_ListFailure(t *testing.T) {
	f := newSchedulerFixture(t)
	f.addJob(t, scheduledAt)
	listErr := errors.New("firestore is unavailable")
	f.repo.jobs.listErr = listErr

	report, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
	gt.Error(t, err).Is(listErr)
	gt.Equal(t, report, &usecase.SchedulerReport{})
	gt.A(t, f.runner.calls()).Length(0)
}

func TestScheduler_CancellationStopsClaimsButNotRuns(t *testing.T) {
	f := newSchedulerFixture(t)
	f.cfg.Concurrency = 1
	var jobs []*model.Job
	for i := 0; i < 3; i++ {
		jobs = append(jobs, f.addJob(t, scheduledAt))
	}
	f.runner.release = make(chan struct{})
	f.runner.started = make(chan struct{}, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		report *usecase.SchedulerReport
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(ctx)
		done <- result{r, err}
	}()

	<-f.runner.started
	cancel()
	select {
	case <-done:
		t.Fatal("RunDue returned before the claimed run finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(f.runner.release)
	res := <-done

	gt.Error(t, res.err).Is(context.Canceled)
	gt.Equal(t, res.report, &usecase.SchedulerReport{Succeeded: 1})
	gt.A(t, f.runner.calls()).Length(1).Required()
	// The run kept its context after the caller's was cancelled.
	gt.Value(t, f.runner.ctxErrs[0]).Nil()

	moved := 0
	for _, j := range jobs {
		if f.stored(t, j).NextRunAt.Equal(nextDay) {
			moved++
		}
	}
	gt.Number(t, moved).Equal(1)
}

func TestScheduler_DoesNotWaitForOtherBackgroundWork(t *testing.T) {
	f := newSchedulerFixture(t)
	f.addJob(t, scheduledAt)

	release := make(chan struct{})
	async.Dispatch(context.Background(), func(_ context.Context) error {
		<-release
		return nil
	})
	t.Cleanup(func() {
		close(release)
		async.Wait()
	})

	done := make(chan struct{})
	go func() {
		_, err := f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())
		gt.NoError(t, err)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunDue waited for background work it did not start")
	}
	gt.A(t, f.runner.calls()).Length(1)
}

func TestNewScheduler_RejectsInvalidConfig(t *testing.T) {
	valid := usecase.SchedulerConfig{MaxDelay: time.Hour, RunTimeout: time.Minute, RunTTL: time.Hour, Concurrency: 1, BatchSize: 1}
	_, err := usecase.NewScheduler(memory.New(), nil, valid)
	gt.NoError(t, err)

	for name, mutate := range map[string]func(c *usecase.SchedulerConfig){
		"no max delay":      func(c *usecase.SchedulerConfig) { c.MaxDelay = 0 },
		"no run timeout":    func(c *usecase.SchedulerConfig) { c.RunTimeout = 0 },
		"ttl below timeout": func(c *usecase.SchedulerConfig) { c.RunTTL = time.Second },
		"no concurrency":    func(c *usecase.SchedulerConfig) { c.Concurrency = 0 },
		"no batch size":     func(c *usecase.SchedulerConfig) { c.BatchSize = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			mutate(&c)
			_, err := usecase.NewScheduler(memory.New(), nil, c)
			gt.Error(t, err)
		})
	}
	_, err = usecase.NewScheduler(memory.New(), map[model.JobKind]usecase.JobRunner{model.JobKindHello: nil}, valid)
	gt.Error(t, err)
}
