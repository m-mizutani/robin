package repository_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// newTestJob returns a job of key due at nextRunAt. Times are whole minutes,
// as the scheduler writes them.
func newTestJob(key model.UserKey, nextRunAt, createdAt time.Time) *model.Job {
	return &model.Job{
		TeamID:      key.TeamID,
		UserID:      key.UserID,
		ID:          model.JobID(uuid.NewString()),
		Kind:        model.JobKindHello,
		ChannelID:   "C0123ABCD",
		ChannelName: "general",
		Schedule:    model.DailySchedule{Hour: 9, Minute: 30, TimeZone: "Asia/Tokyo"},
		NextRunAt:   nextRunAt,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
}

// testMinute is a whole minute far from now, so entries of these tests never
// compete with real ones.
func testMinute() time.Time {
	return time.Date(2001, 2, 3, 4, 5, 0, 0, time.UTC)
}

func createJob(t *testing.T, repo interfaces.Repository, job *model.Job) {
	t.Helper()
	gt.NoError(t, repo.Job().Create(testContext(t), job.Key(), job, 10)).Required()
	// The schedule entries are shared by every user; remove them so later
	// runs against the same Firestore do not see them as due.
	t.Cleanup(func() {
		gt.NoError(t, repo.Job().Delete(testContext(t), job.Key(), job.ID))
	})
}

func jobEqual(t *testing.T, got, want *model.Job) {
	t.Helper()
	gt.Value(t, got.TeamID).Equal(want.TeamID)
	gt.Value(t, got.UserID).Equal(want.UserID)
	gt.Value(t, got.ID).Equal(want.ID)
	gt.Value(t, got.Kind).Equal(want.Kind)
	gt.String(t, got.ChannelID).Equal(want.ChannelID)
	gt.String(t, got.ChannelName).Equal(want.ChannelName)
	gt.Value(t, got.Schedule).Equal(want.Schedule)
	timeEqual(t, got.NextRunAt, want.NextRunAt)
	timeEqual(t, got.CreatedAt, want.CreatedAt)
	timeEqual(t, got.UpdatedAt, want.UpdatedAt)
	if want.LastRun == nil {
		gt.Value(t, got.LastRun).Nil()
		return
	}
	gt.Value(t, got.LastRun).NotNil().Required()
	gt.Value(t, got.LastRun.RunID).Equal(want.LastRun.RunID)
	gt.Value(t, got.LastRun.Status).Equal(want.LastRun.Status)
	gt.Value(t, got.LastRun.Failure).Equal(want.LastRun.Failure)
	timeEqual(t, got.LastRun.ScheduledAt, want.LastRun.ScheduledAt)
	timeEqual(t, got.LastRun.Deadline, want.LastRun.Deadline)
	timeEqual(t, got.LastRun.FinishedAt, want.LastRun.FinishedAt)
}

func runningRun(job *model.Job, now time.Time) *model.JobRun {
	return &model.JobRun{
		TeamID:      job.TeamID,
		UserID:      job.UserID,
		JobID:       job.ID,
		ID:          model.NewJobRunID(job.NextRunAt),
		Kind:        job.Kind,
		ScheduledAt: job.NextRunAt,
		StartedAt:   now,
		Deadline:    now.Add(2 * time.Minute),
		Status:      model.JobRunRunning,
		ExpiresAt:   now.Add(720 * time.Hour),
	}
}

func claimRequest(job *model.Job, now time.Time) model.JobClaimRequest {
	return model.JobClaimRequest{
		JobID:       job.ID,
		ScheduledAt: job.NextRunAt,
		NextRunAt:   job.NextRunAt.Add(24 * time.Hour),
		Run:         runningRun(job, now),
		Now:         now,
	}
}

// dueEntriesOf keeps the entries of the given jobs, in the order returned.
func dueEntriesOf(entries []*model.JobScheduleEntry, jobs ...*model.Job) []*model.JobScheduleEntry {
	ids := map[model.JobID]bool{}
	for _, j := range jobs {
		ids[j.ID] = true
	}
	var out []*model.JobScheduleEntry
	for _, e := range entries {
		if ids[e.JobID] {
			out = append(out, e)
		}
	}
	return out
}

func TestJobRepository(t *testing.T) {
	runRepositoryTest(t, "create, get and list", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		second := newTestJob(key, base, base.Add(time.Minute))
		first := newTestJob(key, base, base)
		createJob(t, repo, second)
		createJob(t, repo, first)

		got, err := repo.Job().Get(ctx, key, first.ID)
		gt.NoError(t, err).Required()
		jobEqual(t, got, first)

		list, err := repo.Job().List(ctx, key)
		gt.NoError(t, err).Required()
		gt.A(t, list).Length(2).Required()
		jobEqual(t, list[0], first)
		jobEqual(t, list[1], second)

		other, err := repo.Job().List(ctx, randomUserKey(t))
		gt.NoError(t, err).Required()
		gt.A(t, other).Length(0)
	})

	runRepositoryTest(t, "create stops at the limit of the user", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		for i := 0; i < 2; i++ {
			job := newTestJob(key, base, base)
			gt.NoError(t, repo.Job().Create(ctx, key, job, 2)).Required()
			t.Cleanup(func() { gt.NoError(t, repo.Job().Delete(testContext(t), key, job.ID)) })
		}
		err := repo.Job().Create(ctx, key, newTestJob(key, base, base), 2)
		gt.Error(t, err).Is(interfaces.ErrJobLimitReached)

		// Another user's jobs do not count.
		other := randomUserKey(t)
		job := newTestJob(other, base, base)
		gt.NoError(t, repo.Job().Create(ctx, other, job, 2))
		t.Cleanup(func() { gt.NoError(t, repo.Job().Delete(testContext(t), other, job.ID)) })

		list, err := repo.Job().List(ctx, key)
		gt.NoError(t, err).Required()
		gt.A(t, list).Length(2)
	})

	runRepositoryTest(t, "create rejects a taken ID", func(t *testing.T, repo interfaces.Repository) {
		key := randomUserKey(t)
		job := newTestJob(key, testMinute(), testMinute())
		createJob(t, repo, job)
		err := repo.Job().Create(testContext(t), key, job, 10)
		gt.Error(t, err).Is(interfaces.ErrAlreadyExists)
	})

	runRepositoryTest(t, "get of a missing or another user's job", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		job := newTestJob(key, testMinute(), testMinute())
		createJob(t, repo, job)

		_, err := repo.Job().Get(ctx, key, model.JobID(uuid.NewString()))
		gt.Error(t, err).Is(interfaces.ErrNotFound)
		_, err = repo.Job().Get(ctx, randomUserKey(t), job.ID)
		gt.Error(t, err).Is(interfaces.ErrNotFound)
	})

	runRepositoryTest(t, "create writes the schedule entry and delete removes it", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		job := newTestJob(key, base, base)
		createJob(t, repo, job)

		entries, err := repo.Job().ListDue(ctx, base, 1000)
		gt.NoError(t, err).Required()
		mine := dueEntriesOf(entries, job)
		gt.A(t, mine).Length(1).Required()
		gt.Value(t, mine[0].Key()).Equal(key)
		gt.Value(t, mine[0].JobID).Equal(job.ID)
		timeEqual(t, mine[0].NextRunAt, base)

		// Another user cannot delete it.
		gt.NoError(t, repo.Job().Delete(ctx, randomUserKey(t), job.ID))
		_, err = repo.Job().Get(ctx, key, job.ID)
		gt.NoError(t, err)

		gt.NoError(t, repo.Job().Delete(ctx, key, job.ID))
		_, err = repo.Job().Get(ctx, key, job.ID)
		gt.Error(t, err).Is(interfaces.ErrNotFound)
		entries, err = repo.Job().ListDue(ctx, base, 1000)
		gt.NoError(t, err).Required()
		gt.A(t, dueEntriesOf(entries, job)).Length(0)

		// A missing job is not an error.
		gt.NoError(t, repo.Job().Delete(ctx, key, job.ID))
	})

	runRepositoryTest(t, "list due returns due entries of every user, oldest first", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		now := testMinute()
		old := newTestJob(randomUserKey(t), now.Add(-2*time.Hour), now)
		recent := newTestJob(randomUserKey(t), now.Add(-time.Minute), now)
		exact := newTestJob(randomUserKey(t), now, now)
		future := newTestJob(randomUserKey(t), now.Add(time.Minute), now)
		for _, j := range []*model.Job{future, exact, old, recent} {
			createJob(t, repo, j)
		}

		entries, err := repo.Job().ListDue(ctx, now, 1000)
		gt.NoError(t, err).Required()
		mine := dueEntriesOf(entries, old, recent, exact, future)
		gt.A(t, mine).Length(3).Required()
		gt.Value(t, mine[0].JobID).Equal(old.ID)
		gt.Value(t, mine[0].Key()).Equal(old.Key())
		gt.Value(t, mine[1].JobID).Equal(recent.ID)
		gt.Value(t, mine[2].JobID).Equal(exact.ID)

		limited, err := repo.Job().ListDue(ctx, now, 2)
		gt.NoError(t, err).Required()
		gt.A(t, limited).Length(2).Required()
		gt.Bool(t, limited[0].NextRunAt.After(limited[1].NextRunAt)).False()
		for _, e := range limited {
			gt.Bool(t, e.NextRunAt.After(now)).False()
		}
	})

	runRepositoryTest(t, "claim moves the job and records the run", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		job := newTestJob(key, base, base)
		createJob(t, repo, job)

		now := base.Add(3 * time.Minute)
		req := claimRequest(job, now)
		ok, err := repo.Job().Claim(ctx, key, req)
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		want := *job
		want.NextRunAt = req.NextRunAt
		want.LastRun = req.Run.Summary()
		want.UpdatedAt = now
		got, err := repo.Job().Get(ctx, key, job.ID)
		gt.NoError(t, err).Required()
		jobEqual(t, got, &want)

		entries, err := repo.Job().ListDue(ctx, req.NextRunAt, 1000)
		gt.NoError(t, err).Required()
		mine := dueEntriesOf(entries, job)
		gt.A(t, mine).Length(1).Required()
		timeEqual(t, mine[0].NextRunAt, req.NextRunAt)
	})

	runRepositoryTest(t, "claim writes nothing when the run is not the job's next one", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		job := newTestJob(key, base, base)
		createJob(t, repo, job)
		now := base.Add(time.Minute)

		ok, err := repo.Job().Claim(ctx, key, claimRequest(job, now))
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		// The same run again: the job has moved on.
		ok, err = repo.Job().Claim(ctx, key, claimRequest(job, now))
		gt.NoError(t, err).Required()
		gt.False(t, ok)

		// Another scheduled time than the job's.
		stale := *job
		stale.NextRunAt = base.Add(-24 * time.Hour)
		ok, err = repo.Job().Claim(ctx, key, claimRequest(&stale, now))
		gt.NoError(t, err).Required()
		gt.False(t, ok)

		// A missing job.
		missing := newTestJob(key, base, base)
		ok, err = repo.Job().Claim(ctx, key, claimRequest(missing, now))
		gt.NoError(t, err).Required()
		gt.False(t, ok)

		got, err := repo.Job().Get(ctx, key, job.ID)
		gt.NoError(t, err).Required()
		timeEqual(t, got.NextRunAt, base.Add(24*time.Hour))
	})

	runRepositoryTest(t, "concurrent claims of one run let exactly one through", func(t *testing.T, repo interfaces.Repository) {
		key := randomUserKey(t)
		base := testMinute()
		job := newTestJob(key, base, base)
		createJob(t, repo, job)
		req := claimRequest(job, base.Add(time.Minute))

		var mu sync.Mutex
		claimed := 0
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := repo.Job().Claim(testContext(t), key, req)
				gt.NoError(t, err)
				if ok {
					mu.Lock()
					claimed++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		gt.Number(t, claimed).Equal(1)
	})

	runRepositoryTest(t, "finish records the result as the job's last run", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		job := newTestJob(key, base, base)
		createJob(t, repo, job)
		now := base.Add(time.Minute)
		req := claimRequest(job, now)
		ok, err := repo.Job().Claim(ctx, key, req)
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		run := *req.Run
		run.Status = model.JobRunSucceeded
		run.FinishedAt = now.Add(10 * time.Second)
		run.MessageTS = "1700000000.000100"
		run.Spent = 12345
		gt.NoError(t, repo.Job().Finish(ctx, key, &run)).Required()

		got, err := repo.Job().Get(ctx, key, job.ID)
		gt.NoError(t, err).Required()
		gt.Value(t, got.LastRun.Status).Equal(model.JobRunSucceeded)
		timeEqual(t, got.LastRun.FinishedAt, run.FinishedAt)
		timeEqual(t, got.UpdatedAt, run.FinishedAt)

		// A running run cannot be the result.
		gt.Error(t, repo.Job().Finish(ctx, key, req.Run))
	})

	runRepositoryTest(t, "finish of an older run leaves the job", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		job := newTestJob(key, base, base)
		createJob(t, repo, job)
		now := base.Add(time.Minute)
		first := claimRequest(job, now)
		ok, err := repo.Job().Claim(ctx, key, first)
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		moved := *job
		moved.NextRunAt = first.NextRunAt
		second := claimRequest(&moved, now.Add(24*time.Hour))
		ok, err = repo.Job().Claim(ctx, key, second)
		gt.NoError(t, err).Required()
		gt.True(t, ok)

		run := *first.Run
		run.Status = model.JobRunFailed
		run.Failure = model.JobRunTimedOut
		run.FinishedAt = now.Add(2 * time.Minute)
		gt.NoError(t, repo.Job().Finish(ctx, key, &run))

		got, err := repo.Job().Get(ctx, key, job.ID)
		gt.NoError(t, err).Required()
		gt.Value(t, got.LastRun.RunID).Equal(second.Run.ID)
		gt.Value(t, got.LastRun.Status).Equal(model.JobRunRunning)
	})

	runRepositoryTest(t, "finish after the job was deleted", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		job := newTestJob(key, base, base)
		createJob(t, repo, job)
		req := claimRequest(job, base.Add(time.Minute))
		ok, err := repo.Job().Claim(ctx, key, req)
		gt.NoError(t, err).Required()
		gt.True(t, ok)
		gt.NoError(t, repo.Job().Delete(ctx, key, job.ID)).Required()

		run := *req.Run
		run.Status = model.JobRunSucceeded
		run.FinishedAt = base.Add(2 * time.Minute)
		gt.NoError(t, repo.Job().Finish(ctx, key, &run))
		_, err = repo.Job().Get(ctx, key, job.ID)
		gt.Error(t, err).Is(interfaces.ErrNotFound)
	})
}
