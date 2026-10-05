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

// fakeJob records every request. Before returning it waits for release when
// release is set, or until the context ends when waitCtx is set.
type fakeJob struct {
	maxDelay time.Duration
	err      error
	release  chan struct{}
	started  chan struct{}
	waitCtx  bool

	mu       sync.Mutex
	requests []usecase.JobRequest
	ctxErrs  []error // ctx.Err() of each call when it returned
	running  atomic.Int32
	maxSeen  atomic.Int32
}

func (j *fakeJob) MaxDelay() time.Duration { return j.maxDelay }

func (j *fakeJob) Run(ctx context.Context, req usecase.JobRequest) error {
	n := j.running.Add(1)
	defer j.running.Add(-1)
	for {
		m := j.maxSeen.Load()
		if n <= m || j.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	j.mu.Lock()
	j.requests = append(j.requests, req)
	j.mu.Unlock()
	if j.started != nil {
		j.started <- struct{}{}
	}
	if j.release != nil {
		<-j.release
	}
	if j.waitCtx {
		<-ctx.Done()
	} else {
		time.Sleep(5 * time.Millisecond)
	}
	j.mu.Lock()
	j.ctxErrs = append(j.ctxErrs, ctx.Err())
	j.mu.Unlock()
	if j.waitCtx {
		return ctx.Err()
	}
	return j.err
}

func (j *fakeJob) calls() []usecase.JobRequest {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]usecase.JobRequest(nil), j.requests...)
}

// hookRepo changes what ListDue and Update return.
type hookRepo struct {
	interfaces.Repository
	settings *settingHooks
}

func (r *hookRepo) JobSetting() interfaces.JobSettingRepository { return r.settings }

type settingHooks struct {
	interfaces.JobSettingRepository
	// extra entries come first in every ListDue answer.
	extra   []*model.JobScheduleEntry
	listErr error
	// beforeUpdate runs before every Update; an error it returns is the
	// result of that Update, which then writes nothing.
	beforeUpdate func(ctx context.Context, key model.UserKey) error
}

func (h *settingHooks) Update(ctx context.Context, key model.UserKey, fn func(*model.JobSetting) (*model.JobSetting, error)) error {
	if h.beforeUpdate != nil {
		if err := h.beforeUpdate(ctx, key); err != nil {
			return err
		}
	}
	return h.JobSettingRepository.Update(ctx, key, fn)
}

func (h *settingHooks) ListDue(ctx context.Context, now time.Time) ([]*model.JobScheduleEntry, error) {
	if h.listErr != nil {
		return nil, h.listErr
	}
	entries, err := h.JobSettingRepository.ListDue(ctx, now)
	return append(append([]*model.JobScheduleEntry(nil), h.extra...), entries...), err
}

type schedulerFixture struct {
	mem   *memory.Memory
	repo  *hookRepo
	hello *fakeJob
	cfg   usecase.SchedulerConfig
	users int
}

func newSchedulerFixture(t *testing.T) *schedulerFixture {
	t.Helper()
	mem := memory.New()
	return &schedulerFixture{
		mem:   mem,
		repo:  &hookRepo{Repository: mem, settings: &settingHooks{JobSettingRepository: mem.JobSetting()}},
		hello: &fakeJob{maxDelay: time.Hour},
		cfg:   usecase.SchedulerConfig{RunTimeout: 5 * time.Second, Concurrency: 4},
	}
}

// addTrigger stores a setting of a new user with one trigger of job at 09:00
// in Tokyo, due at nextRunAt.
func (f *schedulerFixture) addTrigger(t *testing.T, job model.JobName, nextRunAt time.Time) (model.UserKey, model.JobTrigger) {
	t.Helper()
	f.users++
	key := model.UserKey{TeamID: "T0123", UserID: model.SlackUserID(fmt.Sprintf("U%07d", f.users))}
	tr := model.JobTrigger{
		ID:        model.JobTriggerID(fmt.Sprintf("00000000-0000-4000-8000-%012d", f.users)),
		Job:       job,
		Time:      model.DailyTime{Hour: 9},
		NextRunAt: nextRunAt,
	}
	gt.NoError(t, f.mem.JobSetting().Update(context.Background(), key, func(*model.JobSetting) (*model.JobSetting, error) {
		return &model.JobSetting{
			TeamID: key.TeamID, UserID: key.UserID, ChannelID: "C0GENERAL", TimeZone: "Asia/Tokyo",
			Triggers: []model.JobTrigger{tr}, CreatedAt: nextRunAt, UpdatedAt: nextRunAt,
		}, nil
	})).Required()
	return key, tr
}

