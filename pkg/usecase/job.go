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

type JobConfig struct {
	MaxJobsPerUser int // > 0
}

// JobInput is a job a user asks to add on the settings page.
type JobInput struct {
	Kind      model.JobKind
	ChannelID string
	Hour      int
	Minute    int
	TimeZone  string
}

// JobList is the jobs of one user and how many the user may have.
type JobList struct {
	Jobs    []*model.Job
	MaxJobs int
}

// JobUseCase lists, adds and deletes the jobs of the signed-in user.
type JobUseCase struct {
	repo  interfaces.Repository
	bot   interfaces.SlackBot
	cfg   JobConfig
	now   func() time.Time
	newID func() string
}

func NewJobUseCase(repo interfaces.Repository, bot interfaces.SlackBot, cfg JobConfig) *JobUseCase {
	return &JobUseCase{repo: repo, bot: bot, cfg: cfg, now: time.Now, newID: uuid.NewString}
}

func (uc *JobUseCase) List(ctx context.Context, key model.UserKey) (*JobList, error) {
	jobs, err := uc.repo.Job().List(ctx, key)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list jobs")
	}
	return &JobList{Jobs: jobs, MaxJobs: uc.cfg.MaxJobsPerUser}, nil
}

// Create adds a job after checking with Slack that the channel exists, is not
// archived, and has both Robin and the user as members.
func (uc *JobUseCase) Create(ctx context.Context, key model.UserKey, in JobInput) (*model.Job, error) {
	vals := []goerr.Option{goerr.V("team_id", key.TeamID), goerr.V("user_id", key.UserID), goerr.V("channel_id", in.ChannelID)}

	schedule := model.DailySchedule{Hour: in.Hour, Minute: in.Minute, TimeZone: in.TimeZone}
	if !in.Kind.Known() {
		return nil, goerr.Wrap(ErrJobInvalidInput, "unknown job kind", append(vals, goerr.V("kind", in.Kind))...)
	}
	if err := model.ValidateJobChannelID(in.ChannelID); err != nil {
		return nil, goerr.Wrap(ErrJobInvalidInput, "invalid job channel", append(vals, goerr.V("cause", err.Error()))...)
	}
	if err := schedule.Validate(); err != nil {
		return nil, goerr.Wrap(ErrJobInvalidInput, "invalid job schedule",
			append(vals, goerr.V("hour", in.Hour), goerr.V("minute", in.Minute), goerr.V("time_zone", in.TimeZone))...)
	}

	ch, err := uc.bot.GetChannel(ctx, in.ChannelID)
	if errors.Is(err, interfaces.ErrSlackChannelNotFound) {
		return nil, goerr.Wrap(ErrJobChannelNotFound, "job channel not found", vals...)
	}
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the job channel", vals...)
	}
	if ch.IsArchived {
		return nil, goerr.Wrap(ErrJobChannelArchived, "job channel is archived", vals...)
	}

	botID, err := uc.bot.BotUserID(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to identify the bot", vals...)
	}
	members, err := uc.bot.ChannelMembers(ctx, in.ChannelID, []model.SlackUserID{botID, key.UserID})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the job channel members", vals...)
	}
	if !members[botID] {
		return nil, goerr.Wrap(ErrJobRobinNotInChannel, "robin is not in the job channel", vals...)
	}
	if !members[key.UserID] {
		return nil, goerr.Wrap(ErrJobUserNotInChannel, "user is not in the job channel", vals...)
	}

	now := uc.now()
	next, err := schedule.Next(now)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to compute the first run", vals...)
	}
	job := &model.Job{
		TeamID:      key.TeamID,
		UserID:      key.UserID,
		ID:          model.JobID(uc.newID()),
		Kind:        in.Kind,
		ChannelID:   in.ChannelID,
		ChannelName: ch.Name,
		Schedule:    schedule,
		NextRunAt:   next,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := uc.repo.Job().Create(ctx, key, job, uc.cfg.MaxJobsPerUser); err != nil {
		if errors.Is(err, interfaces.ErrJobLimitReached) {
			return nil, goerr.Wrap(ErrJobLimitReached, "user has the most jobs allowed", vals...)
		}
		return nil, goerr.Wrap(err, "failed to create job", vals...)
	}
	return job, nil
}

// Delete removes the user's job. A job the user does not have is not an error.
func (uc *JobUseCase) Delete(ctx context.Context, key model.UserKey, id model.JobID) error {
	if err := uc.repo.Job().Delete(ctx, key, id); err != nil {
		return goerr.Wrap(err, "failed to delete job", goerr.V("job_id", id))
	}
	return nil
}
