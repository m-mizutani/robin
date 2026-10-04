package repository_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// testMinute is a whole minute far from now, so entries of these tests never
// compete with real ones.
func testMinute() time.Time {
	return time.Date(2001, 2, 3, 4, 5, 0, 0, time.UTC)
}

func newTrigger(nextRunAt time.Time) model.JobTrigger {
	return model.JobTrigger{
		ID:        model.JobTriggerID(uuid.NewString()),
		Job:       model.JobNameHello,
		Time:      model.DailyTime{Hour: 9, Minute: 30},
		NextRunAt: nextRunAt,
	}
}

func newSetting(key model.UserKey, triggers ...model.JobTrigger) *model.JobSetting {
	at := testMinute()
	return &model.JobSetting{
		TeamID:    key.TeamID,
		UserID:    key.UserID,
		ChannelID: "C0123ABCD",
		TimeZone:  "Asia/Tokyo",
		Triggers:  triggers,
		CreatedAt: at,
		UpdatedAt: at,
	}
}

// putSetting stores setting and removes its schedule entries at the end of
// the test, so later runs against the same Firestore do not see them as due.
func putSetting(t *testing.T, repo interfaces.Repository, setting *model.JobSetting) {
	t.Helper()
	gt.NoError(t, repo.JobSetting().Update(testContext(t), setting.Key(), func(*model.JobSetting) (*model.JobSetting, error) {
		return setting, nil
	})).Required()
	t.Cleanup(func() {
		gt.NoError(t, repo.JobSetting().Update(testContext(t), setting.Key(), func(cur *model.JobSetting) (*model.JobSetting, error) {
			if cur == nil {
				return nil, nil
			}
			cur.Triggers = nil
			return cur, nil
		}))
	})
}

func settingEqual(t *testing.T, got, want *model.JobSetting) {
	t.Helper()
	gt.Value(t, got.Key()).Equal(want.Key())
	gt.String(t, got.ChannelID).Equal(want.ChannelID)
	gt.String(t, got.TimeZone).Equal(want.TimeZone)
	timeEqual(t, got.CreatedAt, want.CreatedAt)
	timeEqual(t, got.UpdatedAt, want.UpdatedAt)
	gt.A(t, got.Triggers).Length(len(want.Triggers)).Required()
	for i, w := range want.Triggers {
		g := got.Triggers[i]
		gt.Value(t, g.ID).Equal(w.ID)
		gt.Value(t, g.Job).Equal(w.Job)
		gt.Value(t, g.Time).Equal(w.Time)
		timeEqual(t, g.NextRunAt, w.NextRunAt)
	}
}

// entriesOf keeps the entries of the given triggers, in the order returned.
func entriesOf(entries []*model.JobScheduleEntry, triggers ...model.JobTrigger) []*model.JobScheduleEntry {
	ids := map[model.JobTriggerID]bool{}
	for _, tr := range triggers {
		ids[tr.ID] = true
	}
	var out []*model.JobScheduleEntry
	for _, e := range entries {
		if ids[e.TriggerID] {
			out = append(out, e)
		}
	}
	return out
}