func (f *schedulerFixture) scheduler(t *testing.T, now time.Time) *usecase.Scheduler {
	t.Helper()
	s, err := usecase.NewScheduler(f.repo, map[model.JobName]usecase.Job{model.JobNameHello: f.hello}, f.cfg)
	gt.NoError(t, err).Required()
	s.SetNowForTest(func() time.Time { return now })
	return s
}

func (f *schedulerFixture) nextRun(t *testing.T, key model.UserKey, id model.JobTriggerID) time.Time {
	t.Helper()
	s, err := f.mem.JobSetting().Get(context.Background(), key)
	gt.NoError(t, err).Required()
	tr := s.Trigger(id)
	gt.Value(t, tr).NotNil().Required()
	return tr.NextRunAt
}

func TestScheduler_RunsADueTrigger(t *testing.T) {
	f := newSchedulerFixture(t)
	key, tr := f.addTrigger(t, model.JobNameHello, scheduledAt)

	gt.NoError(t, f.scheduler(t, scheduledAt.Add(3*time.Minute)).RunDue(context.Background())).Required()

	gt.Equal(t, f.hello.calls(), []usecase.JobRequest{{
		Key: key, TriggerID: tr.ID, ChannelID: "C0GENERAL", TimeZone: "Asia/Tokyo", ScheduledAt: scheduledAt,
	}})
	gt.True(t, f.nextRun(t, key, tr.ID).Equal(nextDay))
}

func TestScheduler_LeavesATriggerThatIsNotDue(t *testing.T) {
	f := newSchedulerFixture(t)
	key, tr := f.addTrigger(t, model.JobNameHello, scheduledAt)

	gt.NoError(t, f.scheduler(t, scheduledAt.Add(-time.Minute)).RunDue(context.Background())).Required()
	gt.A(t, f.hello.calls()).Length(0)
	gt.True(t, f.nextRun(t, key, tr.ID).Equal(scheduledAt))
}

func TestScheduler_LateRunsFollowTheJob(t *testing.T) {
	cases := map[string]struct {
		maxDelay time.Duration
		now      time.Time
		runs     bool
		wantNext time.Time
	}{
		"61 minutes late for a job that allows 1 hour": {time.Hour, scheduledAt.Add(61 * time.Minute), false, nextDay},
		"59 minutes late for a job that allows 1 hour": {time.Hour, scheduledAt.Add(59 * time.Minute), true, nextDay},
		"three days late for a job without a limit":    {0, scheduledAt.Add(72*time.Hour + time.Minute), true, scheduledAt.Add(96 * time.Hour)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSchedulerFixture(t)
			f.hello.maxDelay = c.maxDelay
			key, tr := f.addTrigger(t, model.JobNameHello, scheduledAt)

			gt.NoError(t, f.scheduler(t, c.now).RunDue(context.Background())).Required()
			if c.runs {
				gt.A(t, f.hello.calls()).Length(1)
			} else {
				gt.A(t, f.hello.calls()).Length(0)
			}
			gt.True(t, f.nextRun(t, key, tr.ID).Equal(c.wantNext))
		})
	}
}

func TestScheduler_TriggerOfAnUndefinedJob(t *testing.T) {
	f := newSchedulerFixture(t)
	futureKey, future := f.addTrigger(t, "future_job", scheduledAt)
	helloKey, hello := f.addTrigger(t, model.JobNameHello, scheduledAt)

	gt.NoError(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())).Required()
	calls := f.hello.calls()
	gt.A(t, calls).Length(1).Required()
	gt.Value(t, calls[0].TriggerID).Equal(hello.ID)
	gt.True(t, f.nextRun(t, futureKey, future.ID).Equal(nextDay))
	gt.True(t, f.nextRun(t, helloKey, hello.ID).Equal(nextDay))
}

