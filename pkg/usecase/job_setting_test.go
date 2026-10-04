package usecase_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
)

var (
	settingOwner = model.UserKey{TeamID: "T0123", UserID: "U0ALICE"}
	// 08:30 in Tokyo on 2026-10-05.
	settingNow = time.Date(2026, 10, 4, 23, 30, 0, 0, time.UTC)
)

type settingFixture struct {
	repo *memory.Memory
	uc   *usecase.JobSettingUseCase
	now  time.Time
	ids  int
}

func newSettingFixture(t *testing.T) *settingFixture {
	t.Helper()
	f := &settingFixture{repo: memory.New(), now: settingNow}
	f.uc = usecase.NewJobSettingUseCase(f.repo)
	f.uc.SetClockForTest(func() time.Time { return f.now }, func() string {
		f.ids++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", f.ids)
	})
	return f
}

func (f *settingFixture) stored(t *testing.T, key model.UserKey) *model.JobSetting {
	t.Helper()
	s, err := f.uc.Get(context.Background(), key)
	gt.NoError(t, err).Required()
	return s
}

func (f *settingFixture) due(t *testing.T, at time.Time) []*model.JobScheduleEntry {
	t.Helper()
	entries, err := f.repo.JobSetting().ListDue(context.Background(), at)
	gt.NoError(t, err).Required()
	return entries
}

func TestJobSettingUseCase_GetWithoutSetting(t *testing.T) {
	f := newSettingFixture(t)
	gt.Value(t, f.stored(t, settingOwner)).Nil()
}

func TestJobSettingUseCase_Save(t *testing.T) {
	f := newSettingFixture(t)
	saved, err := f.uc.Save(context.Background(), settingOwner, "C0GENERAL", "Asia/Tokyo")
	gt.NoError(t, err).Required()
	gt.Equal(t, saved, &model.JobSetting{
		TeamID: settingOwner.TeamID, UserID: settingOwner.UserID,
		ChannelID: "C0GENERAL", TimeZone: "Asia/Tokyo",
		CreatedAt: settingNow, UpdatedAt: settingNow,
	})
	gt.Equal(t, f.stored(t, settingOwner), saved)
}

func TestJobSettingUseCase_SaveRejectsInvalidInput(t *testing.T) {
	for name, in := range map[string][2]string{
		"channel name":      {"general", "Asia/Tokyo"},
		"DM":                {"D0123ABCD", "Asia/Tokyo"},
		"empty time zone":   {"C0GENERAL", ""},
		"process time zone": {"C0GENERAL", "Local"},
		"unknown time zone": {"C0GENERAL", "Mars/Base"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSettingFixture(t)
			_, err := f.uc.Save(context.Background(), settingOwner, in[0], in[1])
			gt.Error(t, err).Is(usecase.ErrJobInputInvalid)
			gt.Value(t, f.stored(t, settingOwner)).Nil()
		})
	}
}

func TestJobSettingUseCase_AddTrigger(t *testing.T) {
	f := newSettingFixture(t)
	_, err := f.uc.Save(context.Background(), settingOwner, "C0GENERAL", "Asia/Tokyo")
	gt.NoError(t, err).Required()

	morning, err := f.uc.AddTrigger(context.Background(), settingOwner, model.JobNameHello, model.DailyTime{Hour: 9})
	gt.NoError(t, err).Required()
	gt.Equal(t, morning, &model.JobTrigger{
		ID: "00000000-0000-4000-8000-000000000001", Job: model.JobNameHello,
		Time: model.DailyTime{Hour: 9}, NextRunAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
	})
	// The same job can have another time.
	evening, err := f.uc.AddTrigger(context.Background(), settingOwner, model.JobNameHello, model.DailyTime{Hour: 18, Minute: 30})
	gt.NoError(t, err).Required()

	s := f.stored(t, settingOwner)
	gt.Equal(t, s.Triggers, []model.JobTrigger{*morning, *evening})
	entries := f.due(t, evening.NextRunAt)
	gt.A(t, entries).Length(2).Required()
	gt.Value(t, entries[0].TriggerID).Equal(morning.ID)
	gt.Value(t, entries[0].Key()).Equal(settingOwner)
}