func TestJobSettingRepository(t *testing.T) {
	runRepositoryTest(t, "get of a missing setting", func(t *testing.T, repo interfaces.Repository) {
		_, err := repo.JobSetting().Get(testContext(t), randomUserKey(t))
		gt.Error(t, err).Is(interfaces.ErrNotFound)
	})

	runRepositoryTest(t, "update creates the setting and its schedule entries", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		a, b := newTrigger(base), newTrigger(base.Add(time.Hour))
		want := newSetting(key, a, b)

		var seen *model.JobSetting
		gt.NoError(t, repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
			seen = cur
			return want, nil
		})).Required()
		t.Cleanup(func() { putSetting(t, repo, newSetting(key)) })
		gt.Value(t, seen).Nil()

		got, err := repo.JobSetting().Get(ctx, key)
		gt.NoError(t, err).Required()
		settingEqual(t, got, want)

		entries, err := repo.JobSetting().ListDue(ctx, base.Add(time.Hour))
		gt.NoError(t, err).Required()
		mine := entriesOf(entries, a, b)
		gt.A(t, mine).Length(2).Required()
		gt.Value(t, mine[0].TriggerID).Equal(a.ID)
		gt.Value(t, mine[0].Key()).Equal(key)
		timeEqual(t, mine[0].NextRunAt, a.NextRunAt)
		gt.Value(t, mine[1].TriggerID).Equal(b.ID)

		// Another user cannot read it.
		_, err = repo.JobSetting().Get(ctx, randomUserKey(t))
		gt.Error(t, err).Is(interfaces.ErrNotFound)
	})

	runRepositoryTest(t, "update passes the stored setting and moves the schedule entries", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		base := testMinute()
		a, b := newTrigger(base), newTrigger(base)
		putSetting(t, repo, newSetting(key, a, b))

		moved := base.Add(24 * time.Hour)
		gt.NoError(t, repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
			gt.Value(t, cur).NotNil().Required()
			gt.A(t, cur.Triggers).Length(2)
			cur.Triggers = []model.JobTrigger{cur.Triggers[0]}
			cur.Triggers[0].NextRunAt = moved
			return cur, nil
		})).Required()

		got, err := repo.JobSetting().Get(ctx, key)
		gt.NoError(t, err).Required()
		gt.A(t, got.Triggers).Length(1).Required()
		timeEqual(t, got.Triggers[0].NextRunAt, moved)

		entries, err := repo.JobSetting().ListDue(ctx, moved)
		gt.NoError(t, err).Required()
		mine := entriesOf(entries, a, b)
		gt.A(t, mine).Length(1).Required()
		gt.Value(t, mine[0].TriggerID).Equal(a.ID)
		timeEqual(t, mine[0].NextRunAt, moved)

		// a is no longer due at the old time; b is gone.
		entries, err = repo.JobSetting().ListDue(ctx, base)
		gt.NoError(t, err).Required()
		gt.A(t, entriesOf(entries, a, b)).Length(0)
	})

	runRepositoryTest(t, "update writes nothing when fn returns nil or an error", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		a := newTrigger(testMinute())
		putSetting(t, repo, newSetting(key, a))

		gt.NoError(t, repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
			cur.ChannelID = "C0OTHER"
			return nil, nil
		}))
		fnErr := errors.New("rejected")
		err := repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
			cur.ChannelID = "C0OTHER"
			return cur, fnErr
		})
		gt.Error(t, err).Is(fnErr)

		got, err := repo.JobSetting().Get(ctx, key)
		gt.NoError(t, err).Required()
		gt.String(t, got.ChannelID).Equal("C0123ABCD")
	})

	runRepositoryTest(t, "update rejects an invalid setting or another user's setting", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		key := randomUserKey(t)
		gt.Error(t, repo.JobSetting().Update(ctx, key, func(*model.JobSetting) (*model.JobSetting, error) {
			s := newSetting(key)
			s.TimeZone = "Local"
			return s, nil
		}))
		err := repo.JobSetting().Update(ctx, key, func(*model.JobSetting) (*model.JobSetting, error) {
			return newSetting(randomUserKey(t)), nil
		})
		gt.Error(t, err).Is(interfaces.ErrKeyMismatch)
		_, err = repo.JobSetting().Get(ctx, key)
		gt.Error(t, err).Is(interfaces.ErrNotFound)
	})

	runRepositoryTest(t, "list due returns due entries of every user, oldest first", func(t *testing.T, repo interfaces.Repository) {
		ctx := testContext(t)
		now := testMinute()
		old, recent, exact, future := newTrigger(now.Add(-2*time.Hour)), newTrigger(now.Add(-time.Minute)), newTrigger(now), newTrigger(now.Add(time.Minute))
		putSetting(t, repo, newSetting(randomUserKey(t), future, exact))
		putSetting(t, repo, newSetting(randomUserKey(t), old))
		putSetting(t, repo, newSetting(randomUserKey(t), recent))

		entries, err := repo.JobSetting().ListDue(ctx, now)
		gt.NoError(t, err).Required()
		mine := entriesOf(entries, old, recent, exact, future)
		gt.A(t, mine).Length(3).Required()
		gt.Value(t, mine[0].TriggerID).Equal(old.ID)
		gt.Value(t, mine[1].TriggerID).Equal(recent.ID)
		gt.Value(t, mine[2].TriggerID).Equal(exact.ID)
	})

	// Each update compares the trigger's next run time with the one it read
	// before, as a claim does; exactly one of them sees it unchanged.
	runRepositoryTest(t, "concurrent claims of one trigger let exactly one through", func(t *testing.T, repo interfaces.Repository) {
		key := randomUserKey(t)
		base := testMinute()
		a := newTrigger(base)
		putSetting(t, repo, newSetting(key, a))

		var mu sync.Mutex
		claimed := 0
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				won := false
				err := repo.JobSetting().Update(testContext(t), key, func(cur *model.JobSetting) (*model.JobSetting, error) {
					won = false
					tr := cur.Trigger(a.ID)
					if tr == nil || !tr.NextRunAt.Equal(base) {
						return nil, nil
					}
					tr.NextRunAt = base.Add(24 * time.Hour)
					won = true
					return cur, nil
				})
				gt.NoError(t, err)
				if won {
					mu.Lock()
					claimed++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		gt.Number(t, claimed).Equal(1)
	})
}
