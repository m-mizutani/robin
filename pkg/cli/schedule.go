package cli

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/robin/pkg/cli/config"
	"github.com/m-mizutani/robin/pkg/usecase"
	"github.com/m-mizutani/robin/pkg/utils/safe"
)

type scheduleConfig struct {
	file       config.File
	repository config.Repository
	slackBot   config.SlackBot
	llm        config.LLM
	scheduler  config.Scheduler
}

func (c *scheduleConfig) flags() []cli.Flag {
	var flags []cli.Flag
	flags = append(flags, c.file.Flags()...)
	flags = append(flags, c.repository.Flags()...)
	flags = append(flags, c.slackBot.Flags()...)
	flags = append(flags, c.llm.Flags()...)
	flags = append(flags, c.scheduler.Flags()...)
	return flags
}

func (c *scheduleConfig) validate() error {
	// The job settings are written by serve; an in-memory repository of this
	// process is always empty.
	if c.repository.IsMemory() {
		return goerr.New("schedule needs the firestore repository that serve uses")
	}
	if err := c.repository.Validate(); err != nil {
		return err
	}
	if err := c.slackBot.Validate(true); err != nil {
		return err
	}
	if err := c.llm.Validate(true); err != nil {
		return err
	}
	return c.scheduler.Validate()
}

func cmdSchedule() *cli.Command {
	var cfg scheduleConfig
	return &cli.Command{
		Name:  "schedule",
		Usage: "Run every scheduled job that is due once, then exit. Start it every few minutes from a scheduler such as cron",
		Flags: cfg.flags(),
		Action: func(ctx context.Context, _ *cli.Command) error {
			return runSchedule(ctx, &cfg)
		},
	}
}

func runSchedule(ctx context.Context, cfg *scheduleConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	settings, err := cfg.file.Load(ctx)
	if err != nil {
		return err
	}
	repo, err := cfg.repository.Configure(ctx)
	if err != nil {
		return err
	}
	defer safe.Close(ctx, repo)

	jobs, err := newJobs(ctx, settings, &cfg.llm, cfg.slackBot.Configure())
	if err != nil {
		return err
	}
	scheduler, err := usecase.NewScheduler(repo, jobs, schedulerConfig(&cfg.scheduler))
	if err != nil {
		return goerr.Wrap(err, "failed to build the scheduler")
	}
	return scheduler.RunDue(ctx)
}
