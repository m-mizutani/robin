package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
)

// JobSettingUseCase reads and changes the signed-in user's job setting: the
// channel, the time zone and the triggers.
type JobSettingUseCase struct {
	repo  interfaces.Repository
	now   func() time.Time
	newID func() string
}

func NewJobSettingUseCase(repo interfaces.Repository) *JobSettingUseCase {
	return &JobSettingUseCase{repo: repo, now: time.Now, newID: uuid.NewString}
}

// Get returns the user's setting, or nil when the user has none.
func (uc *JobSettingUseCase) Get(ctx context.Context, key model.UserKey) (*model.JobSetting, error) {
	s, err := uc.repo.JobSetting().Get(ctx, key)
	if errors.Is(err, interfaces.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get job setting")
	}
	return s, nil
}

// Save stores the channel and the time zone. A new time zone moves every
// trigger to its next time in that zone.
func (uc *JobSettingUseCase) Save(ctx context.Context, key model.UserKey, channelID, timeZone string) (*model.JobSetting, error) {
	if err := model.ValidateJobChannelID(channelID); err != nil {
		return nil, goerr.Wrap(ErrJobInputInvalid, "invalid job channel", goerr.V("channel_id", channelID))
	}
	loc, err := model.LoadTimeZone(timeZone)
	if err != nil {
		return nil, goerr.Wrap(ErrJobInputInvalid, "invalid job time zone", goerr.V("time_zone", timeZone))
	}

	now := uc.now()
	var saved *model.JobSetting
	err = uc.repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
		next := cur
		if next == nil {
			next = &model.JobSetting{TeamID: key.TeamID, UserID: key.UserID, CreatedAt: now}
		}
		if next.TimeZone != timeZone {
			for i := range next.Triggers {
				next.Triggers[i].NextRunAt = next.Triggers[i].Time.Next(now, loc)
			}
		}
		next.ChannelID = channelID
		next.TimeZone = timeZone
		next.UpdatedAt = now
		saved = next
		return next, nil
	})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to save job setting")
	}
	return saved, nil
}

// AddTrigger adds a time to start job every day. The user has to have saved
// the channel and the time zone first.
func (uc *JobSettingUseCase) AddTrigger(ctx context.Context, key model.UserKey, job model.JobName, at model.DailyTime) (*model.JobTrigger, error) {
	if !job.Known() {
		return nil, goerr.Wrap(ErrJobInputInvalid, "unknown job", goerr.V("job", job))
	}
	if err := at.Validate(); err != nil {
		return nil, goerr.Wrap(ErrJobInputInvalid, "invalid trigger time", goerr.V("hour", at.Hour), goerr.V("minute", at.Minute))
	}

	now := uc.now()
	id := model.JobTriggerID(uc.newID())
	var added *model.JobTrigger
	err := uc.repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
		if cur == nil {
			return nil, goerr.Wrap(ErrJobSettingRequired, "no job setting")
		}
		loc, err := cur.Location()
		if err != nil {
			return nil, err
		}
		t := model.JobTrigger{ID: id, Job: job, Time: at, NextRunAt: at.Next(now, loc)}
		cur.Triggers = append(cur.Triggers, t)
		cur.UpdatedAt = now
		added = &t
		return cur, nil
	})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to add job trigger", goerr.V("job", job))
	}
	return added, nil
}

// DeleteTrigger removes the trigger. A trigger the user does not have is not
// an error.
func (uc *JobSettingUseCase) DeleteTrigger(ctx context.Context, key model.UserKey, id model.JobTriggerID) error {
	now := uc.now()
	err := uc.repo.JobSetting().Update(ctx, key, func(cur *model.JobSetting) (*model.JobSetting, error) {
		if cur == nil || cur.Trigger(id) == nil {
			return nil, nil
		}
		kept := cur.Triggers[:0]
		for _, t := range cur.Triggers {
			if t.ID != id {
				kept = append(kept, t)
			}
		}
		cur.Triggers = kept
		cur.UpdatedAt = now
		return cur, nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to delete job trigger", goerr.V("trigger_id", id))
	}
	return nil
}