func TestScheduler_FailedRunsAreNotRetried(t *testing.T) {
	t.Run("job error", func(t *testing.T) {
		f := newSchedulerFixture(t)
		f.hello.err = errors.New("slack is down")
		key, tr := f.addTrigger(t, model.JobNameHello, scheduledAt)

		gt.NoError(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())).Required()
		gt.NoError(t, f.scheduler(t, scheduledAt.Add(2*time.Minute)).RunDue(context.Background())).Required()
		gt.A(t, f.hello.calls()).Length(1)
		gt.True(t, f.nextRun(t, key, tr.ID).Equal(nextDay))
	})

	t.Run("over the time limit", func(t *testing.T) {
		f := newSchedulerFixture(t)
		f.cfg.RunTimeout = 50 * time.Millisecond
		f.hello.waitCtx = true
		f.addTrigger(t, model.JobNameHello, scheduledAt)

		gt.NoError(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())).Required()
		gt.A(t, f.hello.calls()).Length(1)
		gt.Bool(t, errors.Is(f.hello.ctxErrs[0], context.DeadlineExceeded)).True()
	})
}

func TestScheduler_TwoSchedulersRunATriggerOnce(t *testing.T) {
	f := newSchedulerFixture(t)
	f.addTrigger(t, model.JobNameHello, scheduledAt)
	now := scheduledAt.Add(time.Minute)
	a, b := f.scheduler(t, now), f.scheduler(t, now)

	var wg sync.WaitGroup
	for _, s := range []*usecase.Scheduler{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gt.NoError(t, s.RunDue(context.Background()))
		}()
	}
	wg.Wait()
	gt.A(t, f.hello.calls()).Length(1)
}

func TestScheduler_StaleEntries(t *testing.T) {
	t.Run("another process moved the trigger after listing", func(t *testing.T) {
		f := newSchedulerFixture(t)
		key, tr := f.addTrigger(t, model.JobNameHello, scheduledAt)
		now := scheduledAt.Add(time.Minute)
		gt.NoError(t, f.scheduler(t, now).RunDue(context.Background())).Required()

		f.repo.settings.extra = []*model.JobScheduleEntry{{TeamID: key.TeamID, UserID: key.UserID, TriggerID: tr.ID, NextRunAt: scheduledAt}}
		gt.NoError(t, f.scheduler(t, now).RunDue(context.Background())).Required()
		gt.A(t, f.hello.calls()).Length(1)
		gt.True(t, f.nextRun(t, key, tr.ID).Equal(nextDay))
	})

	t.Run("the trigger was deleted", func(t *testing.T) {
		f := newSchedulerFixture(t)
		key, _ := f.addTrigger(t, model.JobNameHello, scheduledAt)
		f.repo.settings.extra = []*model.JobScheduleEntry{
			{TeamID: key.TeamID, UserID: key.UserID, TriggerID: "00000000-0000-4000-8000-999999999999", NextRunAt: scheduledAt},
			{TeamID: "T0123", UserID: "U0GHOST", TriggerID: "00000000-0000-4000-8000-999999999998", NextRunAt: scheduledAt},
		}
		gt.NoError(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())).Required()
		gt.A(t, f.hello.calls()).Length(1)
	})
}

func TestScheduler_LimitsConcurrentRuns(t *testing.T) {
	f := newSchedulerFixture(t)
	f.cfg.Concurrency = 2
	for i := 0; i < 5; i++ {
		f.addTrigger(t, model.JobNameHello, scheduledAt)
	}

	gt.NoError(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())).Required()
	gt.A(t, f.hello.calls()).Length(5)
	gt.Bool(t, f.hello.maxSeen.Load() <= 2).True()
	gt.Number(t, f.hello.running.Load()).Equal(0)
}

func TestScheduler_ListFailure(t *testing.T) {
	f := newSchedulerFixture(t)
	f.addTrigger(t, model.JobNameHello, scheduledAt)
	listErr := errors.New("firestore is unavailable")
	f.repo.settings.listErr = listErr

	gt.Error(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())).Is(listErr)
	gt.A(t, f.hello.calls()).Length(0)
}

