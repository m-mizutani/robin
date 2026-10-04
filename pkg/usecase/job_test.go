package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/robin/pkg/domain/interfaces"
	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/repository/memory"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/usecase/usecasetest"
)

var (
	jobOwner     = model.UserKey{TeamID: "T0123", UserID: "U0ALICE"}
	jobCreatedAt = time.Date(2026, 10, 4, 23, 30, 0, 0, time.UTC)
)

type jobFixture struct {
	repo *memory.Memory
	bot  *usecasetest.SlackBot
	uc   *usecase.JobUseCase
	ids  int
}

func newJobFixture(t *testing.T) *jobFixture {
	t.Helper()
	f := &jobFixture{repo: memory.New(), bot: usecasetest.NewSlackBot()}
	f.bot.Channels["C0GENERAL"] = &model.SlackChannel{ID: "C0GENERAL", Name: "general"}
	f.bot.Members["C0GENERAL"] = []model.SlackUserID{"U0ROBIN", jobOwner.UserID}
	f.uc = usecase.NewJobUseCase(f.repo, f.bot, usecase.JobConfig{MaxJobsPerUser: 10})
	f.uc.SetClockForTest(func() time.Time { return jobCreatedAt }, func() string {
		f.ids++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", f.ids)
	})
	return f
}

func helloInput(channelID string) usecase.JobInput {
	return usecase.JobInput{Kind: model.JobKindHello, ChannelID: channelID, Hour: 9, Minute: 0, TimeZone: "Asia/Tokyo"}
}

func storedJobs(t *testing.T, repo interfaces.Repository, key model.UserKey) []*model.Job {
	t.Helper()
	jobs, err := repo.Job().List(context.Background(), key)
	gt.NoError(t, err).Required()
	return jobs
}

func TestJobUseCase_Create(t *testing.T) {
	f := newJobFixture(t)

	job, err := f.uc.Create(context.Background(), jobOwner, helloInput("C0GENERAL"))
	gt.NoError(t, err).Required()

	want := &model.Job{
		TeamID:      jobOwner.TeamID,
		UserID:      jobOwner.UserID,
		ID:          "00000000-0000-4000-8000-000000000001",
		Kind:        model.JobKindHello,
		ChannelID:   "C0GENERAL",
		ChannelName: "general",
		Schedule:    model.DailySchedule{Hour: 9, Minute: 0, TimeZone: "Asia/Tokyo"},
		NextRunAt:   time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		CreatedAt:   jobCreatedAt,
		UpdatedAt:   jobCreatedAt,
	}
	gt.Equal(t, job, want)
	stored := storedJobs(t, f.repo, jobOwner)
	gt.A(t, stored).Length(1).Required()
	gt.Equal(t, stored[0], want)

	gt.Equal(t, f.bot.Recorded(), []usecasetest.SlackCall{
		{Method: "GetChannel", ChannelID: "C0GENERAL"},
		{Method: "BotUserID"},
		{Method: "ChannelMembers", ChannelID: "C0GENERAL", Text: "U0ROBIN,U0ALICE"},
	})
}

func TestJobUseCase_CreateRejectsInvalidInput(t *testing.T) {
	cases := map[string]func(in *usecase.JobInput){
		"unknown kind":         func(in *usecase.JobInput) { in.Kind = "unknown" },
		"channel name":         func(in *usecase.JobInput) { in.ChannelID = "general" },
		"DM":                   func(in *usecase.JobInput) { in.ChannelID = "D0123ABCD" },
		"hour out of range":    func(in *usecase.JobInput) { in.Hour = 24 },
		"minute out of range":  func(in *usecase.JobInput) { in.Minute = -1 },
		"unknown time zone":    func(in *usecase.JobInput) { in.TimeZone = "Mars/Base" },
		"empty time zone name": func(in *usecase.JobInput) { in.TimeZone = "" },
		"process time zone":    func(in *usecase.JobInput) { in.TimeZone = "Local" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newJobFixture(t)
			in := helloInput("C0GENERAL")
			mutate(&in)
			_, err := f.uc.Create(context.Background(), jobOwner, in)
			gt.Error(t, err).Is(usecase.ErrJobInvalidInput)
			gt.A(t, f.bot.Recorded()).Length(0)
			gt.A(t, storedJobs(t, f.repo, jobOwner)).Length(0)
		})
	}
}

