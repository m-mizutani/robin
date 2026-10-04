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

func TestDailySchedule_Next(t *testing.T) {
	tokyo := mustLoad(t, "Asia/Tokyo")
	s := model.DailySchedule{Hour: 9, Minute: 0, TimeZone: "Asia/Tokyo"}

	cases := map[string]struct {
		after time.Time
		want  time.Time
	}{
		"before the time on the same day": {
			after: time.Date(2026, 10, 5, 8, 59, 0, 0, tokyo),
			want:  time.Date(2026, 10, 5, 9, 0, 0, 0, tokyo),
		},
		"exactly at the time": {
			after: time.Date(2026, 10, 5, 9, 0, 0, 0, tokyo),
			want:  time.Date(2026, 10, 6, 9, 0, 0, 0, tokyo),
		},
		"after the time": {
			after: time.Date(2026, 10, 5, 9, 1, 0, 0, tokyo),
			want:  time.Date(2026, 10, 6, 9, 0, 0, 0, tokyo),
		},
		"across the end of a year": {
			after: time.Date(2026, 12, 31, 9, 30, 0, 0, tokyo),
			want:  time.Date(2027, 1, 1, 9, 0, 0, 0, tokyo),
		},
		"after given in UTC": {
			after: time.Date(2026, 10, 4, 23, 30, 0, 0, time.UTC),
			want:  time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := s.Next(c.after)
			gt.NoError(t, err).Required()
			gt.True(t, got.Equal(c.want))
			gt.Equal(t, got.Location(), time.UTC)
		})
	}
}

func TestDailySchedule_NextDaylightSaving(t *testing.T) {
	ny := mustLoad(t, "America/New_York")
	s := model.DailySchedule{Hour: 2, Minute: 30, TimeZone: "America/New_York"}

	// 02:30 does not exist on 2027-03-14; time.Date gives 06:30 UTC (01:30
	// EST), and the day after is back to 02:30 EDT.
	got, err := s.Next(time.Date(2027, 3, 13, 12, 0, 0, 0, ny))
	gt.NoError(t, err).Required()
	gt.True(t, got.Equal(time.Date(2027, 3, 14, 6, 30, 0, 0, time.UTC)))
	got, err = s.Next(got)
	gt.NoError(t, err).Required()
	gt.True(t, got.Equal(time.Date(2027, 3, 15, 2, 30, 0, 0, ny)))

	// 01:30 happens twice on 2026-11-01; whichever is chosen is after "after".
	fall := model.DailySchedule{Hour: 1, Minute: 30, TimeZone: "America/New_York"}
	after := time.Date(2026, 10, 31, 12, 0, 0, 0, ny)
	got, err = fall.Next(after)
	gt.NoError(t, err).Required()
	gt.True(t, got.After(after))
	gt.Equal(t, got.In(ny).Day(), 1)
}

func TestDailySchedule_Validate(t *testing.T) {
	gt.NoError(t, model.DailySchedule{Hour: 0, Minute: 0, TimeZone: "UTC"}.Validate())
	gt.NoError(t, model.DailySchedule{Hour: 23, Minute: 59, TimeZone: "Asia/Tokyo"}.Validate())

	for name, s := range map[string]model.DailySchedule{
		"negative hour":   {Hour: -1, TimeZone: "UTC"},
		"hour 24":         {Hour: 24, TimeZone: "UTC"},
		"negative minute": {Minute: -1, TimeZone: "UTC"},
		"minute 60":       {Minute: 60, TimeZone: "UTC"},
		"empty zone":      {Hour: 9},
		"process zone":    {Hour: 9, TimeZone: "Local"},
		"unknown zone":    {Hour: 9, TimeZone: "Mars/Base"},
	} {
		t.Run(name, func(t *testing.T) {
			gt.Error(t, s.Validate())
			_, err := s.Next(time.Now())
			gt.Error(t, err)
		})
	}
}

func TestJobKind(t *testing.T) {
	gt.NoError(t, model.JobKindHello.Validate())
	gt.True(t, model.JobKindHello.Known())
	gt.NoError(t, model.JobKind("future_kind").Validate())
	gt.False(t, model.JobKind("future_kind").Known())
	gt.Error(t, model.JobKind("").Validate())
	gt.Error(t, model.JobKind("Hello").Validate())
}

func TestValidateJobChannelID(t *testing.T) {
	gt.NoError(t, model.ValidateJobChannelID("C0123ABCD"))
	gt.NoError(t, model.ValidateJobChannelID("G0123ABCD"))
	for _, id := range []string{"", "general", "D0123ABCD", "C0", "c0123abcd", "C0123/ABC"} {
		gt.Error(t, model.ValidateJobChannelID(id))
	}
}

func TestNewJobRunID(t *testing.T) {
	at := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	gt.Equal(t, model.NewJobRunID(at), model.JobRunID("20261005T000000Z"))
	gt.Equal(t, model.NewJobRunID(at.In(mustLoad(t, "Asia/Tokyo"))), model.JobRunID("20261005T000000Z"))
}

const testJobID = model.JobID("6f1c2d3e-4a5b-4c6d-8e7f-0a1b2c3d4e5f")

