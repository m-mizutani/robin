package model_test

import (
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/model"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	gt.NoError(t, err).Required()
	return loc
}

func TestDailyTime_Next(t *testing.T) {
	tokyo := mustLoad(t, "Asia/Tokyo")
	at := model.DailyTime{Hour: 9, Minute: 0}

	cases := map[string]struct {
		after time.Time
		want  time.Time
	}{
		"before the time on the same day": {time.Date(2026, 10, 5, 8, 59, 0, 0, tokyo), time.Date(2026, 10, 5, 9, 0, 0, 0, tokyo)},
		"exactly at the time":             {time.Date(2026, 10, 5, 9, 0, 0, 0, tokyo), time.Date(2026, 10, 6, 9, 0, 0, 0, tokyo)},
		"after the time":                  {time.Date(2026, 10, 5, 9, 1, 0, 0, tokyo), time.Date(2026, 10, 6, 9, 0, 0, 0, tokyo)},
		"across the end of a year":        {time.Date(2026, 12, 31, 9, 30, 0, 0, tokyo), time.Date(2027, 1, 1, 9, 0, 0, 0, tokyo)},
		"after given in UTC":              {time.Date(2026, 10, 4, 23, 30, 0, 0, time.UTC), time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := at.Next(c.after, tokyo)
			gt.True(t, got.Equal(c.want))
			gt.Equal(t, got.Location(), time.UTC)
		})
	}
}

func TestDailyTime_NextDaylightSaving(t *testing.T) {
	ny := mustLoad(t, "America/New_York")
	at := model.DailyTime{Hour: 2, Minute: 30}

	// 02:30 does not exist on 2027-03-14; time.Date gives 06:30 UTC (01:30
	// EST), and the day after is back to 02:30 EDT.
	got := at.Next(time.Date(2027, 3, 13, 12, 0, 0, 0, ny), ny)
	gt.True(t, got.Equal(time.Date(2027, 3, 14, 6, 30, 0, 0, time.UTC)))
	gt.True(t, at.Next(got, ny).Equal(time.Date(2027, 3, 15, 2, 30, 0, 0, ny)))

	// 01:30 happens twice on 2026-11-01; either is after "after".
	after := time.Date(2026, 10, 31, 12, 0, 0, 0, ny)
	got = model.DailyTime{Hour: 1, Minute: 30}.Next(after, ny)
	gt.True(t, got.After(after))
	gt.Equal(t, got.In(ny).Day(), 1)
}

func TestDailyTime_Validate(t *testing.T) {
	gt.NoError(t, model.DailyTime{Hour: 0, Minute: 0}.Validate())
	gt.NoError(t, model.DailyTime{Hour: 23, Minute: 59}.Validate())
	for _, at := range []model.DailyTime{{Hour: -1}, {Hour: 24}, {Minute: -1}, {Minute: 60}} {
		gt.Error(t, at.Validate())
	}
}

func TestLoadTimeZone(t *testing.T) {
	for _, name := range []string{"UTC", "Asia/Tokyo"} {
		_, err := model.LoadTimeZone(name)
		gt.NoError(t, err)
	}
	for _, name := range []string{"", "Local", "Mars/Base"} {
		_, err := model.LoadTimeZone(name)
		gt.Error(t, err)
	}
}

func TestJobName(t *testing.T) {
	gt.NoError(t, model.JobNameHello.Validate())
	gt.True(t, model.JobNameHello.Known())
	gt.NoError(t, model.JobName("future_job").Validate())
	gt.False(t, model.JobName("future_job").Known())
	gt.Error(t, model.JobName("").Validate())
	gt.Error(t, model.JobName("Hello").Validate())
}

func TestValidateJobChannelID(t *testing.T) {
	gt.NoError(t, model.ValidateJobChannelID("C0123ABCD"))
	gt.NoError(t, model.ValidateJobChannelID("G0123ABCD"))
	for _, id := range []string{"", "general", "D0123ABCD", "C0", "c0123abcd"} {
		gt.Error(t, model.ValidateJobChannelID(id))
	}
}

const (
	triggerA = model.JobTriggerID("6f1c2d3e-4a5b-4c6d-8e7f-0a1b2c3d4e5f")
	triggerB = model.JobTriggerID("7a2d3e4f-5b6c-4d7e-9f80-1b2c3d4e5f60")
)

func validSetting() *model.JobSetting {
	now := time.Now()
	return &model.JobSetting{
		TeamID:    "T0123",
		UserID:    "U0123",
		ChannelID: "C0123ABCD",
		TimeZone:  "Asia/Tokyo",
		Triggers: []model.JobTrigger{
			{ID: triggerA, Job: model.JobNameHello, Time: model.DailyTime{Hour: 9}, NextRunAt: now.Add(time.Hour)},
			{ID: triggerB, Job: model.JobNameHello, Time: model.DailyTime{Hour: 18}, NextRunAt: now.Add(2 * time.Hour)},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestJobSetting_Validate(t *testing.T) {
	gt.NoError(t, validSetting().Validate())
	noTriggers := validSetting()
	noTriggers.Triggers = nil
	gt.NoError(t, noTriggers.Validate())

	cases := map[string]func(s *model.JobSetting){
		"invalid user":         func(s *model.JobSetting) { s.UserID = "x" },
		"DM channel":           func(s *model.JobSetting) { s.ChannelID = "D0123ABCD" },
		"process time zone":    func(s *model.JobSetting) { s.TimeZone = "Local" },
		"unknown time zone":    func(s *model.JobSetting) { s.TimeZone = "Mars/Base" },
		"duplicate trigger ID": func(s *model.JobSetting) { s.Triggers[1].ID = triggerA },
		"invalid trigger ID":   func(s *model.JobSetting) { s.Triggers[0].ID = "x" },
		"invalid job name":     func(s *model.JobSetting) { s.Triggers[0].Job = "" },
		"time out of range":    func(s *model.JobSetting) { s.Triggers[0].Time.Hour = 24 },
		"no next run":          func(s *model.JobSetting) { s.Triggers[0].NextRunAt = time.Time{} },
		"no created_at":        func(s *model.JobSetting) { s.CreatedAt = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSetting()
			mutate(s)
			gt.Error(t, s.Validate())
		})
	}
}

func TestJobSetting_TriggerAndClone(t *testing.T) {
	s := validSetting()
	gt.Value(t, s.Trigger(triggerB).Time.Hour).Equal(18)
	gt.Value(t, s.Trigger("00000000-0000-4000-8000-000000000000")).Nil()

	c := s.Clone()
	c.Triggers[0].Time.Hour = 7
	gt.Value(t, s.Triggers[0].Time.Hour).Equal(9)

	entries := s.ScheduleEntries()
	gt.A(t, entries).Length(2).Required()
	gt.Value(t, entries[0].Key()).Equal(s.Key())
	gt.Value(t, entries[0].TriggerID).Equal(triggerA)
	gt.True(t, entries[0].NextRunAt.Equal(s.Triggers[0].NextRunAt))
	gt.NoError(t, entries[0].Validate())
}