func TestJobUseCase_CreateChecksTheChannel(t *testing.T) {
	cases := map[string]struct {
		setup func(f *jobFixture)
		want  error
	}{
		"channel not found": {
			setup: func(f *jobFixture) { delete(f.bot.Channels, "C0GENERAL") },
			want:  usecase.ErrJobChannelNotFound,
		},
		"archived channel": {
			setup: func(f *jobFixture) { f.bot.Channels["C0GENERAL"].IsArchived = true },
			want:  usecase.ErrJobChannelArchived,
		},
		"robin is not a member": {
			setup: func(f *jobFixture) { f.bot.Members["C0GENERAL"] = []model.SlackUserID{jobOwner.UserID} },
			want:  usecase.ErrJobRobinNotInChannel,
		},
		"the user is not a member": {
			setup: func(f *jobFixture) { f.bot.Members["C0GENERAL"] = []model.SlackUserID{"U0ROBIN"} },
			want:  usecase.ErrJobUserNotInChannel,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newJobFixture(t)
			c.setup(f)
			_, err := f.uc.Create(context.Background(), jobOwner, helloInput("C0GENERAL"))
			gt.Error(t, err).Is(c.want)
			gt.A(t, storedJobs(t, f.repo, jobOwner)).Length(0)
		})
	}
}

func TestJobUseCase_CreateSlackFailure(t *testing.T) {
	slackErr := errors.New("ratelimited")
	cases := map[string]func(b *usecasetest.SlackBot){
		"GetChannel":     func(b *usecasetest.SlackBot) { b.ChannelErr = slackErr },
		"BotUserID":      func(b *usecasetest.SlackBot) { b.BotIDErr = slackErr },
		"ChannelMembers": func(b *usecasetest.SlackBot) { b.MembersErr = slackErr },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newJobFixture(t)
			setup(f.bot)
			_, err := f.uc.Create(context.Background(), jobOwner, helloInput("C0GENERAL"))
			gt.Error(t, err).Is(slackErr)
			for _, known := range []error{usecase.ErrJobInvalidInput, usecase.ErrJobChannelNotFound,
				usecase.ErrJobChannelArchived, usecase.ErrJobRobinNotInChannel, usecase.ErrJobUserNotInChannel, usecase.ErrJobLimitReached} {
				gt.Bool(t, errors.Is(err, known)).False()
			}
			gt.A(t, storedJobs(t, f.repo, jobOwner)).Length(0)
		})
	}
}

func TestJobUseCase_CreateStopsAtTheLimit(t *testing.T) {
	f := newJobFixture(t)
	for i := 0; i < 10; i++ {
		_, err := f.uc.Create(context.Background(), jobOwner, helloInput("C0GENERAL"))
		gt.NoError(t, err).Required()
	}
	_, err := f.uc.Create(context.Background(), jobOwner, helloInput("C0GENERAL"))
	gt.Error(t, err).Is(usecase.ErrJobLimitReached)
	gt.A(t, storedJobs(t, f.repo, jobOwner)).Length(10)
}

func TestJobUseCase_ListAndDelete(t *testing.T) {
	f := newJobFixture(t)
	other := model.UserKey{TeamID: jobOwner.TeamID, UserID: "U0BOB"}
	f.bot.Members["C0GENERAL"] = append(f.bot.Members["C0GENERAL"], other.UserID)

	mine, err := f.uc.Create(context.Background(), jobOwner, helloInput("C0GENERAL"))
	gt.NoError(t, err).Required()
	theirs, err := f.uc.Create(context.Background(), other, helloInput("C0GENERAL"))
	gt.NoError(t, err).Required()

	list, err := f.uc.List(context.Background(), jobOwner)
	gt.NoError(t, err).Required()
	gt.Number(t, list.MaxJobs).Equal(10)
	gt.A(t, list.Jobs).Length(1).Required()
	gt.Value(t, list.Jobs[0].ID).Equal(mine.ID)

	// Deleting another user's job ID changes nothing.
	gt.NoError(t, f.uc.Delete(context.Background(), jobOwner, theirs.ID))
	gt.A(t, storedJobs(t, f.repo, other)).Length(1)

	gt.NoError(t, f.uc.Delete(context.Background(), jobOwner, mine.ID))
	list, err = f.uc.List(context.Background(), jobOwner)
	gt.NoError(t, err).Required()
	gt.A(t, list.Jobs).Length(0)
}