func TestScheduler_CancellationStopsClaimsButNotRuns(t *testing.T) {
	f := newSchedulerFixture(t)
	f.cfg.Concurrency = 1
	type owned struct {
		key model.UserKey
		tr  model.JobTrigger
	}
	var triggers []owned
	for i := 0; i < 3; i++ {
		key, tr := f.addTrigger(t, model.JobNameHello, scheduledAt)
		triggers = append(triggers, owned{key, tr})
	}
	f.hello.release = make(chan struct{})
	f.hello.started = make(chan struct{}, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(ctx) }()

	<-f.hello.started
	cancel()
	select {
	case <-done:
		t.Fatal("RunDue returned before the claimed run finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(f.hello.release)

	gt.Error(t, <-done).Is(context.Canceled)
	gt.A(t, f.hello.calls()).Length(1)
	// The run kept its context after the caller's was cancelled.
	gt.Value(t, f.hello.ctxErrs[0]).Nil()
	moved := 0
	for _, o := range triggers {
		if f.nextRun(t, o.key, o.tr.ID).Equal(nextDay) {
			moved++
		}
	}
	gt.Number(t, moved).Equal(1)
}

func TestScheduler_CancellationDuringTheLastClaim(t *testing.T) {
	f := newSchedulerFixture(t)
	key, tr := f.addTrigger(t, model.JobNameHello, scheduledAt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.repo.settings.beforeUpdate = func(ctx context.Context, _ model.UserKey) error {
		cancel()
		return ctx.Err()
	}

	gt.Error(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(ctx)).Is(context.Canceled)
	gt.A(t, f.hello.calls()).Length(0)
	gt.True(t, f.nextRun(t, key, tr.ID).Equal(scheduledAt))
}

func TestScheduler_ClaimFailure(t *testing.T) {
	f := newSchedulerFixture(t)
	failedKey, failed := f.addTrigger(t, model.JobNameHello, scheduledAt)
	okKey, ok := f.addTrigger(t, model.JobNameHello, scheduledAt)
	claimErr := errors.New("firestore is unavailable")
	f.repo.settings.beforeUpdate = func(_ context.Context, key model.UserKey) error {
		if key == failedKey {
			return claimErr
		}
		return nil
	}

	gt.Error(t, f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background())).Is(claimErr)
	// The trigger that could not be claimed does not stop the other one.
	calls := f.hello.calls()
	gt.A(t, calls).Length(1).Required()
	gt.Value(t, calls[0].TriggerID).Equal(ok.ID)
	gt.True(t, f.nextRun(t, okKey, ok.ID).Equal(nextDay))
	gt.True(t, f.nextRun(t, failedKey, failed.ID).Equal(scheduledAt))
}

func TestScheduler_DoesNotWaitForOtherBackgroundWork(t *testing.T) {
	f := newSchedulerFixture(t)
	f.addTrigger(t, model.JobNameHello, scheduledAt)

	release := make(chan struct{})
	async.Dispatch(context.Background(), func(_ context.Context) error {
		<-release
		return nil
	})
	t.Cleanup(func() {
		close(release)
		async.Wait()
	})

	done := make(chan error, 1)
	go func() { done <- f.scheduler(t, scheduledAt.Add(time.Minute)).RunDue(context.Background()) }()
	select {
	case err := <-done:
		gt.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("RunDue waited for background work it did not start")
	}
	gt.A(t, f.hello.calls()).Length(1)
}

func TestNewScheduler_RejectsInvalidConfig(t *testing.T) {
	valid := usecase.SchedulerConfig{RunTimeout: time.Minute, Concurrency: 1}
	_, err := usecase.NewScheduler(memory.New(), nil, valid)
	gt.NoError(t, err)

	for name, c := range map[string]usecase.SchedulerConfig{
		"no run timeout": {Concurrency: 1},
		"no concurrency": {RunTimeout: time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := usecase.NewScheduler(memory.New(), nil, c)
			gt.Error(t, err)
		})
	}
	_, err = usecase.NewScheduler(memory.New(), map[model.JobName]usecase.Job{model.JobNameHello: nil}, valid)
	gt.Error(t, err)
}