func TestJobSettingUseCase_AddTriggerRejects(t *testing.T) {
	t.Run("without a setting", func(t *testing.T) {
		f := newSettingFixture(t)
		_, err := f.uc.AddTrigger(context.Background(), settingOwner, model.JobNameHello, model.DailyTime{Hour: 9})
		gt.Error(t, err).Is(usecase.ErrJobSettingRequired)
		gt.Value(t, f.stored(t, settingOwner)).Nil()
	})

	for name, c := range map[string]struct {
		job model.JobName
		at  model.DailyTime
	}{
		"unknown job":       {"unknown", model.DailyTime{Hour: 9}},
		"hour out of range": {model.JobNameHello, model.DailyTime{Hour: 24}},
		"negative minute":   {model.JobNameHello, model.DailyTime{Minute: -1}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSettingFixture(t)
			_, err := f.uc.Save(context.Background(), settingOwner, "C0GENERAL", "Asia/Tokyo")
			gt.NoError(t, err).Required()
			_, err = f.uc.AddTrigger(context.Background(), settingOwner, c.job, c.at)
			gt.Error(t, err).Is(usecase.ErrJobInputInvalid)
			gt.A(t, f.stored(t, settingOwner).Triggers).Length(0)
		})
	}
}

func TestJobSettingUseCase_SaveMovesTriggersToANewTimeZone(t *testing.T) {
	f := newSettingFixture(t)
	_, err := f.uc.Save(context.Background(), settingOwner, "C0GENERAL", "Asia/Tokyo")
	gt.NoError(t, err).Required()
	tr, err := f.uc.AddTrigger(context.Background(), settingOwner, model.JobNameHello, model.DailyTime{Hour: 9})
	gt.NoError(t, err).Required()

	// Changing only the channel keeps the next run.
	_, err = f.uc.Save(context.Background(), settingOwner, "C0OTHER", "Asia/Tokyo")
	gt.NoError(t, err).Required()
	gt.True(t, f.stored(t, settingOwner).Triggers[0].NextRunAt.Equal(tr.NextRunAt))

	// 09:00 in UTC after 2026-10-04 23:30 UTC is 2026-10-05 09:00 UTC.
	_, err = f.uc.Save(context.Background(), settingOwner, "C0OTHER", "UTC")
	gt.NoError(t, err).Required()
	want := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	gt.True(t, f.stored(t, settingOwner).Triggers[0].NextRunAt.Equal(want))
	gt.A(t, f.due(t, tr.NextRunAt)).Length(0)
	gt.A(t, f.due(t, want)).Length(1)
}

func TestJobSettingUseCase_DeleteTrigger(t *testing.T) {
	f := newSettingFixture(t)
	other := model.UserKey{TeamID: settingOwner.TeamID, UserID: "U0BOB"}
	for _, key := range []model.UserKey{settingOwner, other} {
		_, err := f.uc.Save(context.Background(), key, "C0GENERAL", "Asia/Tokyo")
		gt.NoError(t, err).Required()
	}
	mine, err := f.uc.AddTrigger(context.Background(), settingOwner, model.JobNameHello, model.DailyTime{Hour: 9})
	gt.NoError(t, err).Required()
	theirs, err := f.uc.AddTrigger(context.Background(), other, model.JobNameHello, model.DailyTime{Hour: 9})
	gt.NoError(t, err).Required()

	// Another user's trigger ID changes nothing.
	gt.NoError(t, f.uc.DeleteTrigger(context.Background(), settingOwner, theirs.ID))
	gt.A(t, f.stored(t, other).Triggers).Length(1)

	gt.NoError(t, f.uc.DeleteTrigger(context.Background(), settingOwner, mine.ID))
	gt.A(t, f.stored(t, settingOwner).Triggers).Length(0)
	entries := f.due(t, mine.NextRunAt)
	gt.A(t, entries).Length(1).Required()
	gt.Value(t, entries[0].TriggerID).Equal(theirs.ID)

	// Deleting again, or without a setting, is not an error.
	gt.NoError(t, f.uc.DeleteTrigger(context.Background(), settingOwner, mine.ID))
	gt.NoError(t, f.uc.DeleteTrigger(context.Background(), model.UserKey{TeamID: "T0123", UserID: "U0NOBODY"}, mine.ID))
}