func validJob() *model.Job {
	now := time.Now()
	return &model.Job{
		TeamID:      "T0123",
		UserID:      "U0123",
		ID:          testJobID,
		Kind:        model.JobKindHello,
		ChannelID:   "C0123ABCD",
		ChannelName: "general",
		Schedule:    model.DailySchedule{Hour: 9, TimeZone: "Asia/Tokyo"},
		NextRunAt:   now.Add(time.Hour),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestJob_Validate(t *testing.T) {
	gt.NoError(t, validJob().Validate())

	cases := map[string]func(j *model.Job){
		"invalid user":       func(j *model.Job) { j.UserID = "x" },
		"invalid ID":         func(j *model.Job) { j.ID = "x" },
		"empty kind":         func(j *model.Job) { j.Kind = "" },
		"DM channel":         func(j *model.Job) { j.ChannelID = "D0123ABCD" },
		"empty channel name": func(j *model.Job) { j.ChannelName = "" },
		"invalid schedule":   func(j *model.Job) { j.Schedule.Hour = 24 },
		"empty next run":     func(j *model.Job) { j.NextRunAt = time.Time{} },
		"empty created_at":   func(j *model.Job) { j.CreatedAt = time.Time{} },
		"invalid last run": func(j *model.Job) {
			j.LastRun = &model.JobRunSummary{RunID: "x", ScheduledAt: time.Now(), Status: "done"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := validJob()
			mutate(j)
			gt.Error(t, j.Validate())
		})
	}
}

func validRun(status model.JobRunStatus) *model.JobRun {
	scheduled := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	started := scheduled.Add(time.Minute)
	r := &model.JobRun{
		TeamID:      "T0123",
		UserID:      "U0123",
		JobID:       testJobID,
		ID:          model.NewJobRunID(scheduled),
		Kind:        model.JobKindHello,
		ScheduledAt: scheduled,
		StartedAt:   started,
		Status:      status,
		ExpiresAt:   started.Add(720 * time.Hour),
	}
	switch status {
	case model.JobRunRunning:
		r.Deadline = started.Add(2 * time.Minute)
	case model.JobRunFailed:
		r.Failure = model.JobRunRunFailed
		r.FinishedAt = started.Add(time.Second)
	default:
		r.FinishedAt = started.Add(time.Second)
	}
	return r
}

func TestJobRun_Validate(t *testing.T) {
	for _, s := range []model.JobRunStatus{model.JobRunRunning, model.JobRunSucceeded, model.JobRunFailed, model.JobRunSkipped} {
		gt.NoError(t, validRun(s).Validate())
	}

	cases := map[string]struct {
		status model.JobRunStatus
		mutate func(r *model.JobRun)
	}{
		"invalid job ID":            {model.JobRunSucceeded, func(r *model.JobRun) { r.JobID = "x" }},
		"ID of another time":        {model.JobRunSucceeded, func(r *model.JobRun) { r.ID = "20261006T000000Z" }},
		"unknown status":            {model.JobRunSucceeded, func(r *model.JobRun) { r.Status = "done" }},
		"running with finished_at":  {model.JobRunRunning, func(r *model.JobRun) { r.FinishedAt = r.StartedAt }},
		"running without deadline":  {model.JobRunRunning, func(r *model.JobRun) { r.Deadline = time.Time{} }},
		"finished without time":     {model.JobRunSucceeded, func(r *model.JobRun) { r.FinishedAt = time.Time{} }},
		"failed without failure":    {model.JobRunFailed, func(r *model.JobRun) { r.Failure = "" }},
		"failure on a success":      {model.JobRunSucceeded, func(r *model.JobRun) { r.Failure = model.JobRunTimedOut }},
		"unknown failure":           {model.JobRunFailed, func(r *model.JobRun) { r.Failure = "crashed" }},
		"negative spending":         {model.JobRunSucceeded, func(r *model.JobRun) { r.Spent = -1 }},
		"expires before it started": {model.JobRunSucceeded, func(r *model.JobRun) { r.ExpiresAt = r.StartedAt }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := validRun(c.status)
			c.mutate(r)
			gt.Error(t, r.Validate())
		})
	}
}

func TestJobRun_Summary(t *testing.T) {
	r := validRun(model.JobRunFailed)
	s := r.Summary()
	gt.Equal(t, s.RunID, r.ID)
	gt.Equal(t, s.Status, model.JobRunFailed)
	gt.Equal(t, s.Failure, model.JobRunRunFailed)
	gt.True(t, s.ScheduledAt.Equal(r.ScheduledAt))
	gt.True(t, s.FinishedAt.Equal(r.FinishedAt))

	j := validJob()
	j.LastRun = s
	gt.NoError(t, j.Validate())
}

func TestJobClaimRequest_Validate(t *testing.T) {
	run := validRun(model.JobRunRunning)
	valid := func() *model.JobClaimRequest {
		return &model.JobClaimRequest{
			JobID:       testJobID,
			ScheduledAt: run.ScheduledAt,
			NextRunAt:   run.ScheduledAt.Add(24 * time.Hour),
			Run:         run,
			Now:         run.StartedAt,
		}
	}
	gt.NoError(t, valid().Validate())

	cases := map[string]func(r *model.JobClaimRequest){
		"next run not after":  func(r *model.JobClaimRequest) { r.NextRunAt = r.ScheduledAt },
		"no run":              func(r *model.JobClaimRequest) { r.Run = nil },
		"run of another time": func(r *model.JobClaimRequest) { r.ScheduledAt = r.ScheduledAt.Add(-time.Hour) },
		"empty now":           func(r *model.JobClaimRequest) { r.Now = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := valid()
			mutate(r)
			gt.Error(t, r.Validate())
		})
	}
}

func TestJobScheduleEntry_Validate(t *testing.T) {
	j := validJob()
	gt.NoError(t, j.ScheduleEntry().Validate())
	e := j.ScheduleEntry()
	e.NextRunAt = time.Time{}
	gt.Error(t, e.Validate())
	e = j.ScheduleEntry()
	e.JobID = "x"
	gt.Error(t, e.Validate())
}
